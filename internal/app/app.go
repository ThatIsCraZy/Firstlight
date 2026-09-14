//go:build windows

package app

import (
	"context"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"firstlight/internal/idrac"
	"firstlight/internal/ilo"
	"firstlight/internal/keyboardmap"
	"firstlight/internal/kvm"
	"firstlight/internal/ui"
	"firstlight/internal/uiicon"
	"firstlight/internal/vmedia"
)

type Config struct {
	Addr                string
	User                string
	Password            string
	Share               bool
	Seize               bool
	VerifyCert          bool
	Debug               bool
	LogPath             string
	ISOPath             string
	KeyboardMaps        *keyboardmap.Registry
	KeyboardMapDir      string
	KeyboardMapWarnings []string
	// TargetLayout names the keyboard layout the remote operating system
	// applies to the key positions this client sends. Empty means en-US,
	// which is what firmware, boot menus and installers use.
	TargetLayout string
}

type SessionWindow struct {
	w *appWindow
}

type keyboardLayout string

const (
	keyboardLayoutDefault     keyboardLayout = ""
	keyboardLayoutForceGerman keyboardLayout = "german"
)

type appWindow struct {
	win     *app.Window
	th      *ui.Theme
	menuBar *ui.MenuBar
	modal   ui.Modal
	ctx     context.Context
	cancel  context.CancelFunc
	cfg     Config
	logger  *log.Logger
	logFile *os.File

	// mu guards connection/session state plus the layout-derived rectangles.
	mu            sync.Mutex
	sharePromptMu sync.Mutex
	status        string
	connecting    bool
	connected     bool
	captured      bool
	inputReady    bool
	closed        bool
	uiBlocked     bool // an open menu or modal suppresses pointer capture
	hwnd          uintptr
	client        *ilo.Client
	conn          *kvm.Conn
	cmdConn       *kvm.Conn
	// remote is set when a Dell controller answered; the iLO fields above stay
	// nil then. sender routes input to whichever backend is live.
	remote        *idrac.Session
	sender        consoleBackend
	shareLeader   *kvm.LegacyShareLeader
	vm            *vmedia.Session
	host          string
	sessionKey    string
	rcInfo        *ilo.RCInfo
	vmISOPath     string
	vmConnecting  bool
	sharedSession bool
	serverPower   string
	postCode      string
	stream        *kvm.VideoStream
	frameReady    bool
	frameDirty    bool
	// Two buffers, used in turn. Gio may still be reading the pixels handed to
	// it on the last tick, and paint.ImageOp requires the image it was built
	// from to stay unchanged, so the next copy goes into the other one.
	frameBuffers [2]*image.RGBA
	frameIndex   int
	// framePainting is the buffer the frame currently on the Gio goroutine is
	// reading, or -1 between frames. The uploader leaves that one alone: two
	// ticks can pass during one slow frame, which is long enough to come back
	// round to the buffer being painted.
	framePainting int
	frameOp       paint.ImageOp
	// remoteTyping is set while the window itself drives the remote keyboard,
	// so local keystrokes do not interleave with the press and release reports
	// a paste sends.
	remoteTyping bool
	// Set when iLO withdraws a permission over the command channel. The
	// controller refuses the action from then on, so the menu stops offering it
	// instead of letting the user send something that is rejected.
	mediaDenied    bool
	powerDenied    bool
	videoRect      image.Rectangle // video area in window client pixels
	canvasRect     image.Rectangle // canvas area in window client pixels
	keyboardLayout keyboardLayout

	// input guards the raw keyboard/mouse tracking shared between the
	// window-procedure thread and the capture ticker.
	input         sync.Mutex
	lastMouseX    int
	lastMouseY    int
	mouseButtons  byte
	pressed       map[Key]bool
	rawPressed    [256]bool
	rawInput      bool
	lastKeyReport [10]byte
	nextBackspace time.Time

	// uiMu guards the deferred UI callbacks executed on the frame goroutine.
	uiMu  sync.Mutex
	uiFns []func()

	keyboardMaps   *keyboardmap.Registry
	targetLayout   *keyboardmap.Target
	keyboardMapDir string
	ticker         *time.Ticker
	wndProc        uintptr
	oldWndProc     uintptr
}

var keyboardMapWarningOnce sync.Once

// Run opens a single console session window and blocks until it is closed.
func Run(ctx context.Context, cfg Config) error {
	done := make(chan struct{})
	_, err := OpenSession(ctx, cfg, func() { close(done) })
	if err != nil {
		return err
	}
	<-done
	return nil
}

func OpenSession(ctx context.Context, cfg Config, onClosed func()) (*SessionWindow, error) {
	if cfg.KeyboardMaps == nil {
		cfg.KeyboardMaps = keyboardmap.BuiltInRegistry()
	}
	logger, logFile, err := setupLogger(cfg)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithCancel(ctx)
	w := &appWindow{
		th:             ui.NewTheme(ui.SystemDark()),
		menuBar:        ui.NewMenuBar(),
		ctx:            cctx,
		cancel:         cancel,
		cfg:            cfg,
		logger:         logger,
		logFile:        logFile,
		status:         "Connecting...",
		serverPower:    "unknown",
		framePainting:  -1,
		stream:         kvm.NewVideoStream(800, 600),
		pressed:        make(map[Key]bool),
		keyboardMaps:   cfg.KeyboardMaps,
		keyboardMapDir: cfg.KeyboardMapDir,
		targetLayout:   keyboardmap.DefaultTarget(),
	}
	if target, ok := keyboardmap.TargetByID(cfg.TargetLayout); ok {
		w.targetLayout = target
	}
	w.logf("app start addr=%q user=%q verify_cert=%v share=%v seize=%v debug=%v iso=%q", cfg.Addr, cfg.User, cfg.VerifyCert, cfg.Share, cfg.Seize, cfg.Debug, cfg.ISOPath)

	w.win = new(app.Window)
	w.win.Option(
		app.Title(sessionTitle(cfg)),
		app.Size(unit.Dp(1024), unit.Dp(768)),
		app.MinSize(unit.Dp(640), unit.Dp(480)),
	)

	if len(cfg.KeyboardMapWarnings) > 0 {
		warnings := append([]string(nil), cfg.KeyboardMapWarnings...)
		keyboardMapWarningOnce.Do(func() {
			w.ui(func() {
				w.modal.Show("Keyboard map warnings", strings.Join(warnings, "\n\n"),
					ui.ModalButton{Label: "OK", Style: ui.ButtonPrimary})
			})
		})
	}

	go w.eventLoop(onClosed)

	w.ticker = time.NewTicker(33 * time.Millisecond)
	go func() {
		// Stopping a ticker does not close its channel, so the loop watches the
		// session context instead and ends with the window.
		for {
			select {
			case <-w.ctx.Done():
				return
			case <-w.ticker.C:
				w.updatePointerCapture()
				w.updateKeyboardRepeat()
				w.uploadFrameIfDirty()
			}
		}
	}()
	w.connectConfigured()
	return &SessionWindow{w: w}, nil
}

func (w *appWindow) eventLoop(onClosed func()) {
	var ops op.Ops
	for {
		switch e := w.win.Event().(type) {
		case app.DestroyEvent:
			w.shutdown()
			if onClosed != nil {
				onClosed()
			}
			return
		case app.Win32ViewEvent:
			w.mu.Lock()
			w.hwnd = e.HWND
			w.mu.Unlock()
			if e.HWND != 0 {
				uiicon.Apply(e.HWND)
				ui.ApplyWindowChrome(e.HWND, w.th.Dark, w.th.BarBg)
				w.installInputSink(e.HWND)
			}
		case app.FrameEvent:
			if dark := ui.SystemDark(); dark != w.th.Dark {
				w.th.SetDark(dark)
				w.mu.Lock()
				hwnd := w.hwnd
				w.mu.Unlock()
				ui.ApplyWindowChrome(hwnd, dark, w.th.BarBg)
			}
			w.drainUI()
			gtx := app.NewContext(&ops, e)
			w.layout(gtx)
			e.Frame(gtx.Ops)
			// The pixels have been handed to the GPU, so the buffer is free.
			w.mu.Lock()
			w.framePainting = -1
			w.mu.Unlock()
		}
	}
}

// ui schedules fn on the frame goroutine.
func (w *appWindow) ui(fn func()) {
	w.uiMu.Lock()
	w.uiFns = append(w.uiFns, fn)
	w.uiMu.Unlock()
	w.invalidate()
}

func (w *appWindow) drainUI() {
	w.uiMu.Lock()
	fns := w.uiFns
	w.uiFns = nil
	w.uiMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func (w *appWindow) invalidate() {
	if w.win != nil {
		w.win.Invalidate()
	}
}

// repaint and updateChrome are kept as call-site-compatible triggers: all
// chrome state is recomputed from scratch on every frame.
func (w *appWindow) repaint()      { w.invalidate() }
func (w *appWindow) updateChrome() { w.invalidate() }

// markFrameDirty flags new decoder output; the ticker uploads it.
func (w *appWindow) markFrameDirty() {
	w.mu.Lock()
	w.frameDirty = true
	w.mu.Unlock()
}

// uploadFrameIfDirty snapshots the decoder framebuffer into a private buffer
// and rebuilds the GPU image op.
func (w *appWindow) uploadFrameIfDirty() {
	w.mu.Lock()
	if !w.frameDirty || !w.frameReady {
		w.mu.Unlock()
		return
	}
	stream, remote := w.stream, w.remote
	next := 1 - w.frameIndex
	if next == w.framePainting {
		// Stay dirty and try again on the next tick.
		w.mu.Unlock()
		return
	}
	w.frameDirty = false
	dst := w.frameBuffers[next]
	w.mu.Unlock()

	// Both backends copy under their own lock, so a frame never mixes pixels
	// from two decoder passes.
	var frameCopy *image.RGBA
	switch {
	case remote != nil:
		frameCopy = remote.Frame(dst)
	case stream != nil:
		frameCopy = stream.CopyFrame(dst)
	default:
		return
	}

	w.mu.Lock()
	w.frameBuffers[next] = frameCopy
	w.frameIndex = next
	// A fresh ImageOp handle forces the GPU cache to pick up the new pixels.
	w.frameOp = paint.NewImageOp(frameCopy)
	w.mu.Unlock()
	w.invalidate()
}

func (w *appWindow) layout(gtx layout.Context) {
	th := w.th
	size := gtx.Constraints.Max
	menuH := gtx.Dp(32)
	statusH := gtx.Dp(26)

	w.mu.Lock()
	status := statusLine(w.status, w.captured, w.vmISOPath, w.vmConnecting, w.keyboardLayoutName(w.keyboardLayout), w.targetLayout)
	serverStatus := serverStatusLine(w.serverPower, w.postCode)
	w.canvasRect = image.Rect(0, menuH, size.X, size.Y-statusH)
	w.mu.Unlock()

	ui.Fill(gtx, size, th.WindowBg)

	// Canvas.
	canvasSize := image.Pt(size.X, size.Y-menuH-statusH)
	if canvasSize.Y > 0 {
		trans := op.Offset(image.Pt(0, menuH)).Push(gtx.Ops)
		w.layoutCanvas(gtx, canvasSize, menuH)
		trans.Pop()
	}

	// Status bar.
	trans := op.Offset(image.Pt(0, size.Y-statusH)).Push(gtx.Ops)
	statusGtx := gtx
	statusGtx.Constraints = layout.Exact(image.Pt(size.X, statusH))
	ui.StatusBar(statusGtx, th, status, serverStatus)
	trans.Pop()

	// Menu bar (its dropdown defers on top of everything).
	menuGtx := gtx
	menuGtx.Constraints = layout.Exact(size)
	w.menuBar.Layout(menuGtx, th, w.buildMenus(), w.buildMenuActions())

	w.modal.Layout(gtx, th)

	w.mu.Lock()
	w.uiBlocked = w.menuBar.Open() || w.modal.Visible()
	w.mu.Unlock()
}

func (w *appWindow) layoutCanvas(gtx layout.Context, size image.Point, offsetY int) {
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	ui.Fill(gtx, size, w.th.CanvasBg)
	w.processCanvasPointer(gtx, offsetY)

	w.mu.Lock()
	frameOp := w.frameOp
	connected := w.connected
	imgSize := frameOp.Size()
	if connected && imgSize.X > 0 && imgSize.Y > 0 {
		// This frame reads that buffer until e.Frame returns.
		w.framePainting = w.frameIndex
	}
	w.mu.Unlock()

	if !connected || imgSize.X <= 0 || imgSize.Y <= 0 {
		w.mu.Lock()
		w.videoRect = image.Rectangle{}
		w.mu.Unlock()
		return
	}
	scale := minf(float64(size.X)/float64(imgSize.X), float64(size.Y)/float64(imgSize.Y))
	dstW, dstH := int(float64(imgSize.X)*scale), int(float64(imgSize.Y)*scale)
	dst := image.Rect((size.X-dstW)/2, (size.Y-dstH)/2, (size.X-dstW)/2+dstW, (size.Y-dstH)/2+dstH)

	w.mu.Lock()
	w.videoRect = dst.Add(image.Pt(0, offsetY))
	w.mu.Unlock()

	defer clip.Rect(dst).Push(gtx.Ops).Pop()
	defer op.Affine(f32.Affine2D{}.
		Scale(f32.Pt(0, 0), f32.Pt(float32(scale), float32(scale))).
		Offset(f32.Pt(float32(dst.Min.X), float32(dst.Min.Y)))).Push(gtx.Ops).Pop()
	frameOp.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// buildMenuActions returns the buttons on the trailing edge of the menu bar.
// Ctrl+Alt+Del earns one because Windows swallows the real chord before any
// application sees it, so there is no keystroke that reaches the remote host.
func (w *appWindow) buildMenuActions() []ui.MenuAction {
	w.mu.Lock()
	// A shared session still types, the same rule the clipboard paste follows.
	enabled := w.connected && w.inputReady
	w.mu.Unlock()
	return []ui.MenuAction{
		{Text: "Ctrl+Alt+Del", Enabled: enabled, Do: w.sendCtrlAltDel},
	}
}

// sendCtrlAltDel sends the chord without the pointer being captured, which is
// the whole point of the button: the click lands on the menu bar, not on the
// video. Both vendors hold the keys down for a moment, so the send runs off
// the layout goroutine rather than freezing the frame for a quarter second.
// beginRemoteTyping claims the remote keyboard, reporting false when a paste or
// a chord already holds it.
func (w *appWindow) beginRemoteTyping() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.remoteTyping {
		return false
	}
	w.remoteTyping = true
	return true
}

func (w *appWindow) endRemoteTyping() {
	w.mu.Lock()
	w.remoteTyping = false
	w.mu.Unlock()
}

func (w *appWindow) sendCtrlAltDel() {
	w.mu.Lock()
	sender := w.sender
	ready := w.connected && w.inputReady
	w.mu.Unlock()
	if sender == nil || !ready {
		return
	}
	if !w.beginRemoteTyping() {
		w.setStatus("The remote keyboard is busy.")
		return
	}
	// The chord ends with every key released. Forget the last report so the
	// next keystroke is transmitted even if it matches what was sent before.
	w.input.Lock()
	w.lastKeyReport = [10]byte{}
	w.input.Unlock()
	go func() {
		defer w.endRemoteTyping()
		w.logf("tx ctrl-alt-del (button)")
		if err := sender.SendCtrlAltDel(); err != nil {
			w.logf("tx ctrl-alt-del error: %v", err)
		}
	}()
}

func (w *appWindow) buildMenus() []ui.MenuDef {
	mounted := w.mediaMounted()
	w.mu.Lock()
	mountEnabled := w.connected && !w.sharedSession && !mounted && !w.vmConnecting && !w.mediaDenied
	unmountEnabled := w.connected && mounted && !w.vmConnecting
	pasteEnabled := w.connected && w.inputReady
	powerEnabled := w.connected && w.inputReady && !w.sharedSession && !w.powerDenied
	sessionMenuEnabled := w.connected && w.remote != nil
	layout := w.keyboardLayout
	w.mu.Unlock()

	keyboardItems := []ui.MenuItem{
		{Text: "Default", Checked: layout == keyboardLayoutDefault, Enabled: true, Do: func() { w.setKeyboardLayout(keyboardLayoutDefault) }},
	}
	for _, info := range w.keyboardMaps.Selectable() {
		id := info.ID
		keyboardItems = append(keyboardItems, ui.MenuItem{
			Text:    info.DisplayName,
			Checked: layout == keyboardLayout(id),
			Enabled: true,
			Do:      func() { w.setKeyboardLayout(keyboardLayout(id)) },
		})
	}
	keyboardItems = append(keyboardItems,
		ui.MenuItem{Separator: true},
		ui.MenuItem{Text: "Export built-in German map...", Enabled: true, Do: func() { go w.exportBuiltInGermanMap() }},
	)

	w.mu.Lock()
	currentTarget := w.targetLayout
	w.mu.Unlock()
	var targetItems []ui.MenuItem
	for _, entry := range keyboardmap.Targets() {
		target := entry
		targetItems = append(targetItems, ui.MenuItem{
			Text:    target.DisplayName,
			Checked: currentTarget == target,
			Enabled: true,
			Do:      func() { w.setTargetLayout(target) },
		})
	}

	return []ui.MenuDef{
		{Title: "Edit", Items: []ui.MenuItem{
			{Text: "Paste Clipboard", Enabled: pasteEnabled, Do: w.pasteClipboard},
		}},
		{Title: "Virtual Media", Items: []ui.MenuItem{
			{Text: "Mount ISO...", Enabled: mountEnabled, Do: func() { go w.chooseAndMountISO() }},
			{Text: "Dismount ISO", Enabled: unmountEnabled, Do: w.dismountISO},
		}},
		{Title: "Power", Items: []ui.MenuItem{
			{Text: "Momentary Press", Enabled: powerEnabled, Do: func() { w.sendPower(kvm.PowerMomentaryPress, "Momentary Press") }},
			{Text: "Press and Hold", Enabled: powerEnabled, Do: func() { w.confirmAndSendPower(kvm.PowerPressAndHold, "Press and Hold") }},
			{Text: "Cold Boot", Enabled: powerEnabled, Do: func() { w.confirmAndSendPower(kvm.PowerColdBoot, "Cold Boot") }},
			{Text: "Reset", Enabled: powerEnabled, Do: func() { w.confirmAndSendPower(kvm.PowerReset, "Reset") }},
		}},
		{Title: "Session", Items: []ui.MenuItem{
			{Text: "Show Other Sessions", Enabled: sessionMenuEnabled, Do: w.listOtherSessions},
			{Separator: true},
			{Text: "Take Over Console...", Enabled: sessionMenuEnabled, Do: w.confirmTakeOverConsole},
		}},
		{Title: "Keyboard Layout", Items: keyboardItems},
		{Title: "Remote Layout", Items: targetItems},
	}
}

func (s *SessionWindow) Focus() {
	if s == nil || s.w == nil {
		return
	}
	s.w.mu.Lock()
	hwnd := s.w.hwnd
	s.w.mu.Unlock()
	focusWindow(hwnd)
}

func (s *SessionWindow) Close() {
	if s == nil || s.w == nil {
		return
	}
	s.w.shutdown()
	if s.w.win != nil {
		s.w.win.Perform(closeAction())
	}
}

func sessionTitle(cfg Config) string {
	addr := strings.TrimSpace(cfg.Addr)
	user := strings.TrimSpace(cfg.User)
	if addr == "" {
		return "Firstlight"
	}
	if user == "" {
		return "Firstlight - " + addr
	}
	return "Firstlight - " + addr + " (" + user + ")"
}

func setupLogger(cfg Config) (*log.Logger, *os.File, error) {
	if !cfg.Debug && cfg.LogPath == "" {
		return nil, nil, nil
	}
	path := cfg.LogPath
	if path == "" {
		path = "Firstlight-debug.log"
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file %q: %w", path, err)
	}
	logger := log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	logger.Printf("logging started path=%q", path)
	return logger, f, nil
}

func (w *appWindow) logf(format string, args ...any) {
	if w.logger != nil {
		w.logger.Printf(format, args...)
	}
}

func (w *appWindow) keyboardLayoutName(layout keyboardLayout) string {
	if layout == keyboardLayoutDefault {
		return "Default"
	}
	if info, ok := w.keyboardMaps.Info(string(layout)); ok {
		return info.DisplayName
	}
	return string(layout)
}

func (w *appWindow) setKeyboardLayout(layout keyboardLayout) {
	if layout != keyboardLayoutDefault {
		if _, ok := w.keyboardMaps.Info(string(layout)); !ok {
			w.setStatus(fmt.Sprintf("Keyboard map %q is unavailable.", layout))
			return
		}
	}
	w.mu.Lock()
	if w.keyboardLayout == layout {
		w.mu.Unlock()
		w.invalidate()
		return
	}
	sender := w.sender
	w.keyboardLayout = layout
	w.mu.Unlock()
	w.resetKeyboardState()
	w.logf("keyboard layout changed layout=%s", layout)
	if sender != nil {
		_ = sender.SendAllKeysUp()
	}
	w.invalidate()
}

// setTargetLayout changes the layout the remote operating system is assumed to
// run. Everything in flight is released first, because the same physical key
// lands on a different position after the switch.
func (w *appWindow) setTargetLayout(target *keyboardmap.Target) {
	if target == nil {
		return
	}
	w.mu.Lock()
	if w.targetLayout == target {
		w.mu.Unlock()
		w.invalidate()
		return
	}
	sender := w.sender
	w.targetLayout = target
	w.mu.Unlock()
	w.resetKeyboardState()
	w.logf("remote layout changed target=%s", target.ID)
	if sender != nil {
		_ = sender.SendAllKeysUp()
	}
	if !target.IsDefault() {
		w.setStatus("Remote layout: " + target.DisplayName + ".")
	}
	w.invalidate()
}

func (w *appWindow) setStatus(status string) {
	w.mu.Lock()
	w.status = status
	w.mu.Unlock()
	w.invalidate()
}

func (w *appWindow) chooseAndMountISO() {
	attached := w.mediaMounted()
	w.mu.Lock()
	connected := w.connected
	mounted := attached || w.vmConnecting
	current := w.vmISOPath
	w.mu.Unlock()
	if !connected {
		w.setStatus("Connect before mounting virtual media.")
		return
	}
	if mounted {
		w.setStatus("Virtual media is already mounted. Dismount first.")
		return
	}
	path, ok, err := chooseISOFile(current)
	if err != nil {
		w.setStatus(fmt.Sprintf("ISO selection failed: %v", err))
		return
	}
	if ok {
		w.mountISO(path)
	}
}

func (w *appWindow) exportBuiltInGermanMap() {
	initial := "german-template.json"
	if w.keyboardMapDir != "" {
		initial = filepath.Join(w.keyboardMapDir, initial)
	}
	selected, ok, err := chooseKeyboardMapExport(initial)
	if err != nil {
		w.setStatus(fmt.Sprintf("Keyboard map export failed: %v", err))
		return
	}
	if !ok {
		return
	}
	jsonPath, markdownPath := keyboardmap.ExportPaths(selected)
	if fileExists(jsonPath) || fileExists(markdownPath) {
		message := fmt.Sprintf("Replace both exported files?\n\n%s\n%s", jsonPath, markdownPath)
		w.ui(func() {
			w.modal.Show("Export German keyboard map", message,
				ui.ModalButton{Label: "Cancel", Style: ui.ButtonRegular},
				ui.ModalButton{Label: "Replace", Style: ui.ButtonPrimary, Action: func() { go w.performKeyboardMapExport(jsonPath) }},
			)
		})
		return
	}
	w.performKeyboardMapExport(jsonPath)
}

func (w *appWindow) performKeyboardMapExport(jsonPath string) {
	jsonPath, markdownPath, err := keyboardmap.ExportBuiltInGerman(jsonPath)
	if err != nil {
		w.logf("keyboard map export failed json=%q markdown=%q: %v", jsonPath, markdownPath, err)
		w.setStatus(fmt.Sprintf("Keyboard map export failed: %v", err))
		return
	}
	w.logf("keyboard map exported json=%q markdown=%q", jsonPath, markdownPath)
	w.setStatus("German keyboard map and LLM guide exported.")
	w.ui(func() {
		w.modal.Show("Export complete", fmt.Sprintf("Created:\n\n%s\n%s", jsonPath, markdownPath),
			ui.ModalButton{Label: "OK", Style: ui.ButtonPrimary})
	})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (w *appWindow) mountISO(path string) {
	if w.isRemote() {
		w.mountISORemote(path)
		return
	}
	w.mu.Lock()
	if w.vm != nil || w.vmConnecting {
		w.mu.Unlock()
		w.setStatus("Virtual media is already mounted. Dismount first.")
		return
	}
	host, sessionKey, rc := w.host, w.sessionKey, w.rcInfo
	w.vmConnecting = true
	w.status = fmt.Sprintf("Mounting ISO: %s", filepath.Base(path))
	w.mu.Unlock()
	w.invalidate()
	go func() {
		vm, err := w.startISO(path, host, sessionKey, rc)
		w.mu.Lock()
		w.vmConnecting = false
		if w.closed {
			w.mu.Unlock()
			if vm != nil {
				_ = vm.Close()
			}
			return
		}
		if err != nil {
			w.status = fmt.Sprintf("ISO mount failed: %v", err)
			w.logf("iso mount failed path=%q: %v", path, err)
			w.mu.Unlock()
			w.invalidate()
			return
		}
		w.vm = vm
		w.vmISOPath = path
		w.status = fmt.Sprintf("ISO mounted: %s", filepath.Base(path))
		w.mu.Unlock()
		w.invalidate()
		go func() {
			<-vm.Done()
			w.mu.Lock()
			if w.vm == vm {
				w.vm = nil
				w.vmISOPath = ""
				if !w.closed {
					w.status = "Virtual media disconnected."
				}
			}
			w.mu.Unlock()
			w.invalidate()
		}()
	}()
}

func (w *appWindow) dismountISO() {
	if w.isRemote() {
		w.dismountISORemote()
		return
	}
	w.mu.Lock()
	if w.vmConnecting {
		w.mu.Unlock()
		w.setStatus("ISO mount is still in progress.")
		return
	}
	vm := w.vm
	path := w.vmISOPath
	w.vm, w.vmISOPath = nil, ""
	w.vmConnecting = false
	w.mu.Unlock()
	if vm == nil {
		w.setStatus("No ISO is mounted.")
		return
	}
	_ = vm.Close()
	w.logf("iso dismounted path=%q", path)
	w.setStatus("ISO dismounted.")
}

func (w *appWindow) pasteClipboard() {
	text, err := readClipboardText()
	if err != nil {
		w.setStatus(fmt.Sprintf("Clipboard paste failed: %v", err))
		return
	}
	if text == "" {
		w.setStatus("Clipboard is empty.")
		return
	}
	w.mu.Lock()
	sender := w.sender
	ready := w.connected && w.inputReady
	layout := w.keyboardLayout
	target := w.targetLayout
	w.mu.Unlock()
	w.resetKeyboardState()
	if sender == nil || !ready {
		w.setStatus("Connect before pasting clipboard.")
		return
	}
	if !w.beginRemoteTyping() {
		w.setStatus("The remote keyboard is busy.")
		return
	}
	w.logf("clipboard paste start runes=%d layout=%s", len([]rune(text)), layout)
	w.setStatus("Pasting clipboard...")
	go func() {
		defer w.endRemoteTyping()
		sent, skipped, err := sendClipboardText(w.ctx, sender, w.keyboardMaps, layout, target, text)
		if err != nil {
			w.logf("clipboard paste failed sent=%d skipped=%d: %v", sent, skipped, err)
			w.setStatus(fmt.Sprintf("Clipboard paste failed: %v", err))
			return
		}
		w.logf("clipboard paste done sent=%d skipped=%d layout=%s", sent, skipped, layout)
		if skipped > 0 {
			w.setStatus(fmt.Sprintf("Clipboard pasted: %d chars, %d unsupported skipped.", sent, skipped))
			return
		}
		w.setStatus(fmt.Sprintf("Clipboard pasted: %d chars.", sent))
	}()
}

func (w *appWindow) confirmAndSendPower(option kvm.PowerOption, label string) {
	w.ui(func() {
		w.modal.Show("Firstlight Power", fmt.Sprintf("Send power command %q to the server?", label),
			ui.ModalButton{Label: "Cancel", Style: ui.ButtonRegular, Action: func() {
				w.logf("power command cancelled option=%d label=%q", option, label)
			}},
			ui.ModalButton{Label: label, Style: ui.ButtonDestructive, Action: func() {
				w.sendPower(option, label)
			}},
		)
	})
}

func (w *appWindow) sendPower(option kvm.PowerOption, label string) {
	w.mu.Lock()
	sender := w.sender
	ready := w.connected && w.inputReady
	w.mu.Unlock()
	if sender == nil || !ready {
		w.setStatus("Connect before sending power commands.")
		return
	}
	w.logf("tx power option=%d label=%q", option, label)
	w.setStatus("Sending power command: " + label)
	// Menu and dialog actions run on the Gio goroutine. A Dell power command
	// goes out over Redfish with a twenty-second timeout, so sending it here
	// would stop the window redrawing for that long.
	go func() {
		if err := sender.SendPower(option); err != nil {
			w.logf("tx power error option=%d label=%q: %v", option, label, err)
			w.setStatus(fmt.Sprintf("Power command failed: %v", err))
			return
		}
		w.setStatus("Power command sent: " + label)
	}()
}

// canvasTag identifies the canvas as a pointer-event target.
type canvasTag struct{}

// processCanvasPointer forwards Gio pointer events over the canvas to the
// remote console. Gio enables WM_POINTER input, so the legacy WM_MOUSEMOVE /
// WM_*BUTTON* messages never arrive at the window procedure — the mouse has to
// come from Gio's own event stream. offsetY converts canvas-local coordinates
// back to window client coordinates.
func (w *appWindow) processCanvasPointer(gtx layout.Context, offsetY int) {
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, canvasTag{})
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target:  canvasTag{},
			Kinds:   pointer.Press | pointer.Release | pointer.Move | pointer.Drag | pointer.Scroll | pointer.Leave,
			ScrollY: pointer.ScrollRange{Min: -1, Max: 1},
		})
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		if pe.Kind == pointer.Leave {
			w.releaseCapture()
			continue
		}
		x, y := int(pe.Position.X), int(pe.Position.Y)+offsetY
		wheel := int8(0)
		if pe.Kind == pointer.Scroll {
			// The remote console expects notches, inverted against Gio's
			// downward-positive scroll axis.
			switch {
			case pe.Scroll.Y > 0:
				wheel = -1
			case pe.Scroll.Y < 0:
				wheel = 1
			}
		}
		w.mouseEvent(x, y, remoteButtons(pe.Buttons), wheel)
	}
}

func remoteButtons(b pointer.Buttons) byte {
	var out byte
	if b&pointer.ButtonPrimary != 0 {
		out |= 1
	}
	if b&pointer.ButtonSecondary != 0 {
		out |= 2
	}
	if b&pointer.ButtonTertiary != 0 {
		out |= 4
	}
	return out
}

// mouseEvent forwards one pointer sample; x/y are window client coordinates.
func (w *appWindow) mouseEvent(x, y int, buttons byte, wheel int8) {
	w.input.Lock()
	w.mouseButtons = buttons
	w.input.Unlock()
	w.updateCaptureForPoint(x, y)
	w.sendMouse(x, y, wheel)
}

func (w *appWindow) sendMouse(x, y int, wheel int8) {
	w.mu.Lock()
	sender := w.sender
	ready := w.inputReady && w.captured
	rect := w.videoRect
	w.mu.Unlock()
	w.input.Lock()
	lastX, lastY := w.lastMouseX, w.lastMouseY
	buttons := w.mouseButtons
	w.lastMouseX, w.lastMouseY = x, y
	w.input.Unlock()
	if sender == nil || !ready || rect.Dx() <= 0 || rect.Dy() <= 0 {
		return
	}
	vx, vy := x-rect.Min.X, y-rect.Min.Y
	if vx < 0 || vy < 0 || vx >= rect.Dx() || vy >= rect.Dy() {
		w.releaseCapture()
		return
	}
	relX, relY := x-lastX, y-lastY
	if err := sender.SendMouse(vx, vy, relX, relY, rect.Dx(), rect.Dy(), wheel, buttons); err != nil {
		w.logf("tx mouse error: %v", err)
	}
}

func (w *appWindow) updateCaptureForPoint(x, y int) {
	w.mu.Lock()
	rect := w.videoRect
	ready := w.connected && w.inputReady && !w.uiBlocked
	captured := w.captured
	inside := ready && rect.Dx() > 0 && rect.Dy() > 0 && image.Pt(x, y).In(rect)
	w.mu.Unlock()
	if inside && !captured {
		w.setCapture(true)
		w.input.Lock()
		w.lastMouseX, w.lastMouseY = x, y
		w.input.Unlock()
		w.logf("capture enter x=%d y=%d", x, y)
		return
	}
	if !inside && captured {
		w.logf("capture leave x=%d y=%d", x, y)
		w.releaseCapture()
	}
}

func (w *appWindow) updatePointerCapture() {
	inside, x, y := w.pointerInsideCanvas()

	w.mu.Lock()
	ready := w.connected && w.inputReady && !w.uiBlocked
	captured := w.captured
	sender := w.sender
	shouldCapture := ready && inside
	changed := false
	enter := false
	leave := false

	if shouldCapture && !captured {
		w.captured = true
		changed = true
		enter = true
	} else if !shouldCapture && captured {
		w.captured = false
		changed = true
		leave = true
	}
	w.mu.Unlock()

	if enter {
		w.input.Lock()
		w.lastMouseX, w.lastMouseY = x, y
		w.lastKeyReport = kvm.KeyboardReport(0)
		w.rawPressed = [256]bool{}
		w.input.Unlock()
		w.logf("capture enter pointer x=%d y=%d", x, y)
		if sender != nil {
			_ = sender.SendAllKeysUp()
		}
	}
	if leave {
		w.resetCapturedInput()
		w.logf("capture leave pointer")
		if sender != nil {
			_ = sender.SendAllKeysUp()
		}
	}
	if changed {
		w.invalidate()
	}
}

func (w *appWindow) setCapture(v bool) {
	w.mu.Lock()
	if w.captured == v {
		w.mu.Unlock()
		return
	}
	w.captured = v
	w.mu.Unlock()
	w.invalidate()
}

func (w *appWindow) releaseCapture() {
	w.mu.Lock()
	sender := w.sender
	wasCaptured := w.captured
	w.captured = false
	w.mu.Unlock()
	w.resetCapturedInput()
	if sender != nil && wasCaptured {
		_ = sender.SendAllKeysUp()
	}
	w.invalidate()
}

func (w *appWindow) shutdown() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	w.logf("shutdown requested")
	conn, cmdConn, shareLeader, vm, client := w.conn, w.cmdConn, w.shareLeader, w.vm, w.client
	remote := w.remote
	w.conn, w.cmdConn, w.shareLeader, w.vm, w.client = nil, nil, nil, nil, nil
	w.remote, w.sender = nil, nil
	w.connected, w.captured, w.inputReady = false, false, false
	w.sharedSession = false
	w.mu.Unlock()
	w.resetCapturedInput()
	if w.ticker != nil {
		w.ticker.Stop()
	}
	w.cancel()
	if vm != nil {
		_ = vm.Close()
	}
	if shareLeader != nil {
		_ = shareLeader.Close()
	}
	if conn != nil {
		_ = conn.Close()
	}
	if cmdConn != nil {
		_ = cmdConn.Close()
	}
	if remote != nil {
		// Closing the Dell session unmaps any image, tells the firmware the
		// viewer is leaving and ends the web login.
		_ = remote.Close()
	}
	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Logout(ctx)
	}
	if w.logFile != nil {
		_ = w.logFile.Close()
		w.logFile = nil
	}
}

func statusLine(status string, captured bool, vmISO string, vmConnecting bool, layoutName string, target *keyboardmap.Target) string {
	if vmConnecting {
		status += " | VM: mounting..."
	} else if vmISO != "" {
		status += " | VM: " + filepath.Base(vmISO)
	} else {
		status += " | VM: none"
	}
	status += " | Keyboard: " + layoutName
	if target != nil && !target.IsDefault() {
		status += " to " + target.ID
	}
	if captured {
		return status + " [mouse inside]"
	}
	return status + " [mouse outside]"
}

func serverStatusLine(power, postCode string) string {
	if power == "" {
		power = "unknown"
	}
	if postCode == "" {
		postCode = "--"
	}
	status := "Power: " + power
	return status + " | POST: " + postCode
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
