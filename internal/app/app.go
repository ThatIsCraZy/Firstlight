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

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"firstlight/internal/ilo"
	"firstlight/internal/keyboardmap"
	"firstlight/internal/kvm"
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
	*walk.MainWindow
	ctx     context.Context
	cancel  context.CancelFunc
	cfg     Config
	logger  *log.Logger
	logFile *os.File

	mu            sync.Mutex
	sharePromptMu sync.Mutex
	status        string
	connecting    bool
	connected     bool
	captured      bool
	inputReady    bool
	closed        bool
	client        *ilo.Client
	conn          *kvm.Conn
	cmdConn       *kvm.Conn
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
	decoder       *kvm.Decoder
	frame         image.Image

	canvas            *walk.CustomWidget
	statusBar         *walk.StatusBarItem
	serverStatusBar   *walk.StatusBarItem
	pasteAct          *walk.Action
	mountAct          *walk.Action
	unmountAct        *walk.Action
	powerMomentaryAct *walk.Action
	powerHoldAct      *walk.Action
	powerColdBootAct  *walk.Action
	powerResetAct     *walk.Action
	kbdDefaultAct     *walk.Action
	keyboardMapActs   map[string]*walk.Action
	ticker            *time.Ticker
	mainWndProc       uintptr
	oldMainWndProc    uintptr
	canvasWndProc     uintptr
	oldCanvasWndProc  uintptr

	videoRect      walk.Rectangle
	lastMouseX     int
	lastMouseY     int
	mouseButtons   byte
	pressed        map[walk.Key]bool
	rawPressed     [256]bool
	rawInput       bool
	lastKeyReport  [10]byte
	keyboardLayout keyboardLayout
	keyboardMaps   *keyboardmap.Registry
	keyboardMapDir string
	nextBackspace  time.Time
}

var keyboardMapWarningOnce sync.Once

func Run(ctx context.Context, cfg Config) error {
	session, err := OpenSession(ctx, cfg, nil)
	if err != nil {
		return err
	}
	code := session.w.Run()
	session.w.shutdown()
	if code != 0 {
		return fmt.Errorf("window closed with code %d", code)
	}
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
		ctx:             cctx,
		cancel:          cancel,
		cfg:             cfg,
		logger:          logger,
		logFile:         logFile,
		status:          "Connecting...",
		serverPower:     "unknown",
		decoder:         kvm.NewDecoder(800, 600),
		pressed:         make(map[walk.Key]bool),
		keyboardMaps:    cfg.KeyboardMaps,
		keyboardMapDir:  cfg.KeyboardMapDir,
		keyboardMapActs: make(map[string]*walk.Action),
	}
	w.frame = w.decoder.Framebuffer.Image()
	w.logf("app start addr=%q user=%q verify_cert=%v share=%v seize=%v debug=%v iso=%q", cfg.Addr, cfg.User, cfg.VerifyCert, cfg.Share, cfg.Seize, cfg.Debug, cfg.ISOPath)

	keyboardItems := []MenuItem{
		Action{AssignTo: &w.kbdDefaultAct, Text: "Default", Checkable: true, Checked: true, OnTriggered: func() { w.setKeyboardLayout(keyboardLayoutDefault) }},
	}
	type actionBinding struct {
		id     string
		action *walk.Action
	}
	bindings := make([]*actionBinding, 0, len(w.keyboardMaps.Selectable()))
	for _, info := range w.keyboardMaps.Selectable() {
		binding := &actionBinding{id: info.ID}
		id := info.ID
		keyboardItems = append(keyboardItems, Action{
			AssignTo:    &binding.action,
			Text:        info.DisplayName,
			Checkable:   true,
			OnTriggered: func() { w.setKeyboardLayout(keyboardLayout(id)) },
		})
		bindings = append(bindings, binding)
	}
	keyboardItems = append(keyboardItems,
		Separator{},
		Action{Text: "Export built-in German map...", OnTriggered: w.exportBuiltInGermanMap},
	)

	err = MainWindow{
		AssignTo: &w.MainWindow,
		Title:    sessionTitle(cfg),
		MinSize:  Size{Width: 640, Height: 480},
		Size:     Size{Width: 1024, Height: 768},
		Layout:   VBox{MarginsZero: true},
		MenuItems: []MenuItem{
			Menu{
				Text: "&Edit",
				Items: []MenuItem{
					Action{AssignTo: &w.pasteAct, Text: "Paste Clipboard", OnTriggered: w.pasteClipboard},
				},
			},
			Menu{
				Text: "&Virtual Media",
				Items: []MenuItem{
					Action{AssignTo: &w.mountAct, Text: "Mount ISO...", OnTriggered: w.chooseAndMountISO},
					Action{AssignTo: &w.unmountAct, Text: "Dismount ISO", OnTriggered: w.dismountISO},
				},
			},
			Menu{
				Text: "&Power",
				Items: []MenuItem{
					Action{AssignTo: &w.powerMomentaryAct, Text: "Momentary Press", OnTriggered: func() { w.sendPower(kvm.PowerMomentaryPress, "Momentary Press") }},
					Action{AssignTo: &w.powerHoldAct, Text: "Press and Hold", OnTriggered: func() { w.confirmAndSendPower(kvm.PowerPressAndHold, "Press and Hold") }},
					Action{AssignTo: &w.powerColdBootAct, Text: "Cold Boot", OnTriggered: func() { w.confirmAndSendPower(kvm.PowerColdBoot, "Cold Boot") }},
					Action{AssignTo: &w.powerResetAct, Text: "Reset", OnTriggered: func() { w.confirmAndSendPower(kvm.PowerReset, "Reset") }},
				},
			},
			Menu{
				Text:  "&Keyboard Layout",
				Items: keyboardItems,
			},
		},
		Children: []Widget{
			CustomWidget{
				AssignTo:            &w.canvas,
				PaintMode:           PaintBuffered,
				InvalidatesOnResize: true,
				PaintPixels:         w.paint,
			},
		},
		StatusBarItems: []StatusBarItem{
			{AssignTo: &w.statusBar, Text: w.status},
			{AssignTo: &w.serverStatusBar, Text: "Power: unknown"},
		},
		OnBoundsChanged: w.repaint,
	}.Create()
	if err != nil {
		cancel()
		if logFile != nil {
			_ = logFile.Close()
		}
		return nil, err
	}
	for _, binding := range bindings {
		w.keyboardMapActs[binding.id] = binding.action
	}
	if icon, err := uiicon.Load(); err == nil {
		_ = w.SetIcon(icon)
		w.AddDisposable(icon)
	} else {
		w.logf("load app icon failed: %v", err)
	}
	w.installInputSink()
	w.canvas.MouseMove().Attach(w.mouseMove)
	w.canvas.MouseDown().Attach(w.mouseDown)
	w.canvas.MouseUp().Attach(w.mouseUp)
	w.canvas.MouseWheel().Attach(w.mouseWheel)
	w.Deactivating().Attach(func() { w.releaseCapture() })
	w.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		w.shutdown()
		if onClosed != nil {
			onClosed()
		}
	})
	w.updateChrome()
	if len(cfg.KeyboardMapWarnings) > 0 {
		warnings := append([]string(nil), cfg.KeyboardMapWarnings...)
		keyboardMapWarningOnce.Do(func() {
			w.ui(func() {
				walk.MsgBox(w.MainWindow, "Keyboard map warnings", strings.Join(warnings, "\n\n"), walk.MsgBoxOK|walk.MsgBoxIconWarning)
			})
		})
	}
	w.ticker = time.NewTicker(33 * time.Millisecond)
	go func() {
		for range w.ticker.C {
			w.ui(func() {
				w.updatePointerCapture()
				w.updateKeyboardRepeat()
				w.repaint()
			})
		}
	}()
	w.connectConfigured()
	return &SessionWindow{w: w}, nil
}

func (s *SessionWindow) Focus() {
	if s == nil || s.w == nil || s.w.MainWindow == nil {
		return
	}
	s.w.Show()
	_ = s.w.BringToTop()
	_ = s.w.Activate()
	win.SetForegroundWindow(s.w.Handle())
}

func (s *SessionWindow) Close() {
	if s == nil || s.w == nil {
		return
	}
	s.w.shutdown()
	if s.w.MainWindow != nil && s.w.Handle() != 0 {
		_ = s.w.Close()
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

func (w *appWindow) ui(fn func()) {
	if w.MainWindow != nil {
		w.Synchronize(fn)
	}
}

func (w *appWindow) repaint() {
	if w.canvas != nil {
		_ = w.canvas.Invalidate()
	}
}

func (w *appWindow) paint(canvas *walk.Canvas, bounds walk.Rectangle) error {
	brush, _ := walk.NewSolidColorBrush(walk.RGB(18, 18, 18))
	if brush != nil {
		defer brush.Dispose()
		_ = canvas.FillRectanglePixels(brush, bounds)
	}
	w.mu.Lock()
	img := w.frame
	connected := w.connected
	w.mu.Unlock()
	if !connected || img == nil {
		return nil
	}
	iw, ih := img.Bounds().Dx(), img.Bounds().Dy()
	if iw <= 0 || ih <= 0 || bounds.Width <= 0 || bounds.Height <= 0 {
		return nil
	}
	scale := min(float64(bounds.Width)/float64(iw), float64(bounds.Height)/float64(ih))
	dstW, dstH := int(float64(iw)*scale), int(float64(ih)*scale)
	dst := walk.Rectangle{X: bounds.X + (bounds.Width-dstW)/2, Y: bounds.Y + (bounds.Height-dstH)/2, Width: dstW, Height: dstH}
	w.videoRect = dst
	bmp, err := walk.NewBitmapFromImage(img)
	if err != nil {
		return err
	}
	defer bmp.Dispose()
	return canvas.DrawImageStretchedPixels(bmp, dst)
}

func (w *appWindow) updateChrome() {
	w.mu.Lock()
	layoutName := w.keyboardLayoutName(w.keyboardLayout)
	status := statusLine(w.status, w.captured, w.vmISOPath, w.vmConnecting, layoutName)
	serverStatus := serverStatusLine(w.serverPower, w.postCode)
	mountEnabled := w.connected && !w.sharedSession && w.vm == nil && !w.vmConnecting
	unmountEnabled := w.connected && w.vm != nil && !w.vmConnecting
	pasteEnabled := w.connected && w.inputReady
	powerEnabled := w.connected && w.inputReady && !w.sharedSession
	layout := w.keyboardLayout
	w.mu.Unlock()
	if w.statusBar != nil {
		if w.MainWindow != nil {
			rightWidth := 230
			leftWidth := max(w.ClientBounds().Width-rightWidth, 120)
			_ = w.statusBar.SetWidth(leftWidth)
			if w.serverStatusBar != nil {
				_ = w.serverStatusBar.SetWidth(rightWidth)
			}
		}
		_ = w.statusBar.SetText(status)
	}
	if w.serverStatusBar != nil {
		_ = w.serverStatusBar.SetText(serverStatus)
	}
	if w.mountAct != nil {
		_ = w.mountAct.SetEnabled(mountEnabled)
	}
	if w.unmountAct != nil {
		_ = w.unmountAct.SetEnabled(unmountEnabled)
	}
	if w.pasteAct != nil {
		_ = w.pasteAct.SetEnabled(pasteEnabled)
	}
	if w.powerMomentaryAct != nil {
		_ = w.powerMomentaryAct.SetEnabled(powerEnabled)
	}
	if w.powerHoldAct != nil {
		_ = w.powerHoldAct.SetEnabled(powerEnabled)
	}
	if w.powerColdBootAct != nil {
		_ = w.powerColdBootAct.SetEnabled(powerEnabled)
	}
	if w.powerResetAct != nil {
		_ = w.powerResetAct.SetEnabled(powerEnabled)
	}
	if w.kbdDefaultAct != nil {
		_ = w.kbdDefaultAct.SetChecked(layout == keyboardLayoutDefault)
	}
	for id, action := range w.keyboardMapActs {
		if action != nil {
			_ = action.SetChecked(layout == keyboardLayout(id))
		}
	}
	w.repaint()
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
		w.updateChrome()
		return
	}
	conn := w.conn
	w.keyboardLayout = layout
	w.resetKeyboardStateLocked()
	w.mu.Unlock()
	w.logf("keyboard layout changed layout=%s", layout)
	if conn != nil {
		_ = conn.SendAllKeysUp()
	}
	w.updateChrome()
}

func (w *appWindow) setStatus(status string) {
	w.mu.Lock()
	w.status = status
	w.mu.Unlock()
	w.ui(w.updateChrome)
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
		if walk.MsgBox(w.MainWindow, "Export German keyboard map", message, walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2) != win.IDYES {
			return
		}
	}
	jsonPath, markdownPath, err = keyboardmap.ExportBuiltInGerman(jsonPath)
	if err != nil {
		w.logf("keyboard map export failed json=%q markdown=%q: %v", jsonPath, markdownPath, err)
		w.setStatus(fmt.Sprintf("Keyboard map export failed: %v", err))
		return
	}
	w.logf("keyboard map exported json=%q markdown=%q", jsonPath, markdownPath)
	w.setStatus("German keyboard map and LLM guide exported.")
	walk.MsgBox(w.MainWindow, "Export complete", fmt.Sprintf("Created:\n\n%s\n%s", jsonPath, markdownPath), walk.MsgBoxOK|walk.MsgBoxIconInformation)
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
	w.updateChrome()
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
			w.ui(w.updateChrome)
			return
		}
		w.vm = vm
		w.vmISOPath = path
		w.status = fmt.Sprintf("ISO mounted: %s", filepath.Base(path))
		w.mu.Unlock()
		w.ui(w.updateChrome)
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
			w.ui(w.updateChrome)
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
	w.resetKeyboardStateLocked()
	w.mu.Unlock()
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
	result := walk.MsgBox(
		w.MainWindow,
		"Firstlight Power",
		fmt.Sprintf("Send power command %q to the server?", label),
		walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2,
	)
	if result != win.IDYES {
		w.logf("power command cancelled option=%d label=%q", option, label)
		return
	}
	w.sendPower(option, label)
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

func (w *appWindow) mouseDown(x, y int, button walk.MouseButton) {
	w.logf("mouse down x=%d y=%d button=%d", x, y, button)
	_ = w.canvas.SetFocus()
	w.mouseMove(x, y, button)
}

func (w *appWindow) mouseUp(x, y int, button walk.MouseButton) {
	switch button {
	case walk.LeftButton:
		w.mouseButtons &^= 1
	case walk.RightButton:
		w.mouseButtons &^= 2
	case walk.MiddleButton:
		w.mouseButtons &^= 4
	}
	w.mouseMove(x, y, button)
}

func (w *appWindow) mouseMove(x, y int, button walk.MouseButton) {
	if button&walk.LeftButton != 0 {
		w.mouseButtons |= 1
	}
	if button&walk.RightButton != 0 {
		w.mouseButtons |= 2
	}
	if button&walk.MiddleButton != 0 {
		w.mouseButtons |= 4
	}
	w.updateCaptureForPoint(x, y)
	w.sendMouse(x, y, 0)
}

func (w *appWindow) mouseWheel(x, y int, button walk.MouseButton) {
	w.sendMouse(x, y, int8(walk.MouseWheelEventDelta(button)/120))
}

func (w *appWindow) sendMouse(x, y int, wheel int8) {
	w.mu.Lock()
	conn := w.conn
	ready := w.inputReady && w.captured
	rect := w.videoRect
	w.mu.Unlock()
	if conn == nil || !ready || rect.Width <= 0 || rect.Height <= 0 {
		w.lastMouseX, w.lastMouseY = x, y
		return
	}
	vx, vy := x-rect.X, y-rect.Y
	if vx < 0 || vy < 0 || vx >= rect.Width || vy >= rect.Height {
		w.releaseCapture()
		return
	}
	relX, relY := x-w.lastMouseX, y-w.lastMouseY
	w.lastMouseX, w.lastMouseY = x, y
	if err := conn.SendMouse(vx, vy, relX, relY, rect.Width, rect.Height, wheel, w.mouseButtons); err != nil {
		w.logf("tx mouse error: %v", err)
	}
}

func (w *appWindow) updateCaptureForPoint(x, y int) {
	w.mu.Lock()
	rect := w.videoRect
	ready := w.connected && w.inputReady
	captured := w.captured
	inside := ready && rect.Width > 0 && rect.Height > 0 && x >= rect.X && y >= rect.Y && x < rect.X+rect.Width && y < rect.Y+rect.Height
	w.mu.Unlock()
	if inside && !captured {
		_ = w.canvas.SetFocus()
		w.setCapture(true)
		w.lastMouseX, w.lastMouseY = x, y
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
	ready := w.connected && w.inputReady
	captured := w.captured
	conn := w.conn
	shouldCapture := ready && inside
	changed := false
	enter := false
	leave := false

	if shouldCapture && !captured {
		w.captured = true
		w.lastMouseX, w.lastMouseY = x, y
		w.lastKeyReport = kvm.KeyboardReport(0)
		w.rawPressed = [256]bool{}
		changed = true
		enter = true
	} else if !shouldCapture && captured {
		w.captured = false
		w.resetCapturedInputLocked()
		changed = true
		leave = true
	}
	w.mu.Unlock()

	if enter {
		_ = w.canvas.SetFocus()
		w.logf("capture enter pointer x=%d y=%d", x, y)
		if conn != nil {
			_ = conn.SendAllKeysUp()
		}
	}
	if leave {
		w.logf("capture leave pointer")
		if conn != nil {
			_ = conn.SendAllKeysUp()
		}
	}
	if changed {
		w.updateChrome()
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
	w.updateChrome()
}

func (w *appWindow) releaseCapture() {
	w.mu.Lock()
	conn := w.conn
	wasCaptured := w.captured
	w.captured = false
	w.resetCapturedInputLocked()
	w.mu.Unlock()
	if conn != nil && wasCaptured {
		_ = conn.SendAllKeysUp()
	}
	w.ui(w.updateChrome)
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
	w.resetCapturedInputLocked()
	w.mu.Unlock()
	if w.ticker != nil {
		w.ticker.Stop()
	}
	w.uninstallInputSink()
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

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
