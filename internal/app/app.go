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
	mu             sync.Mutex
	sharePromptMu  sync.Mutex
	status         string
	connecting     bool
	connected      bool
	captured       bool
	inputReady     bool
	closed         bool
	uiBlocked      bool // an open menu or modal suppresses pointer capture
	hwnd           uintptr
	client         *ilo.Client
	conn           *kvm.Conn
	cmdConn        *kvm.Conn
	shareLeader    *kvm.LegacyShareLeader
	vm             *vmedia.Session
	host           string
	sessionKey     string
	rcInfo         *ilo.RCInfo
	vmISOPath      string
	vmConnecting   bool
	sharedSession  bool
	serverPower    string
	postCode       string
	decoder        *kvm.Decoder
	frame          *image.RGBA
	frameDirty     bool
	frameCopy      *image.RGBA
	frameOp        paint.ImageOp
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
		decoder:        kvm.NewDecoder(800, 600),
		pressed:        make(map[Key]bool),
		keyboardMaps:   cfg.KeyboardMaps,
		keyboardMapDir: cfg.KeyboardMapDir,
	}
	w.frame = w.decoder.Framebuffer.Image()
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
		for range w.ticker.C {
			w.updatePointerCapture()
			w.updateKeyboardRepeat()
			w.uploadFrameIfDirty()
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
	if !w.frameDirty || w.frame == nil {
		w.mu.Unlock()
		return
	}
	w.frameDirty = false
	src := w.frame
	if w.frameCopy == nil || !w.frameCopy.Rect.Eq(src.Rect) {
		w.frameCopy = image.NewRGBA(src.Rect)
	}
	copy(w.frameCopy.Pix, src.Pix)
	// A fresh ImageOp handle forces the GPU cache to pick up the new pixels.
	w.frameOp = paint.NewImageOp(w.frameCopy)
	w.mu.Unlock()
	w.invalidate()
}

func (w *appWindow) layout(gtx layout.Context) {
	th := w.th
	size := gtx.Constraints.Max
	menuH := gtx.Dp(32)
	statusH := gtx.Dp(26)

	w.mu.Lock()
	status := statusLine(w.status, w.captured, w.vmISOPath, w.vmConnecting, w.keyboardLayoutName(w.keyboardLayout))
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
	w.menuBar.Layout(menuGtx, th, w.buildMenus())

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
	w.mu.Unlock()

	imgSize := frameOp.Size()
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

func (w *appWindow) buildMenus() []ui.MenuDef {
	w.mu.Lock()
	mountEnabled := w.connected && !w.sharedSession && w.vm == nil && !w.vmConnecting
	unmountEnabled := w.connected && w.vm != nil && !w.vmConnecting
	pasteEnabled := w.connected && w.inputReady
	powerEnabled := w.connected && w.inputReady && !w.sharedSession
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
		{Title: "Keyboard Layout", Items: keyboardItems},
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
	conn := w.conn
	w.keyboardLayout = layout
	w.mu.Unlock()
	w.resetKeyboardState()
	w.logf("keyboard layout changed layout=%s", layout)
	if conn != nil {
		_ = conn.SendAllKeysUp()
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
	w.mu.Lock()
	connected := w.connected
	mounted := w.vm != nil || w.vmConnecting
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
	conn := w.conn
	ready := w.connected && w.inputReady
	layout := w.keyboardLayout
	w.mu.Unlock()
	w.resetKeyboardState()
	if conn == nil || !ready {
		w.setStatus("Connect before pasting clipboard.")
		return
	}
	w.logf("clipboard paste start runes=%d layout=%s", len([]rune(text)), layout)
	w.setStatus("Pasting clipboard...")
	go func() {
		sent, skipped, err := sendClipboardText(w.ctx, conn, w.keyboardMaps, layout, text)
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
	conn := w.conn
	ready := w.connected && w.inputReady
	w.mu.Unlock()
	if conn == nil || !ready {
		w.setStatus("Connect before sending power commands.")
		return
	}
	w.logf("tx power option=%d label=%q", option, label)
	if err := conn.SendPower(option); err != nil {
		w.logf("tx power error option=%d label=%q: %v", option, label, err)
		w.setStatus(fmt.Sprintf("Power command failed: %v", err))
		return
	}
	w.setStatus("Power command sent: " + label)
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
	conn := w.conn
	ready := w.inputReady && w.captured
	rect := w.videoRect
	w.mu.Unlock()
	w.input.Lock()
	lastX, lastY := w.lastMouseX, w.lastMouseY
	buttons := w.mouseButtons
	w.lastMouseX, w.lastMouseY = x, y
	w.input.Unlock()
	if conn == nil || !ready || rect.Dx() <= 0 || rect.Dy() <= 0 {
		return
	}
	vx, vy := x-rect.Min.X, y-rect.Min.Y
	if vx < 0 || vy < 0 || vx >= rect.Dx() || vy >= rect.Dy() {
		w.releaseCapture()
		return
	}
	relX, relY := x-lastX, y-lastY
	if err := conn.SendMouse(vx, vy, relX, relY, rect.Dx(), rect.Dy(), wheel, buttons); err != nil {
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
	conn := w.conn
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
		if conn != nil {
			_ = conn.SendAllKeysUp()
		}
	}
	if leave {
		w.resetCapturedInput()
		w.logf("capture leave pointer")
		if conn != nil {
			_ = conn.SendAllKeysUp()
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
	conn := w.conn
	wasCaptured := w.captured
	w.captured = false
	w.mu.Unlock()
	w.resetCapturedInput()
	if conn != nil && wasCaptured {
		_ = conn.SendAllKeysUp()
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
	w.conn, w.cmdConn, w.shareLeader, w.vm, w.client = nil, nil, nil, nil, nil
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

func statusLine(status string, captured bool, vmISO string, vmConnecting bool, layoutName string) string {
	if vmConnecting {
		status += " | VM: mounting..."
	} else if vmISO != "" {
		status += " | VM: " + filepath.Base(vmISO)
	} else {
		status += " | VM: none"
	}
	status += " | Keyboard: " + layoutName
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
