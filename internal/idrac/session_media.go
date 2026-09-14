package idrac

import (
	"context"
	"errors"
	"sync"

	"firstlight/internal/vmedia"
)

// The media half of a console session. It is kept beside the session rather
// than inside it because the two run on separate WebSocket channels and either
// can end without taking the other down.

type mediaState struct {
	mu      sync.Mutex
	session *MediaSession
	path    string
	name    string
	size    int64
}

// MountISO attaches an ISO image as a virtual optical drive. The image is read
// locally and streamed out over the media channel, so the controller never
// opens a connection back to this machine.
func (s *Session) MountISO(ctx context.Context, iso *vmedia.ISO, name string) error {
	if iso == nil {
		return errors.New("no ISO image given")
	}
	if err := s.ready(); err != nil {
		return err
	}
	s.media.mu.Lock()
	defer s.media.mu.Unlock()
	if s.media.session != nil {
		return errors.New("an ISO is already mounted for this console")
	}
	cookie, xsrf := s.client.credentials()
	media, err := StartVirtualMedia(ctx, MediaConfig{
		Host:       s.client.Host(),
		Port:       s.ticket.Port,
		Key1:       s.ticket.Key1,
		Key2:       s.currentKey2(),
		Cookie:     cookie,
		XSRF:       xsrf,
		VerifyCert: s.cfg.VerifyCert,
		ISO:        iso,
		Logf:       s.logf,
	})
	if err != nil {
		return err
	}
	s.media.session = media
	s.media.path = iso.Path()
	s.media.name = name
	s.media.size = iso.Size()
	go s.watchMedia(media)
	return nil
}

// UnmountISO detaches the image.
func (s *Session) UnmountISO() error {
	s.media.mu.Lock()
	media := s.media.session
	s.clearMediaLocked()
	s.media.mu.Unlock()
	if media == nil {
		return nil
	}
	return media.Close()
}

// MediaStatus reports what the virtual drive is doing.
func (s *Session) MediaStatus() (mounted bool, name string, size int64, health vmedia.Health) {
	s.media.mu.Lock()
	defer s.media.mu.Unlock()
	if s.media.session == nil {
		return false, "", 0, vmedia.Health{}
	}
	return true, s.media.name, s.media.size, s.media.session.Health()
}

// MediaPath reports the image currently attached, empty when none is.
func (s *Session) MediaPath() string {
	s.media.mu.Lock()
	defer s.media.mu.Unlock()
	return s.media.path
}

func (s *Session) watchMedia(media *MediaSession) {
	select {
	case <-media.Done():
	case <-s.ctx.Done():
		_ = media.Close()
	}
	s.media.mu.Lock()
	if s.media.session == media {
		s.clearMediaLocked()
	}
	s.media.mu.Unlock()
	s.mu.Lock()
	s.signalLocked()
	s.mu.Unlock()
}

func (s *Session) clearMediaLocked() {
	s.media.session = nil
	s.media.path = ""
	s.media.name = ""
	s.media.size = 0
}

// OtherSessions lists the sessions on the controller apart from this one.
func (s *Session) OtherSessions(ctx context.Context) ([]SessionInfo, error) {
	all, err := s.client.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	var others []SessionInfo
	for _, session := range all {
		if session.ID == s.webSessionID {
			continue
		}
		others = append(others, session)
	}
	return others, nil
}

// TakeOverConsole ends the other web sessions of this user, which releases the
// console slots they hold. The controller keeps only six, has its console
// timeout switched off by default, and refuses video to a joining viewer while
// another one is registered, so a client that died without logging out can
// block the console until someone clears it.
//
// This displaces whoever is on the console, so it never runs on its own: a
// caller reaches it through an explicit request from the operator.
func (s *Session) TakeOverConsole(ctx context.Context) (int, error) {
	closed, err := s.client.CloseWebSessionsFor(ctx, s.cfg.User, s.webSessionID)
	if closed > 0 {
		s.logEvent("closed %d other session(s) of user %q", closed, s.cfg.User)
	}
	return closed, err
}
