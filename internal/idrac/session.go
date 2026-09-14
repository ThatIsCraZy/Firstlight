package idrac

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"image"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"firstlight/internal/rfb"
	"firstlight/internal/wsx"
)

// DefaultEncodings is what the client offers the firmware, most preferred
// first. It matches the list the browser client negotiates for 16-bit colour.
func DefaultEncodings() []int32 {
	return []int32{
		rfb.EncodingCopyRect,
		rfb.EncodingHextile,
		rfb.EncodingRRE,
		rfb.EncodingRaw,
		rfb.EncodingDesktop,
		rfb.EncodingLastRect,
	}
}

// Console sizes the iDRAC reports before the host produces video.
const (
	initialWidth  = 1024
	initialHeight = 768
)

// Config describes one console session.
type Config struct {
	Addr       string
	User       string
	Password   string
	VerifyCert bool
	// Exclusive asks the firmware to drop other viewers instead of joining
	// them. It maps to the RFB shared flag.
	Exclusive bool
	// SeizeOtherSessions closes the other web sessions of this user before the
	// console ticket is taken, which releases the console slots they hold.
	// It displaces whoever is on the console, so it only ever runs because the
	// operator asked for it.
	SeizeOtherSessions bool
	// Logf receives diagnostics. It may be nil.
	Logf func(format string, args ...any)
	// TraceDecoder additionally logs every framebuffer rectangle, which is
	// noisy and only useful when chasing a rendering fault.
	TraceDecoder bool
	// Encodings overrides the encoding list offered to the firmware. Leave it
	// empty for DefaultEncodings.
	Encodings []int32
	// PixelFormat overrides the format requested from the firmware. Nil asks
	// for rfb.RGB565, which is the only format every Dell encoding delivers
	// correctly.
	PixelFormat *rfb.PixelFormat
	// KeepServerFormat skips SetPixelFormat entirely and decodes in whatever
	// format the firmware announced in ServerInit.
	KeepServerFormat bool
}

// State is a snapshot of the session for the user interface.
type State struct {
	Connected   bool
	InputReady  bool
	Shared      bool
	Width       int
	Height      int
	Revision    uint64
	PowerOn     bool
	PowerKnown  bool
	BootDevice  byte
	UserName    string
	ClientID    uint32
	Privileges  uint32
	OtherUsers  []Participant
	FPS         byte
	Disconnect  string
	SharingFrom *SharingRequest
	// WaitingForApproval reports that the handshake succeeded but no video has
	// arrived. The firmware does that when another viewer already holds the
	// console: the session becomes a shared one and stays dark until the
	// current viewer allows it, or until the controller applies the default
	// from VirtualConsole.1.AccessPrivilege, which ships as "Deny Access".
	WaitingForApproval bool
}

// SharingRequest is a pending request from another user to join the session.
type SharingRequest struct {
	ClientID uint32
	Name     string
}

// Session is a live remote console against an iDRAC.
type Session struct {
	cfg    Config
	client *Client
	rfb    *rfb.Conn
	trans  *transport
	logf   func(string, ...any)

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	// ticket is kept because the virtual media channel authenticates with the
	// same pair the console used.
	ticket Ticket
	media  mediaState
	// webSessionID identifies this client's own session on the controller, so
	// a take-over never closes the connection it is running on.
	webSessionID string

	mu       sync.Mutex
	state    State
	key2     string
	notify   chan struct{}
	keyboard rfb.KeyboardState
	buttons  byte
	closed   bool

	writeMu   sync.Mutex
	closeOnce sync.Once
}

// Connect opens a console session: web login, console ticket, WebSocket, the
// Dell key exchange and finally the RFB handshake.
func Connect(ctx context.Context, cfg Config) (*Session, error) {
	if strings.TrimSpace(cfg.User) == "" {
		return nil, errors.New("iDRAC user name is required")
	}
	if cfg.Password == "" {
		return nil, errors.New("iDRAC password is required")
	}
	client, err := NewClient(Options{Addr: cfg.Addr, VerifyCert: cfg.VerifyCert})
	if err != nil {
		return nil, err
	}
	if err := client.Login(ctx, cfg.User, cfg.Password); err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			logoutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = client.Logout(logoutCtx)
			cancel()
		}
	}()

	// The session list is read before the ticket so the client knows which
	// entry is its own, which a later take-over has to keep.
	var webSessionID string
	if sessions, listErr := client.Sessions(ctx); listErr == nil {
		webSessionID = newestWebSessionFor(sessions, cfg.User, "")
	}

	if cfg.SeizeOtherSessions {
		if closed, err := client.CloseWebSessionsFor(ctx, cfg.User, webSessionID); err != nil {
			logEvent(cfg.Logf, "seizing the console: %v", err)
		} else if closed > 0 {
			logEvent(cfg.Logf, "seized the console, closed %d other session(s)", closed)
		}
	}

	ticket, err := client.ConsoleTicket(ctx)
	if err != nil {
		return nil, err
	}
	cookie, xsrf := client.credentials()
	header := headerFor(cookie, xsrf, client.Host())

	target := fmt.Sprintf("wss://%s:%d/vnc/vconsole?vck=%s",
		client.Host(), ticket.Port, url.QueryEscape(ticket.Key1))
	conn, err := wsx.Dial(ctx, wsx.Options{
		URL:         target,
		Subprotocol: "binary",
		Header:      header,
		TLSConfig:   tlsConfig(cfg.VerifyCert, client.Host()),
	})
	if err != nil {
		return nil, fmt.Errorf("console channel: %w", err)
	}

	sessionCtx, cancel := context.WithCancel(context.Background())
	s := &Session{
		cfg:          cfg,
		client:       client,
		logf:         cfg.Logf,
		ctx:          sessionCtx,
		cancel:       cancel,
		done:         make(chan struct{}),
		notify:       make(chan struct{}),
		key2:         ticket.Key2,
		ticket:       ticket,
		webSessionID: webSessionID,
		state: State{
			Connected: true,
			Width:     initialWidth,
			Height:    initialHeight,
		},
	}
	s.trans = newTransport(conn, s.handleControl)
	s.rfb = rfb.New(s.trans)
	if cfg.TraceDecoder && cfg.Logf != nil {
		s.rfb.SetLogger(cfg.Logf)
	}

	if err := s.handshake(ctx); err != nil {
		cancel()
		_ = s.trans.Close()
		return nil, err
	}
	success = true
	go s.readLoop()
	go s.heartBeatLoop()
	go s.watchFirstFrame()
	return s, nil
}

// headerFor builds the request headers both WebSocket channels need.
func headerFor(cookie, xsrf, host string) http.Header {
	header := http.Header{}
	if cookie != "" {
		header.Set("Cookie", cookie)
	}
	if xsrf != "" {
		header.Set("XSRF-TOKEN", xsrf)
	}
	header.Set("Origin", "https://"+host)
	return header
}

func tlsConfig(verify bool, host string) *tls.Config {
	if verify {
		return &tls.Config{ServerName: host}
	}
	return &tls.Config{InsecureSkipVerify: true} //nolint:gosec // iDRACs ship self-signed certificates.
}

// handshake answers the firmware's key challenge and then runs RFB.
func (s *Session) handshake(ctx context.Context) error {
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		deadline = time.Now().Add(30 * time.Second)
	}
	_ = s.trans.SetReadDeadline(deadline)
	defer func() { _ = s.trans.SetReadDeadline(time.Time{}) }()

	// No pixel data flows yet, so the demultiplexer may route every control
	// message it sees. The firmware relies on that: it pushes the client list
	// between the version exchange and the security types.
	s.trans.SetHandshake(true)
	defer s.trans.SetHandshake(false)

	// The firmware opens the conversation with a two-byte request for the
	// second half of the ticket and stays silent until it arrives.
	if err := s.awaitKeyRequest(deadline); err != nil {
		return err
	}
	init, err := s.rfb.Handshake(!s.cfg.Exclusive)
	if err != nil {
		return fmt.Errorf("RFB handshake: %w", err)
	}
	if !s.cfg.KeepServerFormat {
		format := rfb.RGB565
		if s.cfg.PixelFormat != nil {
			format = *s.cfg.PixelFormat
		}
		if err := s.rfb.SetPixelFormat(format); err != nil {
			return err
		}
	}
	s.logEvent("pixel format in use: %+v", s.rfb.Format())
	encodings := s.cfg.Encodings
	if len(encodings) == 0 {
		encodings = DefaultEncodings()
	}
	if err := s.rfb.SetEncodings(encodings); err != nil {
		return err
	}
	if err := s.rfb.RequestUpdate(false, 0, 0, init.Width, init.Height); err != nil {
		return err
	}
	s.mu.Lock()
	s.state.Width = init.Width
	s.state.Height = init.Height
	s.state.InputReady = true
	s.signalLocked()
	s.mu.Unlock()
	s.logEvent("console ready %dx%d name=%q", init.Width, init.Height, init.Name)
	return nil
}

// awaitKeyRequest waits for the key2 challenge and answers it. Some firmware
// revisions send the challenge only after the client shows up, so the reply is
// also sent unprompted after a short wait. The wait runs on a read deadline
// rather than in a goroutine: a goroutine left a second reader on the socket
// once the wait expired, and two readers pull the frame stream apart between
// them.
func (s *Session) awaitKeyRequest(handshakeDeadline time.Time) error {
	wait := time.Now().Add(3 * time.Second)
	if wait.After(handshakeDeadline) {
		wait = handshakeDeadline
	}
	_ = s.trans.SetReadDeadline(wait)
	message, err := s.trans.socket.ReadMessage()
	_ = s.trans.SetReadDeadline(handshakeDeadline)
	switch {
	case err == nil:
		if !IsControlFrame(message) || message[1] != MsgKey2Request {
			// Not the challenge: hand the bytes to the RFB layer instead.
			s.trans.push(message)
			return nil
		}
	case isReadTimeout(err):
		s.logEvent("no key challenge arrived, sending the key unprompted")
	default:
		return fmt.Errorf("console channel closed during the key exchange: %w", err)
	}
	return s.sendRaw(Key2Reply(s.currentKey2()))
}

func isReadTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

func (s *Session) currentKey2() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.key2
}

// readLoop pumps RFB messages and keeps asking for incremental updates.
func (s *Session) readLoop() {
	defer close(s.done)
	defer s.cancel()
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		s.trans.SetBoundary(true)
		update, err := s.rfb.ReadMessage()
		if err != nil {
			s.markDisconnected(err)
			return
		}
		if update == nil {
			continue
		}
		bounds := s.rfb.Screen().Bounds()
		s.mu.Lock()
		s.state.Width = bounds.Dx()
		s.state.Height = bounds.Dy()
		s.state.Revision = s.rfb.Screen().Revision()
		s.state.WaitingForApproval = false
		s.signalLocked()
		s.mu.Unlock()
		// Ask for the next change. Without a standing request the firmware
		// stops sending and the picture freezes.
		if err := s.rfb.RequestUpdate(true, 0, 0, bounds.Dx(), bounds.Dy()); err != nil {
			s.markDisconnected(err)
			return
		}
	}
}

// firstFrameGrace is how long a console may stay dark after the handshake
// before the session is reported as waiting for approval.
const firstFrameGrace = 10 * time.Second

// watchFirstFrame turns a console that never sends video into a state the user
// interface can explain, rather than a black window.
func (s *Session) watchFirstFrame() {
	timer := time.NewTimer(firstFrameGrace)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
		return
	case <-timer.C:
	}
	s.mu.Lock()
	if s.state.Revision == 0 && s.state.Connected {
		s.state.WaitingForApproval = true
		s.signalLocked()
		s.logEvent("no video after %s: the console is held by another session", firstFrameGrace)
	}
	s.mu.Unlock()
}

// heartBeatLoop keeps the console session registered. The firmware ships with
// the console timeout switched off, so a session whose keep-alive stops is
// held open until someone clears it by hand.
func (s *Session) heartBeatLoop() {
	ticker := time.NewTicker(HeartBeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := s.sendRaw(HeartBeat()); err != nil {
				return
			}
		}
	}
}

// handleControl reacts to one Dell control message.
func (s *Session) handleControl(frame []byte) {
	control, err := ParseControl(frame)
	if err != nil {
		s.logEvent("control message ignored: %v", err)
		return
	}
	switch control.Subtype {
	case MsgKey2Request:
		if err := s.sendRaw(Key2Reply(s.currentKey2())); err != nil {
			s.logEvent("key refresh failed: %v", err)
		}
		return
	case MsgClientListAuthKey:
		s.mu.Lock()
		if control.Key2 != "" {
			s.key2 = control.Key2
		}
		s.state.UserName = control.UserName
		s.state.ClientID = control.ClientID
		s.state.Privileges = control.Privileges
		s.state.OtherUsers = control.Clients
		s.signalLocked()
		s.mu.Unlock()
		return
	case MsgHostPowerState:
		s.mu.Lock()
		s.state.PowerOn = control.PowerOn
		s.state.PowerKnown = true
		s.signalLocked()
		s.mu.Unlock()
		return
	case MsgFirstBootDevice:
		s.mu.Lock()
		s.state.BootDevice = control.BootDevice
		s.signalLocked()
		s.mu.Unlock()
		return
	case MsgFPSData:
		s.mu.Lock()
		s.state.FPS = control.FPS
		s.mu.Unlock()
		return
	case MsgSessionIsShared:
		s.mu.Lock()
		s.state.Shared = true
		s.signalLocked()
		s.mu.Unlock()
		return
	case MsgSharingRequest:
		request := &SharingRequest{}
		if len(frame) >= 6 {
			request.ClientID = uint32(frame[2]) | uint32(frame[3])<<8 |
				uint32(frame[4])<<16 | uint32(frame[5])<<24
			request.Name = trimNul(frame[6:])
		}
		s.mu.Lock()
		s.state.SharingFrom = request
		s.signalLocked()
		s.mu.Unlock()
		s.logEvent("another user asks to share the console: %q", request.Name)
		return
	case MsgGracefulShutdown, MsgClientExit:
		s.logEvent("firmware reported %s", SubtypeName(control.Subtype))
		return
	}
	s.logEvent("control message %s (%d bytes)", SubtypeName(control.Subtype), len(frame))
}

// AnswerSharingRequest approves or denies the pending request.
func (s *Session) AnswerSharingRequest(approve bool) error {
	s.mu.Lock()
	request := s.state.SharingFrom
	s.state.SharingFrom = nil
	s.signalLocked()
	s.mu.Unlock()
	if request == nil {
		return errors.New("no sharing request is pending")
	}
	return s.sendRaw(SharingAnswer(approve, request.ClientID))
}

// Screen exposes the framebuffer.
func (s *Session) Screen() *rfb.Screen { return s.rfb.Screen() }

// Frame copies the current framebuffer.
func (s *Session) Frame(dst *image.RGBA) *image.RGBA { return s.rfb.Screen().CopyFrame(dst) }

// State returns a snapshot.
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state
	state.OtherUsers = append([]Participant(nil), s.state.OtherUsers...)
	return state
}

// Changed returns a channel closed on the next state change.
func (s *Session) Changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notify
}

// Done closes when the session ends.
func (s *Session) Done() <-chan struct{} { return s.done }

// Client exposes the authenticated HTTP client for management calls.
func (s *Session) Client() *Client { return s.client }

func (s *Session) signalLocked() {
	close(s.notify)
	s.notify = make(chan struct{})
}

func (s *Session) logEvent(format string, args ...any) {
	logEvent(s.logf, format, args...)
}

func logEvent(logf func(string, ...any), format string, args ...any) {
	if logf != nil {
		logf(format, args...)
	}
}

func (s *Session) markDisconnected(err error) {
	s.mu.Lock()
	connected := s.state.Connected
	s.mu.Unlock()
	if !connected {
		return
	}
	// An RFB parse error ends the session on a socket that is still open, and
	// the transport closes a few lines below. Try the release while there is
	// still something to write to; on a dead line it simply fails.
	s.releaseHeldKeys()

	s.mu.Lock()
	if !s.state.Connected {
		s.mu.Unlock()
		return
	}
	s.state.Connected = false
	s.state.InputReady = false
	if err != nil {
		s.state.Disconnect = err.Error()
	}
	s.signalLocked()
	s.mu.Unlock()
	_ = s.trans.Close()
}

// Close ends the session and the underlying web login.
func (s *Session) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.releaseHeldKeys()
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		_ = s.UnmountISO()
		// The firmware releases the console slot only when the viewer says it
		// is leaving. Without this the session stays on its books.
		_ = s.sendRaw(WindowClose())
		s.cancel()
		err = s.trans.Close()
		logoutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = s.client.Logout(logoutCtx)
		cancel()
		s.markDisconnected(errConsoleClosed)
	})
	return err
}

// releaseHeldKeys puts every key the session is holding back up, so none stays
// stuck on the remote machine. It has to run before the session is marked
// closed: ready() refuses every send after that, and the release would never
// reach the wire.
func (s *Session) releaseHeldKeys() {
	s.mu.Lock()
	held := s.keyboard.Held()
	s.mu.Unlock()
	if !held {
		return
	}
	_ = s.SendKeyboardReport([10]byte{1})
}

func (s *Session) sendRaw(payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.rfb.VendorMessage(payload)
}

func (s *Session) ready() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errConsoleClosed
	}
	if !s.state.Connected {
		if s.state.Disconnect != "" {
			return fmt.Errorf("console is disconnected: %s", s.state.Disconnect)
		}
		return errConsoleClosed
	}
	if !s.state.InputReady {
		return errors.New("console input is not ready yet")
	}
	return nil
}
