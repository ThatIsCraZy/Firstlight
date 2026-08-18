package console

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"sync"
	"time"

	"firstlight/internal/ilo"
	"firstlight/internal/keyboardmap"
	"firstlight/internal/kvm"
)

const connectionPollInterval = 500 * time.Millisecond

type Session struct {
	ctx    context.Context
	cancel context.CancelFunc
	logf   func(format string, args ...any)

	mu                sync.Mutex
	handle            string
	address           string
	connected         bool
	inputReady        bool
	shared            bool
	closed            bool
	protocolVersion   int
	width             int
	height            int
	revision          uint64
	frameRevision     uint64
	power             string
	postCode          string
	disconnectReason  string
	openedAt          time.Time
	lastFrameAt       time.Time
	insecureTransport bool
	notify            chan struct{}
	client            *ilo.Client
	conn              *kvm.Conn
	cmdConn           *kvm.Conn
	decoder           *kvm.Decoder
	keyboardMaps      *keyboardmap.Registry
	isoRoot           *ISORoot
	virtualMediaData  virtualMediaConnectionData
	mouseX            int
	mouseY            int
	mouseButtons      byte
	decoderMu         sync.Mutex
	operationMu       sync.Mutex
	managementMu      sync.Mutex
	dedupeMu          sync.Mutex
	operations        map[string]*operationRecord
	virtualMediaMu    sync.Mutex
	virtualMedia      managedVirtualMedia
	virtualMediaPath  string
	virtualMediaName  string
	virtualMediaSize  int64
	closeOnce         sync.Once
	done              chan struct{}
}

type operationRecord struct {
	key      string
	done     chan struct{}
	value    any
	err      error
	finished bool
}

func openSession(rootCtx, connectCtx context.Context, opts OpenOptions, isoRoot *ISORoot, logf func(string, ...any)) (*Session, error) {
	validated, err := opts.validate()
	if err != nil {
		return nil, err
	}
	host, _, err := ilo.ParseAddress(validated.Address)
	if err != nil {
		return nil, err
	}
	client, err := ilo.NewClient(ilo.Options{
		Addr:       validated.Address,
		VerifyCert: !validated.InsecureSkipVerify,
	})
	if err != nil {
		return nil, err
	}
	loggedIn := false
	defer func() {
		if loggedIn {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = client.Logout(ctx)
		}
	}()
	if _, err := client.Login(connectCtx, validated.Username, validated.Password); err != nil {
		return nil, fmt.Errorf("iLO login failed: %w", err)
	}
	loggedIn = true
	rc, err := client.GetRCInfo(connectCtx)
	if err != nil {
		return nil, fmt.Errorf("iLO RcInfo failed: %w", err)
	}
	defer func() {
		clear(rc.MasterKey)
		clear(rc.CommandKey)
	}()
	if !rc.Enabled {
		return nil, errors.New("remote console is disabled on this iLO")
	}

	info := newKVMInfo(host, rc.RCPort, client.SessionKey(), rc, kvm.ChannelKVM)
	inKey, outKey := kvm.DeriveKeys(rc.MasterKey)
	conn, status, err := kvm.DialWithKeys(connectCtx, info, inKey, outKey)
	clear(inKey)
	clear(outKey)
	if err != nil {
		return nil, fmt.Errorf("KVM connect failed: %w", err)
	}
	if status != kvm.StatusSuccess {
		return nil, errors.New(status.Error())
	}
	shared := false

	var cmdConn *kvm.Conn
	if !shared {
		cmdInfo := newKVMInfo(host, rc.RCPort, client.SessionKey(), rc, kvm.ChannelCmd)
		cmdIn, cmdOut := kvm.DeriveKeyPair(rc.MasterKey, 1)
		candidate, cmdStatus, cmdErr := kvm.DialWithKeys(connectCtx, cmdInfo, cmdIn, cmdOut)
		clear(cmdIn)
		clear(cmdOut)
		if cmdErr == nil && cmdStatus == kvm.StatusSuccess {
			cmdConn = candidate
		} else {
			if candidate != nil {
				_ = candidate.Close()
			}
			if logf != nil {
				logf("command channel unavailable address=%q status=%s error=%v", validated.Address, cmdStatus, cmdErr)
			}
		}
	}

	sessionCtx, cancel := context.WithCancel(rootCtx)
	now := time.Now().UTC()
	s := &Session{
		ctx:               sessionCtx,
		cancel:            cancel,
		logf:              logf,
		address:           validated.Address,
		connected:         true,
		shared:            shared,
		protocolVersion:   rc.ProtocolVersion,
		width:             800,
		height:            600,
		revision:          1,
		power:             "unknown",
		openedAt:          now,
		insecureTransport: validated.InsecureSkipVerify,
		notify:            make(chan struct{}),
		client:            client,
		conn:              conn,
		cmdConn:           cmdConn,
		decoder:           kvm.NewDecoder(800, 600),
		keyboardMaps:      keyboardmap.BuiltInRegistry(),
		isoRoot:           isoRoot,
		virtualMediaData:  newVirtualMediaConnectionData(host, client.SessionKey(), rc),
		operations:        make(map[string]*operationRecord),
		done:              make(chan struct{}),
	}
	loggedIn = false
	go s.readLoop(conn, rc.ProtocolVersion <= 1)
	if cmdConn != nil {
		go s.readCommandLoop(cmdConn)
	}
	return s, nil
}

func newKVMInfo(host string, port uint16, sessionKey string, rc *ilo.RCInfo, channel kvm.Channel) kvm.Info {
	info := kvm.Info{
		Host:            host,
		Port:            port,
		SessionKey:      sessionKey,
		ProtocolVersion: rc.ProtocolVersion,
		Command:         kvm.CommandNew,
		Channel:         channel,
	}
	if rc.ProtocolVersion <= 1 {
		info.Legacy = &kvm.LegacyOptions{
			EncryptionKey:     append([]byte(nil), rc.MasterKey...),
			EncryptionKeyText: rc.LegacyKeyText,
			CommandKey:        append([]byte(nil), rc.CommandKey...),
			EncryptSessionKey: rc.OptionalFeatures["ENCRYPT_KEY"],
			EncryptVMKey:      rc.OptionalFeatures["ENCRYPT_VMKEY"],
			EncryptCommand:    rc.OptionalFeatures["ENCRYPT_CMD"],
		}
	}
	return info
}

func (s *Session) readLoop(conn *kvm.Conn, legacy bool) {
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(connectionPollInterval))
		n, readErr := conn.Read(buf)
		if n > 0 {
			s.decoderMu.Lock()
			previousEncryptionID := s.decoder.EncryptionID()
			feedErr := s.decoder.Feed(buf[:n])
			if legacy && s.decoder.EncryptionID() != previousEncryptionID {
				if err := conn.SetLegacyKVMEncryption(s.decoder.Encryption()); err != nil {
					s.decoderMu.Unlock()
					s.markDisconnected(fmt.Sprintf("legacy KVM encryption failed: %v", err))
					return
				}
			}
			ready := s.decoder.ReadyToWrite()
			frameRevision := s.decoder.FrameRevision()
			bounds := s.decoder.Framebuffer.Image().Bounds()
			s.decoderMu.Unlock()
			if feedErr != nil && s.logf != nil {
				s.logf("KVM decoder warning address=%q bytes=%d error=%v", s.address, n, feedErr)
			}
			if ready {
				firstReady := false
				now := time.Now().UTC()
				s.mu.Lock()
				if !s.inputReady {
					s.inputReady = true
					firstReady = true
				}
				s.width = bounds.Dx()
				s.height = bounds.Dy()
				frameChanged := frameRevision > s.frameRevision
				if frameChanged {
					s.frameRevision = frameRevision
					s.lastFrameAt = now
				}
				if firstReady || frameChanged {
					s.signalChangeLocked()
				}
				s.mu.Unlock()
				if firstReady {
					s.operationMu.Lock()
					_ = conn.SendAllKeysUp()
					s.operationMu.Unlock()
				}
			}
		}
		if readErr != nil {
			if isTimeoutError(readErr) {
				continue
			}
			s.markDisconnected(readErr.Error())
			return
		}
	}
}

const (
	commandServerPower  = 3
	commandPOSTCode     = 5
	commandShareRequest = 9
)

func (s *Session) readCommandLoop(conn *kvm.Conn) {
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(connectionPollInterval))
		packet, err := conn.ReadCommandPacket()
		if err != nil {
			if isTimeoutError(err) {
				continue
			}
			s.mu.Lock()
			if s.cmdConn == conn {
				s.cmdConn = nil
				s.power = "unavailable"
				s.signalChangeLocked()
			}
			s.mu.Unlock()
			return
		}
		switch packet.Command {
		case commandServerPower:
			if len(packet.Data) < 1 {
				continue
			}
			power := "off"
			if packet.Data[0] != 0 {
				power = "on"
			}
			s.mu.Lock()
			s.power = power
			s.signalChangeLocked()
			s.mu.Unlock()
		case commandPOSTCode:
			if len(packet.Data) < 2 {
				continue
			}
			s.mu.Lock()
			s.postCode = fmt.Sprintf("%02X%02X", packet.Data[1], packet.Data[0])
			s.signalChangeLocked()
			s.mu.Unlock()
		case commandShareRequest:
			if s.protocolVersion <= 1 {
				_ = conn.SendLegacyShareDecision(false)
			}
		}
	}
}

func isTimeoutError(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

func (s *Session) setHandle(handle string) {
	s.mu.Lock()
	s.handle = handle
	s.mu.Unlock()
}

func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked()
}

func (s *Session) stateLocked() State {
	return State{
		Handle:            s.handle,
		Address:           s.address,
		Connected:         s.connected,
		InputReady:        s.inputReady,
		Shared:            s.shared,
		ProtocolVersion:   s.protocolVersion,
		Width:             s.width,
		Height:            s.height,
		Revision:          s.revision,
		FrameRevision:     s.frameRevision,
		ImageAvailable:    s.frameRevision > 0,
		Power:             s.power,
		POSTCode:          s.postCode,
		DisconnectReason:  s.disconnectReason,
		OpenedAt:          s.openedAt,
		LastFrameAt:       s.lastFrameAt,
		InsecureTransport: s.insecureTransport,
	}
}

func (s *Session) Observe(ctx context.Context, afterRevision uint64, wait time.Duration) (State, []byte, error) {
	if wait < 0 {
		wait = 0
	}
	if wait > MaximumObserveWait {
		wait = MaximumObserveWait
	}
	deadline := time.Now().Add(wait)
	for {
		s.mu.Lock()
		state := s.stateLocked()
		notify := s.notify
		s.mu.Unlock()
		if state.FrameRevision > afterRevision || !state.Connected || wait == 0 {
			image, err := s.snapshotPNG(state.ImageAvailable)
			return state, image, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			image, err := s.snapshotPNG(state.ImageAvailable)
			return state, image, err
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return State{}, nil, ctx.Err()
		case <-notify:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			image, err := s.snapshotPNG(state.ImageAvailable)
			return state, image, err
		}
	}
}

func (s *Session) snapshotPNG(available bool) ([]byte, error) {
	if !available {
		return nil, nil
	}
	s.decoderMu.Lock()
	defer s.decoderMu.Unlock()
	if s.decoder == nil || s.decoder.Framebuffer == nil || s.decoder.Framebuffer.Image() == nil {
		return nil, nil
	}
	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&buf, s.decoder.Framebuffer.Image()); err != nil {
		return nil, fmt.Errorf("encode console frame: %w", err)
	}
	return buf.Bytes(), nil
}

func (s *Session) signalChangeLocked() {
	s.revision++
	close(s.notify)
	s.notify = make(chan struct{})
}

func (s *Session) markDisconnected(reason string) {
	s.mu.Lock()
	if !s.connected {
		s.mu.Unlock()
		return
	}
	s.connected = false
	s.inputReady = false
	s.disconnectReason = reason
	conn, cmdConn, client := s.conn, s.cmdConn, s.client
	s.conn, s.cmdConn, s.client = nil, nil, nil
	s.signalChangeLocked()
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	if cmdConn != nil {
		_ = cmdConn.Close()
	}
	if client != nil {
		s.managementMu.Lock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = client.Logout(ctx)
		cancel()
		s.managementMu.Unlock()
	}
	_ = s.closeVirtualMedia()
	s.mu.Lock()
	clear(s.virtualMediaData.masterKey)
	s.virtualMediaData = virtualMediaConnectionData{}
	s.mu.Unlock()
}

func (s *Session) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.operationMu.Lock()
		s.mu.Lock()
		conn := s.conn
		width, height := s.width, s.height
		lastX, lastY := s.mouseX, s.mouseY
		s.mu.Unlock()
		if conn != nil {
			_ = conn.SendAllKeysUp()
			_ = conn.SendMouse(lastX, lastY, 0, 0, width, height, 0, 0)
		}
		s.operationMu.Unlock()

		s.cancel()
		closeErr = errors.Join(closeErr, s.closeVirtualMedia())
		s.mu.Lock()
		s.closed = true
		s.connected = false
		s.inputReady = false
		s.disconnectReason = "closed"
		conn, cmdConn, client := s.conn, s.cmdConn, s.client
		s.conn, s.cmdConn, s.client = nil, nil, nil
		clear(s.virtualMediaData.masterKey)
		s.virtualMediaData = virtualMediaConnectionData{}
		s.signalChangeLocked()
		s.mu.Unlock()
		if conn != nil {
			closeErr = errors.Join(closeErr, conn.Close())
		}
		if cmdConn != nil {
			closeErr = errors.Join(closeErr, cmdConn.Close())
		}
		if client != nil {
			s.managementMu.Lock()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			closeErr = errors.Join(closeErr, client.Logout(ctx))
			cancel()
			s.managementMu.Unlock()
		}
		close(s.done)
	})
	return closeErr
}

func (s *Session) Done() <-chan struct{} {
	return s.done
}

func (s *Session) ExecuteOnce(ctx context.Context, operationID, key string, fn func() (any, error)) (any, error) {
	if operationID == "" {
		return nil, errors.New("operation_id is required")
	}
	if len(operationID) > 128 {
		return nil, errors.New("operation_id must not exceed 128 characters")
	}
	s.dedupeMu.Lock()
	if existing := s.operations[operationID]; existing != nil {
		if existing.key != key {
			s.dedupeMu.Unlock()
			return nil, errors.New("operation_id was already used with different arguments")
		}
		done := existing.done
		s.dedupeMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-done:
			return existing.value, existing.err
		}
	}
	record := &operationRecord{key: key, done: make(chan struct{})}
	s.operations[operationID] = record
	s.dedupeMu.Unlock()

	value, err := fn()
	s.dedupeMu.Lock()
	record.value, record.err, record.finished = value, err, true
	close(record.done)
	if len(s.operations) > 256 {
		for id, candidate := range s.operations {
			if id != operationID && candidate.finished {
				delete(s.operations, id)
				if len(s.operations) <= 256 {
					break
				}
			}
		}
	}
	s.dedupeMu.Unlock()
	return value, err
}
