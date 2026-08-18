package console

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ilo-kvm/internal/ilo"
	"ilo-kvm/internal/kvm"
	"ilo-kvm/internal/vmedia"
)

const virtualMediaConnectTimeout = 30 * time.Second

type managedVirtualMedia interface {
	Close() error
	Done() <-chan struct{}
}

type virtualMediaConnectionData struct {
	host              string
	consolePort       uint16
	port              uint16
	protocolVersion   int
	sessionKey        string
	masterKey         []byte
	legacyKeyText     string
	encryptSessionKey bool
}

func newVirtualMediaConnectionData(host, sessionKey string, rc *ilo.RCInfo) virtualMediaConnectionData {
	if rc == nil {
		return virtualMediaConnectionData{}
	}
	return virtualMediaConnectionData{
		host:              host,
		consolePort:       rc.RCPort,
		port:              rc.VMPort,
		protocolVersion:   rc.ProtocolVersion,
		sessionKey:        sessionKey,
		masterKey:         append([]byte(nil), rc.MasterKey...),
		legacyKeyText:     rc.LegacyKeyText,
		encryptSessionKey: rc.OptionalFeatures["ENCRYPT_VMKEY"],
	}
}

func (s *Session) VirtualMediaStatus() VirtualMediaStatus {
	s.virtualMediaMu.Lock()
	defer s.virtualMediaMu.Unlock()
	s.clearFinishedVirtualMediaLocked()
	return s.virtualMediaStatusLocked()
}

func (s *Session) MountISO(ctx context.Context, requestedPath string) (VirtualMediaStatus, error) {
	if s.isoRoot == nil {
		return s.VirtualMediaStatus(), errors.New("ISO mounting is disabled; configure -iso-root")
	}
	path, name, err := s.isoRoot.ResolveISOPath(requestedPath)
	if err != nil {
		return s.VirtualMediaStatus(), err
	}

	s.virtualMediaMu.Lock()
	defer s.virtualMediaMu.Unlock()
	s.clearFinishedVirtualMediaLocked()
	if s.virtualMedia != nil {
		if s.virtualMediaPath == path {
			return s.virtualMediaStatusLocked(), nil
		}
		return s.virtualMediaStatusLocked(), errors.New("an ISO is already mounted for this console")
	}

	data, err := s.virtualMediaDataForMount()
	if err != nil {
		return s.virtualMediaStatusLocked(), err
	}
	iso, err := vmedia.OpenISO(path)
	if err != nil {
		return s.virtualMediaStatusLocked(), errors.New("requested ISO file cannot be opened")
	}
	mountCtx, cancel := context.WithTimeout(ctx, virtualMediaConnectTimeout)
	stop := context.AfterFunc(s.ctx, cancel)
	conn, err := s.dialVirtualMedia(mountCtx, data)
	stop()
	cancel()
	if err != nil {
		_ = iso.Close()
		return s.virtualMediaStatusLocked(), err
	}
	if err := s.contextIsActive(); err != nil {
		_ = conn.Close()
		_ = iso.Close()
		return s.virtualMediaStatusLocked(), err
	}

	media := vmedia.Start(s.ctx, conn, iso, nil)
	s.virtualMedia = media
	s.virtualMediaPath = path
	s.virtualMediaName = name
	s.virtualMediaSize = iso.Size()
	go s.watchVirtualMedia(media)
	return s.virtualMediaStatusLocked(), nil
}

func (s *Session) UnmountISO() (VirtualMediaStatus, error) {
	return s.unmountISO()
}

func (s *Session) unmountISO() (VirtualMediaStatus, error) {
	s.virtualMediaMu.Lock()
	defer s.virtualMediaMu.Unlock()
	s.clearFinishedVirtualMediaLocked()
	if s.virtualMedia == nil {
		return s.virtualMediaStatusLocked(), nil
	}
	media := s.virtualMedia
	s.clearVirtualMediaLocked()
	if err := media.Close(); err != nil {
		return s.virtualMediaStatusLocked(), errors.New("virtual media could not be unmounted")
	}
	return s.virtualMediaStatusLocked(), nil
}

func (s *Session) closeVirtualMedia() error {
	_, err := s.unmountISO()
	return err
}

func (s *Session) virtualMediaDataForMount() (virtualMediaConnectionData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected || s.client == nil {
		return virtualMediaConnectionData{}, errors.New("console is disconnected")
	}
	if s.virtualMediaData.port == 0 {
		return virtualMediaConnectionData{}, errors.New("virtual media is unavailable on this iLO")
	}
	data := s.virtualMediaData
	data.masterKey = append([]byte(nil), s.virtualMediaData.masterKey...)
	data.sessionKey = s.client.SessionKey()
	if data.sessionKey == "" {
		clear(data.masterKey)
		return virtualMediaConnectionData{}, errors.New("virtual media session is unavailable")
	}
	return data, nil
}

func (s *Session) dialVirtualMedia(ctx context.Context, data virtualMediaConnectionData) (vmedia.Conn, error) {
	defer clear(data.masterKey)
	if data.protocolVersion <= 1 {
		conn, err := vmedia.DialLegacy(ctx, vmedia.LegacyInfo{
			Host:              data.host,
			Port:              data.port,
			SessionKey:        data.sessionKey,
			EncryptionKeyText: data.legacyKeyText,
			EncryptSessionKey: data.encryptSessionKey,
			Device:            vmedia.LegacyDeviceCDROM,
		})
		if err != nil {
			return nil, errors.New("virtual media connection failed")
		}
		return conn, nil
	}
	if err := s.ensureVirtualMediaCommandChannel(ctx, data); err != nil {
		return nil, err
	}
	info := kvm.Info{
		Host:            data.host,
		Port:            data.port,
		SessionKey:      data.sessionKey,
		ProtocolVersion: data.protocolVersion,
		Command:         kvm.CommandNew,
		Channel:         kvm.ChannelDisc,
	}
	var lastStatus kvm.Status
	for _, pair := range []int{2, 1, 0} {
		inKey, outKey := kvm.DeriveKeyPair(data.masterKey, pair)
		conn, status, err := kvm.DialWithKeys(ctx, info, inKey, outKey)
		clear(inKey)
		clear(outKey)
		if err == nil && status == kvm.StatusSuccess {
			return conn, nil
		}
		if conn != nil {
			_ = conn.Close()
		}
		lastStatus = status
		if ctx.Err() != nil {
			break
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if lastStatus != kvm.StatusSuccess {
		return nil, fmt.Errorf("virtual media iLO status: %s", lastStatus.Error())
	}
	return nil, errors.New("virtual media connection failed")
}

func (s *Session) ensureVirtualMediaCommandChannel(ctx context.Context, data virtualMediaConnectionData) error {
	s.mu.Lock()
	if s.cmdConn != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	info := kvm.Info{
		Host:            data.host,
		Port:            data.consolePort,
		SessionKey:      data.sessionKey,
		ProtocolVersion: data.protocolVersion,
		Command:         kvm.CommandNew,
		Channel:         kvm.ChannelCmd,
	}
	inKey, outKey := kvm.DeriveKeyPair(data.masterKey, 1)
	candidate, status, err := kvm.DialWithKeys(ctx, info, inKey, outKey)
	clear(inKey)
	clear(outKey)
	if err != nil || status != kvm.StatusSuccess {
		if candidate != nil {
			_ = candidate.Close()
		}
		return errors.New("virtual media command channel is unavailable")
	}

	s.mu.Lock()
	if !s.connected || s.cmdConn != nil {
		s.mu.Unlock()
		_ = candidate.Close()
		if !s.connected {
			return errors.New("console is disconnected")
		}
		return nil
	}
	s.cmdConn = candidate
	s.mu.Unlock()
	go s.readCommandLoop(candidate)
	return nil
}

func (s *Session) contextIsActive() error {
	select {
	case <-s.ctx.Done():
		return errors.New("console is closed")
	default:
		return nil
	}
}

func (s *Session) watchVirtualMedia(media managedVirtualMedia) {
	<-media.Done()
	s.virtualMediaMu.Lock()
	if s.virtualMedia == media {
		s.clearVirtualMediaLocked()
	}
	s.virtualMediaMu.Unlock()
}

func (s *Session) clearFinishedVirtualMediaLocked() {
	if s.virtualMedia == nil {
		return
	}
	select {
	case <-s.virtualMedia.Done():
		s.clearVirtualMediaLocked()
	default:
	}
}

func (s *Session) clearVirtualMediaLocked() {
	s.virtualMedia = nil
	s.virtualMediaPath = ""
	s.virtualMediaName = ""
	s.virtualMediaSize = 0
}

func (s *Session) virtualMediaStatusLocked() VirtualMediaStatus {
	status := VirtualMediaStatus{Enabled: s.isoRoot != nil}
	if s.virtualMedia != nil {
		status.Mounted = true
		status.TransportAlive = true
		status.ISOName = s.virtualMediaName
		status.SizeBytes = s.virtualMediaSize
		if reporter, ok := s.virtualMedia.(interface{ Health() vmedia.Health }); ok {
			health := reporter.Health()
			status.TransportAlive = health.TransportAlive
			status.DeviceReady = health.DeviceReady
			status.ReadBytes = health.ReadBytes
			status.DeliveredBytes = health.DeliveredBytes
		}
	}
	return status
}
