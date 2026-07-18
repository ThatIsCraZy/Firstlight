//go:build windows

package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"hpeirc/internal/ilo"
	"hpeirc/internal/kvm"
	"hpeirc/internal/vmedia"
)

const connectionPollInterval = 500 * time.Millisecond

func (w *appWindow) connectConfigured() {
	w.mu.Lock()
	if w.connecting || w.closed {
		w.mu.Unlock()
		return
	}
	w.connecting = true
	w.status = "Connecting..."
	w.logf("connect requested addr=%q user=%q", w.cfg.Addr, w.cfg.User)
	w.mu.Unlock()
	w.updateChrome()

	go func(cfg Config) {
		err := w.connect(cfg)
		w.mu.Lock()
		w.connecting = false
		if w.closed {
			w.mu.Unlock()
			return
		}
		if err != nil {
			w.status = err.Error()
			w.connected = false
			w.logf("connect failed: %v", err)
			w.mu.Unlock()
			w.ui(w.updateChrome)
			return
		}
		w.status = "Connected."
		w.connected = true
		w.logf("connect succeeded")
		w.mu.Unlock()
		w.ui(w.updateChrome)
	}(w.cfg)
}

func (w *appWindow) connect(cfg Config) error {
	if cfg.Addr == "" || cfg.User == "" || cfg.Password == "" {
		return errors.New("addr, name, and password are required")
	}
	host, _, err := ilo.ParseAddress(cfg.Addr)
	if err != nil {
		return err
	}
	w.logf("parsed addr host=%q", host)
	client, err := ilo.NewClient(ilo.Options{Addr: cfg.Addr, VerifyCert: cfg.VerifyCert})
	if err != nil {
		return err
	}
	w.logf("https client ready verify_cert=%v", cfg.VerifyCert)
	if _, err := client.Login(w.ctx, cfg.User, cfg.Password); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}
	w.logf("login ok user=%q", cfg.User)
	handoff := false
	defer func() {
		if handoff {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Logout(ctx)
	}()
	rc, err := client.GetRCInfo(w.ctx)
	if err != nil {
		return fmt.Errorf("RcInfo failed: %w", err)
	}
	w.logf("rcinfo ok source=%s enabled=%v protocol=%d rc_port=%d vm_port=%d master_key_bytes=%d", rc.Source, rc.Enabled, rc.ProtocolVersion, rc.RCPort, rc.VMPort, len(rc.MasterKey))
	if !rc.Enabled {
		return errors.New("remote console is disabled on this iLO")
	}
	info := kvm.Info{Host: host, Port: rc.RCPort, SessionKey: client.SessionKey(), ProtocolVersion: rc.ProtocolVersion, Command: kvm.CommandNew, Channel: kvm.ChannelKVM}
	inKey, outKey := kvm.DeriveKeys(rc.MasterKey)
	conn, status, err := kvm.DialWithKeys(w.ctx, info, inKey, outKey)
	if err != nil {
		return err
	}
	w.logf("kvm dial command=%d status=%s", info.Command, status)
	if status == kvm.StatusBusy && (cfg.Share || cfg.Seize) {
		if cfg.Share {
			info.Command = kvm.CommandShare
		} else {
			info.Command = kvm.CommandAcquire
		}
		conn, status, err = kvm.DialWithKeys(w.ctx, info, inKey, outKey)
		if err != nil {
			return err
		}
	}
	if status != kvm.StatusSuccess {
		return errors.New(status.Error())
	}
	var cmdConn *kvm.Conn
	if cfg.ISOPath == "" {
		var cmdErr error
		cmdConn, cmdErr = w.openCommandChannel(host, client.SessionKey(), rc)
		if cmdErr != nil {
			w.logf("cmd channel unavailable: %v", cmdErr)
		} else {
			w.logf("cmd channel connected")
		}
	}
	var vm *vmedia.Session
	if cfg.ISOPath != "" {
		vm, err = w.startISO(cfg.ISOPath, host, client.SessionKey(), rc)
		if err != nil {
			_ = conn.Close()
			if cmdConn != nil {
				_ = cmdConn.Close()
			}
			return err
		}
	}
	decoder := kvm.NewDecoder(800, 600)
	w.mu.Lock()
	w.client, w.conn, w.vm = client, conn, vm
	if cmdConn != nil {
		w.cmdConn = cmdConn
	}
	w.host, w.sessionKey, w.rcInfo = host, client.SessionKey(), rc
	w.decoder, w.frame = decoder, decoder.Framebuffer.Image()
	w.vmISOPath = ""
	if vm != nil {
		w.vmISOPath = cfg.ISOPath
	}
	w.captured, w.inputReady = false, false
	w.resetCapturedInputLocked()
	w.mu.Unlock()
	handoff = true
	w.logf("kvm connected host=%q port=%d protocol=%d command=%d", info.Host, info.Port, info.ProtocolVersion, info.Command)
	if cmdConn != nil {
		go w.readCommandLoop(cmdConn)
	}
	go w.readLoop(conn)
	return nil
}

func (w *appWindow) readLoop(conn *kvm.Conn) {
	buf := make([]byte, 32*1024)
	readBytes := 0
	lastReadLog := time.Now()
	w.logf("read loop start")
	for {
		select {
		case <-w.ctx.Done():
			w.logf("read loop stop: context done")
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(connectionPollInterval))
		n, err := conn.Read(buf)
		if n > 0 {
			readBytes += n
			if time.Since(lastReadLog) >= time.Second {
				w.logf("rx video bytes=%d input_ready=%v", readBytes, w.decoder.ReadyToWrite())
				readBytes = 0
				lastReadLog = time.Now()
			}
			if feedErr := w.decoder.Feed(buf[:n]); feedErr != nil {
				w.logf("decoder unsupported packet after read n=%d: %v", n, feedErr)
			}
			if w.decoder.ReadyToWrite() {
				sendInitialAllKeysUp := false
				w.mu.Lock()
				if !w.inputReady {
					w.inputReady = true
					sendInitialAllKeysUp = true
				}
				w.frame = w.decoder.Framebuffer.Image()
				w.mu.Unlock()
				if sendInitialAllKeysUp {
					w.logf("tx keyboard initial-all-keys-up")
					_ = conn.SendAllKeysUp()
				}
				w.ui(w.repaint)
			}
		}
		if err != nil {
			if isTimeoutError(err) {
				continue
			}
			w.logf("read loop disconnect: %v", err)
			w.handleDisconnect(fmt.Sprintf("Disconnected: %v", err))
			return
		}
	}
}

func (w *appWindow) handleDisconnect(status string) {
	var vm *vmedia.Session
	var cmdConn *kvm.Conn
	w.mu.Lock()
	w.status = status
	w.connected = false
	w.captured = false
	w.inputReady = false
	w.resetCapturedInputLocked()
	vm, cmdConn = w.vm, w.cmdConn
	w.vm, w.cmdConn = nil, nil
	w.vmISOPath = ""
	w.vmConnecting = false
	w.serverPower = "unavailable"
	w.postCode = ""
	w.mu.Unlock()
	if cmdConn != nil {
		_ = cmdConn.Close()
	}
	if vm != nil {
		_ = vm.Close()
	}
	w.ui(w.updateChrome)
}

func (w *appWindow) connectCommandChannel(host, sessionKey string, rc *ilo.RCInfo) (*kvm.Conn, kvm.Status, error) {
	info := kvm.Info{Host: host, Port: rc.RCPort, SessionKey: sessionKey, ProtocolVersion: rc.ProtocolVersion, Command: kvm.CommandNew, Channel: kvm.ChannelCmd}
	inKey, outKey := kvm.DeriveKeyPair(rc.MasterKey, 1)
	return kvm.DialWithKeys(w.ctx, info, inKey, outKey)
}

func (w *appWindow) openCommandChannel(host, sessionKey string, rc *ilo.RCInfo) (*kvm.Conn, error) {
	cmdConn, cmdStatus, cmdErr := w.connectCommandChannel(host, sessionKey, rc)
	if cmdErr != nil {
		return nil, cmdErr
	}
	w.logf("cmd channel status=%s", cmdStatus)
	if cmdStatus != kvm.StatusSuccess {
		_ = cmdConn.Close()
		return nil, errors.New(cmdStatus.Error())
	}
	return cmdConn, nil
}

func (w *appWindow) startISO(isoPath, host, sessionKey string, rc *ilo.RCInfo) (*vmedia.Session, error) {
	if rc == nil {
		return nil, errors.New("virtual media is unavailable before RcInfo has been loaded")
	}
	if rc.ProtocolVersion <= 1 {
		return nil, fmt.Errorf("virtual media ISO requires iLO remote console protocol v2+, got v%d", rc.ProtocolVersion)
	}
	if rc.VMPort == 0 {
		return nil, errors.New("virtual media ISO requested but iLO did not provide VmPort")
	}
	if strings.ToLower(filepath.Ext(isoPath)) != ".iso" {
		return nil, fmt.Errorf("virtual media only supports .iso files, got %q", isoPath)
	}
	iso, err := vmedia.OpenISO(isoPath)
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	cmdConn := w.cmdConn
	w.mu.Unlock()
	if cmdConn == nil {
		cmdConn, err = w.openCommandChannel(host, sessionKey, rc)
		if err != nil {
			_ = iso.Close()
			return nil, fmt.Errorf("virtual media command channel failed: %w", err)
		}
		w.mu.Lock()
		if w.cmdConn == nil {
			w.cmdConn = cmdConn
			go w.readCommandLoop(cmdConn)
		} else {
			_ = cmdConn.Close()
			cmdConn = w.cmdConn
		}
		w.mu.Unlock()
	}

	info := kvm.Info{Host: host, Port: rc.VMPort, SessionKey: sessionKey, ProtocolVersion: rc.ProtocolVersion, Command: kvm.CommandNew, Channel: kvm.ChannelDisc}
	var conn *kvm.Conn
	var status kvm.Status
	var dialErr error
	for _, pair := range []int{2, 1, 0} {
		inKey, outKey := kvm.DeriveKeyPair(rc.MasterKey, pair)
		conn, status, dialErr = kvm.DialWithKeys(w.ctx, info, inKey, outKey)
		if dialErr == nil && status == kvm.StatusSuccess {
			break
		}
	}
	if dialErr != nil {
		_ = iso.Close()
		return nil, fmt.Errorf("virtual media connect failed: %w", dialErr)
	}
	if status != kvm.StatusSuccess {
		_ = iso.Close()
		return nil, fmt.Errorf("virtual media connect failed: %s", status.Error())
	}
	return vmedia.Start(w.ctx, conn, iso, w.logf), nil
}

func (w *appWindow) readCommandLoop(conn *kvm.Conn) {
	for {
		select {
		case <-w.ctx.Done():
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(connectionPollInterval))
		packet, err := conn.ReadCommandPacket()
		if err == nil {
			w.handleCommandPacket(packet)
			continue
		}
		if isTimeoutError(err) {
			continue
		}
		w.logf("cmd channel read loop stop: %v", err)
		return
	}
}

func isTimeoutError(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

const (
	commandServerPower = 3
	commandPOSTCode    = 5
)

type serverUpdate struct {
	power    *bool
	postCode string
}

func decodeServerUpdate(packet kvm.CommandPacket) (serverUpdate, bool) {
	switch packet.Command {
	case commandServerPower:
		if len(packet.Data) < 1 {
			return serverUpdate{}, false
		}
		on := packet.Data[0] != 0
		return serverUpdate{power: &on}, true
	case commandPOSTCode:
		if len(packet.Data) < 2 {
			return serverUpdate{}, false
		}
		return serverUpdate{postCode: fmt.Sprintf("%02X%02X", packet.Data[1], packet.Data[0])}, true
	default:
		return serverUpdate{}, false
	}
}

func (w *appWindow) handleCommandPacket(packet kvm.CommandPacket) {
	w.logf("cmd packet cmd=%d size=%d seq=%d flags=%d data=%x", packet.Command, packet.Size, packet.Seq, packet.Flags, packet.Data)
	update, ok := decodeServerUpdate(packet)
	if !ok {
		return
	}
	if update.power != nil {
		w.setServerPower(*update.power)
	}
	if update.postCode != "" {
		w.setPostCode(update.postCode)
	}
}

func (w *appWindow) setServerPower(on bool) {
	w.mu.Lock()
	if on {
		w.serverPower = "on"
	} else {
		w.serverPower = "off"
	}
	w.logf("server power updated power=%s", w.serverPower)
	w.mu.Unlock()
	w.ui(w.updateChrome)
}

func (w *appWindow) setPostCode(code string) {
	w.mu.Lock()
	w.postCode = code
	w.logf("server post updated post=%s", w.postCode)
	w.mu.Unlock()
	w.ui(w.updateChrome)
}
