package idrac

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"firstlight/internal/vmedia"
	"firstlight/internal/wsx"
)

// Dell carries virtual media on its own WebSocket channel, /vnc/vmedia, in
// the same outbound direction as the console. The client holds the image and
// answers block reads, so the controller never has to reach back to the
// workstation, which is what makes this work through a firewall.
//
// Every message starts with the same five 32-bit little-endian words:
//
//	msgType, imageType, msgLen, vmToken, statusCode
//
// The numbers below come from the console application the iDRAC serves and
// were confirmed against firmware 7.10.30.00.
const (
	mediaMsgReadData  = 0x1    // server asks for blocks, client answers with the same type
	mediaMsgEject     = 0x8    // host ejected the medium, or client unmaps it
	mediaMsgConnected = 0x10   // channel ready, carries the token and a refreshed key
	mediaMsgInquiry   = 0x20   // client announces the medium and its geometry
	mediaMsgNotNeeded = 0x40   // a read the controller no longer needs
	mediaMsgStatus    = 0x100  // success and error codes
	mediaMsgReset     = 0x200  // reset the virtual device
	mediaMsgClose     = 0x400  // client leaves in an orderly way
	mediaMsgHeartBeat = 0x800  // keep-alive, every twenty seconds
	mediaMsgAuthKey2  = 0x2000 // key challenge and answer
	mediaMsgAuthKey3  = 0x4000 // challenge for the legacy Java client
)

// Image types the channel knows.
const (
	mediaImageCD   = 1
	mediaImageDisk = 2
)

const (
	// cdBlockSize is the optical block size the firmware expects.
	cdBlockSize = 2048
	// initialBlocks is how much the browser client pushes without being asked,
	// right after it announces the medium.
	initialBlocks = 32
	// maxBlocksPerRead bounds one answer so a confused controller cannot make
	// the client allocate without limit. The browser reads ahead in 320-block
	// chunks, so this leaves generous headroom.
	maxBlocksPerRead = 4096
	// inquiryPayloadLen is the inquiry message minus the common header, the
	// value the firmware expects in msgLen.
	inquiryPayloadLen = 28
	// readDataPayloadLen is what the browser puts in msgLen for read answers.
	readDataPayloadLen = 28
	// tocDataLen is the length the browser announces for the table of
	// contents. It never sends the bytes themselves, and the firmware does not
	// ask for them, so this client announces the same and stays silent too.
	tocDataLen = 20
)

// Status codes the firmware reports in a message of type mediaMsgStatus.
const (
	mediaStatusSuccess      = 0
	mediaStatusFailed       = 1
	mediaStatusUnsupported  = 4
	mediaStatusDisabled     = 23
	mediaStatusEjectFailed  = 24
	mediaStatusEjectSuccess = 25
	mediaStatusMapped       = 38
	mediaStatusLicense      = 39
	mediaStatusDetached     = 40
	mediaStatusNoSessions   = 41
	mediaStatusSessionCount = 42
	// Firmware 7.10.30.00 confirms an attached image with this code rather
	// than mediaStatusMapped. Dell's own spelling of the constant carries a
	// typo, which is a useful fingerprint that the value is genuine.
	mediaStatusPartitionMounted = 70
	mediaStatusSessionKilled    = 83
	mediaStatusLicenseDisabled  = 84
	mediaStatusLoginDenied      = 89
	mediaStatusNotPrivileged    = 98
	mediaStatusFloppyEmulation  = 100
)

func mediaStatusText(code uint32) string {
	switch code {
	case mediaStatusSuccess:
		return "success"
	case mediaStatusFailed:
		return "failed"
	case mediaStatusUnsupported:
		return "unsupported image type"
	case mediaStatusDisabled:
		return "virtual media is disabled on this iDRAC"
	case mediaStatusEjectFailed:
		return "eject failed"
	case mediaStatusEjectSuccess:
		return "eject succeeded"
	case mediaStatusMapped:
		return "medium mapped"
	case mediaStatusPartitionMounted:
		return "partition mounted"
	case mediaStatusSessionKilled:
		return "session terminated by the controller"
	case mediaStatusLicenseDisabled:
		return "virtual media is disabled by licence"
	case mediaStatusLoginDenied:
		return "login denied"
	case mediaStatusNotPrivileged:
		return "the account lacks the virtual media privilege"
	case mediaStatusFloppyEmulation:
		return "floppy emulation is enabled"
	case mediaStatusLicense:
		return "the licence does not cover virtual media"
	case mediaStatusDetached:
		return "virtual media is in detached mode"
	case mediaStatusNoSessions:
		return "virtual media session limit is zero"
	case mediaStatusSessionCount:
		return "virtual media session limit reached"
	}
	return fmt.Sprintf("status %d", code)
}

// MediaSession streams one ISO image to the controller.
type MediaSession struct {
	socket socket
	iso    *vmedia.ISO
	logf   func(string, ...any)

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	writeMu sync.Mutex

	mu             sync.Mutex
	vmToken        uint32
	key2           string
	blocks         uint32
	announced      bool
	mapped         bool
	transportAlive bool
	readBytes      uint64
	deliveredBytes uint64
	lastStatus     string

	closeOnce sync.Once
}

// MediaConfig describes one virtual media attachment.
type MediaConfig struct {
	Host       string
	Port       uint16
	Key1       string
	Key2       string
	Cookie     string
	XSRF       string
	VerifyCert bool
	ISO        *vmedia.ISO
	Logf       func(format string, args ...any)
}

// StartVirtualMedia opens the media channel and serves the image until the
// session is closed or the controller ejects it. It takes ownership of the
// image and closes it when the session ends.
func StartVirtualMedia(ctx context.Context, cfg MediaConfig) (*MediaSession, error) {
	if cfg.ISO == nil {
		return nil, errors.New("no ISO image given")
	}
	if cfg.Key1 == "" {
		return nil, errors.New("virtual media needs a console ticket")
	}
	header := headerFor(cfg.Cookie, cfg.XSRF, cfg.Host)
	target := fmt.Sprintf("wss://%s:%d/vnc/vmedia?vmk=%s", cfg.Host, cfg.Port, cfg.Key1)
	conn, err := wsx.Dial(ctx, wsx.Options{
		URL:         target,
		Subprotocol: "binary",
		Header:      header,
		TLSConfig:   tlsConfig(cfg.VerifyCert, cfg.Host),
	})
	if err != nil {
		return nil, fmt.Errorf("virtual media channel: %w", err)
	}
	sessionCtx, cancel := context.WithCancel(context.Background())
	size := cfg.ISO.Size()
	m := &MediaSession{
		socket:         conn,
		iso:            cfg.ISO,
		logf:           cfg.Logf,
		ctx:            sessionCtx,
		cancel:         cancel,
		done:           make(chan struct{}),
		key2:           cfg.Key2,
		blocks:         uint32((size + cdBlockSize - 1) / cdBlockSize),
		transportAlive: true,
	}
	go m.readLoop()
	go m.heartBeatLoop()
	return m, nil
}

// Done closes when the media session ends.
func (m *MediaSession) Done() <-chan struct{} { return m.done }

// Health reports what the transport is doing, in the same shape the iLO path
// uses so the user interface can show one status line for both.
func (m *MediaSession) Health() vmedia.Health {
	m.mu.Lock()
	defer m.mu.Unlock()
	return vmedia.Health{
		TransportAlive: m.transportAlive,
		DeviceReady:    m.mapped,
		ReadBytes:      m.readBytes,
		DeliveredBytes: m.deliveredBytes,
	}
}

// LastStatus reports the most recent status message from the firmware.
func (m *MediaSession) LastStatus() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastStatus
}

// Close unmaps the medium and tears the channel down.
func (m *MediaSession) Close() error {
	var err error
	m.closeOnce.Do(func() {
		// Tell the firmware the device is going away, then leave in an orderly
		// way, which is what the browser client does.
		_ = m.send(m.header(mediaMsgEject, mediaImageCD, 0))
		_ = m.send(m.header(mediaMsgClose, 0, 0))
		m.cancel()
		err = m.socket.Close()
		if m.iso != nil {
			_ = m.iso.Close()
		}
		m.mu.Lock()
		m.transportAlive = false
		m.mapped = false
		m.mu.Unlock()
	})
	return err
}

func (m *MediaSession) readLoop() {
	defer close(m.done)
	defer m.cancel()
	// The image is released here as well, so a channel that dies on its own
	// does not leave the file open.
	defer func() {
		if m.iso != nil {
			_ = m.iso.Close()
		}
	}()
	for {
		message, err := m.socket.ReadMessage()
		if err != nil {
			m.mu.Lock()
			m.transportAlive = false
			m.mapped = false
			m.mu.Unlock()
			m.log("virtual media channel closed: %v", err)
			return
		}
		if err := m.handle(message); err != nil {
			m.log("virtual media: %v", err)
			return
		}
		select {
		case <-m.ctx.Done():
			return
		default:
		}
	}
}

func (m *MediaSession) heartBeatLoop() {
	ticker := time.NewTicker(HeartBeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			token := m.vmToken
			m.mu.Unlock()
			if err := m.send(words(mediaMsgHeartBeat, 0, 0, token)); err != nil {
				return
			}
		}
	}
}

// handle dispatches one message from the controller.
func (m *MediaSession) handle(message []byte) error {
	if len(message) < 16 {
		return fmt.Errorf("short message of %d bytes", len(message))
	}
	msgType := binary.LittleEndian.Uint32(message[0:4])
	imageType := binary.LittleEndian.Uint32(message[4:8])
	token := binary.LittleEndian.Uint32(message[12:16])
	var status uint32
	if len(message) >= 20 {
		status = binary.LittleEndian.Uint32(message[16:20])
	}

	switch msgType {
	case mediaMsgAuthKey2:
		return m.sendAuth(token)

	case mediaMsgConnected:
		m.mu.Lock()
		m.vmToken = token
		if refreshed := asciiWords(message[16:]); refreshed != "" {
			m.key2 = refreshed
		}
		m.transportAlive = true
		announced := m.announced
		m.announced = true
		m.mu.Unlock()
		m.log("virtual media channel ready, token %d", token)
		if announced {
			return nil
		}
		return m.announceMedium()

	case mediaMsgReadData:
		return m.serveRead(message)

	case mediaMsgEject:
		m.log("host ejected the virtual medium")
		if err := m.send(m.header(mediaMsgEject, imageType, 0)); err != nil {
			return err
		}
		m.mu.Lock()
		m.mapped = false
		m.mu.Unlock()
		return errors.New("medium ejected by the host")

	case mediaMsgStatus:
		text := mediaStatusText(status)
		m.mu.Lock()
		m.lastStatus = text
		switch status {
		case mediaStatusMapped, mediaStatusPartitionMounted:
			m.mapped = true
		case mediaStatusEjectSuccess:
			m.mapped = false
		}
		m.mu.Unlock()
		m.log("virtual media status: %s", text)
		switch status {
		case mediaStatusDisabled, mediaStatusLicense, mediaStatusDetached,
			mediaStatusNoSessions, mediaStatusSessionCount, mediaStatusUnsupported,
			mediaStatusSessionKilled, mediaStatusLicenseDisabled, mediaStatusLoginDenied,
			mediaStatusNotPrivileged:
			return errors.New(text)
		}
		return nil

	case mediaMsgNotNeeded:
		// The controller dropped a read it had already asked for.
		return nil

	case mediaMsgAuthKey3:
		// Only the retired Java client answers this one.
		return nil
	}
	m.log("unhandled virtual media message type %#x", msgType)
	return nil
}

// sendAuth answers the key challenge. The key travels as six 32-bit words of
// four characters each, little-endian, exactly as the browser packs it.
func (m *MediaSession) sendAuth(token uint32) error {
	m.mu.Lock()
	key := m.key2
	m.vmToken = token
	m.mu.Unlock()
	payload := make([]uint32, 12)
	payload[0] = mediaMsgAuthKey2
	payload[1] = 0
	payload[2] = 0x20
	payload[3] = token
	for i := 0; i < 6; i++ {
		payload[4+i] = packWord(key, i*4)
	}
	return m.send(words(payload...))
}

// announceMedium sends the inquiry and the first blocks. The firmware maps the
// device only once it has both.
func (m *MediaSession) announceMedium() error {
	m.mu.Lock()
	token := m.vmToken
	blocks := m.blocks
	m.mu.Unlock()
	if blocks == 0 {
		return errors.New("the ISO image is empty")
	}
	// Inquiry: header, then geometry. Cylinders, heads and sectors stay zero
	// for optical media; flags carries the read-only bit.
	inquiry := words(
		mediaMsgInquiry, mediaImageCD, inquiryPayloadLen, token,
		blocks, cdBlockSize, 0, 0, 0, 1, tocDataLen,
	)
	if err := m.send(inquiry); err != nil {
		return err
	}
	count := uint32(initialBlocks)
	if blocks < count {
		count = blocks
	}
	return m.sendBlocks(0, count, cdBlockSize)
}

// serveRead answers a block request from the controller.
func (m *MediaSession) serveRead(message []byte) error {
	if len(message) < 28 {
		return fmt.Errorf("read request of %d bytes is too short", len(message))
	}
	startBlock := binary.LittleEndian.Uint32(message[16:20])
	numBlocks := binary.LittleEndian.Uint32(message[20:24])
	blockSize := binary.LittleEndian.Uint32(message[24:28])
	if blockSize == 0 {
		blockSize = cdBlockSize
	}
	if numBlocks == 0 {
		return nil
	}
	if numBlocks > maxBlocksPerRead {
		return fmt.Errorf("controller asked for %d blocks at once", numBlocks)
	}
	return m.sendBlocks(startBlock, numBlocks, blockSize)
}

// sendBlocks reads from the image and answers with a header followed by the
// raw bytes, in two messages, the way the browser client does.
func (m *MediaSession) sendBlocks(startBlock, numBlocks, blockSize uint32) error {
	m.mu.Lock()
	token := m.vmToken
	total := m.blocks
	m.mu.Unlock()

	// A request past the end is clamped rather than refused: the firmware
	// probes beyond the last block while it works out the geometry.
	if startBlock >= total {
		numBlocks = 0
	} else if startBlock+numBlocks > total {
		numBlocks = total - startBlock
	}
	length := int(numBlocks) * int(blockSize)
	data := make([]byte, length)
	if length > 0 {
		chunk, err := m.iso.ReadAt(int64(startBlock)*int64(blockSize), length)
		if err != nil {
			return fmt.Errorf("read ISO at block %d: %w", startBlock, err)
		}
		copy(data, chunk)
		m.mu.Lock()
		m.readBytes += uint64(len(chunk))
		m.mu.Unlock()
	}
	header := words(
		mediaMsgReadData, mediaImageCD, readDataPayloadLen, token,
		startBlock, numBlocks, startBlock+numBlocks, blockSize, uint32(length),
	)
	if err := m.send(header); err != nil {
		return err
	}
	if length == 0 {
		return nil
	}
	if err := m.send(data); err != nil {
		return err
	}
	m.mu.Lock()
	m.deliveredBytes += uint64(length)
	m.mu.Unlock()
	return nil
}

func (m *MediaSession) header(msgType, imageType, payloadLen uint32) []byte {
	m.mu.Lock()
	token := m.vmToken
	m.mu.Unlock()
	return words(msgType, imageType, payloadLen, token)
}

func (m *MediaSession) send(payload []byte) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return m.socket.WriteMessage(payload)
}

func (m *MediaSession) log(format string, args ...any) {
	if m.logf != nil {
		m.logf(format, args...)
	}
}

// words encodes 32-bit values little-endian, the byte order the channel uses.
func words(values ...uint32) []byte {
	out := make([]byte, 4*len(values))
	for i, value := range values {
		binary.LittleEndian.PutUint32(out[4*i:], value)
	}
	return out
}

// packWord folds four characters of the key into one 32-bit word.
func packWord(key string, offset int) uint32 {
	var word uint32
	for i := 0; i < 4; i++ {
		if offset+i < len(key) {
			word |= uint32(key[offset+i]) << (8 * i)
		}
	}
	return word
}

// asciiWords reads a run of 32-bit words back into the string they carry,
// stopping at the first zero byte.
func asciiWords(raw []byte) string {
	out := make([]byte, 0, len(raw))
	for _, b := range raw {
		if b == 0 {
			break
		}
		out = append(out, b)
	}
	return string(out)
}
