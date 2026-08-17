//go:build windows

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/cnlanlansky/claude-patch/internal/claude"
	"github.com/cnlanlansky/claude-patch/internal/update"
	"github.com/cnlanlansky/claude-patch/internal/version"
	"golang.org/x/sys/windows"
)

const (
	wmCreate         = 0x0001
	wmDestroy        = 0x0002
	wmPaint          = 0x000f
	wmClose          = 0x0010
	wmCommand        = 0x0111
	wmDrawItem       = 0x002b
	wmCtlColorStatic = 0x0138
	wmSysColorChange = 0x0015
	wmSettingChange  = 0x001a
	wmDPIChanged     = 0x02e0
	wmApp            = 0x8000
	wmTrayIcon       = wmApp + 1
	wmShowManagement = wmApp + 2
	wmUpdateComplete = wmApp + 3
	wmLButtonDblClk  = 0x0203
	wmRButtonUp      = 0x0205
	wsVisible        = 0x10000000
	wsChild          = 0x40000000
	wsTabStop        = 0x00010000
	wsCaption        = 0x00c00000
	wsSysMenu        = 0x00080000
	wsMinimize       = 0x00020000
	wsClipChildren   = 0x02000000
	bsOwnerDraw      = 0x0000000b
	swHide           = 0
	swShow           = 5
	swRestore        = 9
	cwUseDefault     = ^uintptr(0x7fffffff)

	buttonOpenWeb = 101
	buttonInstall = 102
	buttonRemove  = 104
	buttonUpdate  = 107
	toggleStartup = 105
	toggleTray    = 106

	trayShow    = 201
	trayOpenWeb = 202
	trayExit    = 203

	nimAdd     = 0
	nimDelete  = 2
	nimSetVers = 4
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifShowTip = 0x00000080
	notifyV4   = 4

	mfString    = 0x0000
	mfSeparator = 0x0800
	tpmRight    = 0x0002
	tpmBottom   = 0x0020
	tpmReturn   = 0x0100

	colorWindow    = 5
	idiApplication = 32512
	imageIcon      = 1
	lrDefaultSize  = 0x40
	lrShared       = 0x8000

	dwmCornerPreference = 33
	dwmCornerRound      = 2
	dwmBorderColor      = 34
	dwmCaptionColor     = 35
	dwmTextColor        = 36
)

const (
	colorCanvas  = 0x00f6f4ef
	colorCard    = 0x00ffffff
	colorInk     = 0x00201e1c
	colorMuted   = 0x00756f6a
	colorFaint   = 0x00a49e98
	colorLine    = 0x00e5e0da
	colorPurple  = 0x00d95767
	colorPurple2 = 0x00ede8e4
	colorGreen   = 0x00548834
	colorRed     = 0x00453bae
	colorAmber   = 0x002176a3
)

var (
	guiUser32                  = windows.NewLazySystemDLL("user32.dll")
	guiKernel32                = windows.NewLazySystemDLL("kernel32.dll")
	guiGDI32                   = windows.NewLazySystemDLL("gdi32.dll")
	guiShell32                 = windows.NewLazySystemDLL("shell32.dll")
	procRegisterClassExW       = guiUser32.NewProc("RegisterClassExW")
	procCreateWindowExW        = guiUser32.NewProc("CreateWindowExW")
	procDefWindowProcW         = guiUser32.NewProc("DefWindowProcW")
	procShowWindow             = guiUser32.NewProc("ShowWindow")
	procUpdateWindow           = guiUser32.NewProc("UpdateWindow")
	procGetMessageW            = guiUser32.NewProc("GetMessageW")
	procTranslateMessage       = guiUser32.NewProc("TranslateMessage")
	procDispatchMessageW       = guiUser32.NewProc("DispatchMessageW")
	procPostQuitMessage        = guiUser32.NewProc("PostQuitMessage")
	procDestroyWindow          = guiUser32.NewProc("DestroyWindow")
	procMessageBoxW            = guiUser32.NewProc("MessageBoxW")
	procSetWindowTextW         = guiUser32.NewProc("SetWindowTextW")
	procEnableWindow           = guiUser32.NewProc("EnableWindow")
	procGetWindowTextLengthW   = guiUser32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW         = guiUser32.NewProc("GetWindowTextW")
	procSendMessageW           = guiUser32.NewProc("SendMessageW")
	procPostMessageW           = guiUser32.NewProc("PostMessageW")
	procFindWindowW            = guiUser32.NewProc("FindWindowW")
	procSetForegroundWindow    = guiUser32.NewProc("SetForegroundWindow")
	procIsIconic               = guiUser32.NewProc("IsIconic")
	procInvalidateRect         = guiUser32.NewProc("InvalidateRect")
	procBeginPaint             = guiUser32.NewProc("BeginPaint")
	procEndPaint               = guiUser32.NewProc("EndPaint")
	procFillRect               = guiUser32.NewProc("FillRect")
	procDrawTextW              = guiUser32.NewProc("DrawTextW")
	procGetClientRect          = guiUser32.NewProc("GetClientRect")
	procSetWindowPos           = guiUser32.NewProc("SetWindowPos")
	procGetDpiForWindow        = guiUser32.NewProc("GetDpiForWindow")
	procSetProcessDPIAware     = guiUser32.NewProc("SetProcessDPIAware")
	procLoadImageW             = guiUser32.NewProc("LoadImageW")
	procLoadIconW              = guiUser32.NewProc("LoadIconW")
	procRegisterWindowMessageW = guiUser32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu        = guiUser32.NewProc("CreatePopupMenu")
	procAppendMenuW            = guiUser32.NewProc("AppendMenuW")
	procTrackPopupMenu         = guiUser32.NewProc("TrackPopupMenu")
	procDestroyMenu            = guiUser32.NewProc("DestroyMenu")
	procGetCursorPos           = guiUser32.NewProc("GetCursorPos")
	procCreateSolidBrush       = guiGDI32.NewProc("CreateSolidBrush")
	procCreatePen              = guiGDI32.NewProc("CreatePen")
	procCreateFontW            = guiGDI32.NewProc("CreateFontW")
	procDeleteObject           = guiGDI32.NewProc("DeleteObject")
	procSelectObject           = guiGDI32.NewProc("SelectObject")
	procSetBkMode              = guiGDI32.NewProc("SetBkMode")
	procSetTextColor           = guiGDI32.NewProc("SetTextColor")
	procRoundRect              = guiGDI32.NewProc("RoundRect")
	procShellNotifyIconW       = guiShell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW       = guiKernel32.NewProc("GetModuleHandleW")
	procGetConsoleWindow       = guiKernel32.NewProc("GetConsoleWindow")
)

var (
	guiRuntime       *Runtime
	guiWindow        windows.Handle
	guiBackground    bool
	guiStatusControl windows.Handle
	guiInstance      windows.Handle
	guiIcon          windows.Handle
	guiSettings      DesktopSettings
	guiDPI           uint32 = 96
	guiTrayAdded     bool
	guiExiting       bool
	guiUpdateCancel  context.CancelFunc
	guiUpdateBusy    bool
	taskbarCreated   uint32
	guiUpdateResults = make(chan guiUpdateResult, 1)
	guiFonts         map[fontKind]windows.Handle
	guiCanvasBrush   windows.Handle
	guiControls      = make(map[int]windows.Handle)
)

type fontKind uint8

const (
	fontHero fontKind = iota
	fontTitle
	fontBody
	fontSmall
	fontButton
	fontMetric
)

type wndClassEx struct {
	Size, Style            uint32
	WndProc                uintptr
	ClsExtra, WndExtra     int32
	Instance, Icon, Cursor windows.Handle
	Background             windows.Handle
	MenuName, ClassName    *uint16
	IconSmall              windows.Handle
}

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }
type message struct {
	Window  windows.Handle
	ID      uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   point
	Private uint32
}
type paintStruct struct {
	DC        windows.Handle
	Erase     int32
	Paint     rect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}
type drawItemStruct struct {
	ControlType, ControlID, ItemID, ItemAction, ItemState uint32
	Window, DC                                            windows.Handle
	Bounds                                                rect
	ItemData                                              uintptr
}
type guiUpdateResult struct {
	value update.Result
	err   error
}

type notifyIconData struct {
	Size                       uint32
	Window                     windows.Handle
	ID, Flags, CallbackMessage uint32
	Icon                       windows.Handle
	Tip                        [128]uint16
	State, StateMask           uint32
	Info                       [256]uint16
	TimeoutOrVersion           uint32
	InfoTitle                  [64]uint16
	InfoFlags                  uint32
	Item                       windows.GUID
	BalloonIcon                windows.Handle
}

func RunGUI(runtimeValue *Runtime, background bool) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer runtimeValue.Stop()
	_, _, _ = procSetProcessDPIAware.Call()
	guiRuntime = runtimeValue
	guiBackground = background
	settings, err := ReadDesktopSettings(runtimeValue.Executable)
	if err != nil {
		return err
	}
	guiSettings = settings
	className := windows.StringToUTF16Ptr(managementWindowName)
	title := windows.StringToUTF16Ptr(fmt.Sprintf("Claude Patch %s", version.Current))
	instance, _, _ := procGetModuleHandleW.Call(0)
	guiInstance = windows.Handle(instance)
	guiIcon = loadApplicationIcon(guiInstance)
	class := wndClassEx{
		Size: uint32(unsafe.Sizeof(wndClassEx{})), WndProc: syscall.NewCallback(windowProc),
		Instance: guiInstance, Icon: guiIcon, IconSmall: guiIcon, Cursor: loadArrowCursor(),
		Background: windows.Handle(colorWindow + 1), ClassName: className,
	}
	if atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		return fmt.Errorf("RegisterClassExW: %w", callErr)
	}
	window, _, callErr := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		wsCaption|wsSysMenu|wsMinimize|wsClipChildren,
		cwUseDefault, cwUseDefault, 780, 620, 0, 0, instance, 0,
	)
	if window == 0 {
		return fmt.Errorf("CreateWindowExW: %w", callErr)
	}
	guiWindow = windows.Handle(window)
	if guiBackground && guiTrayAdded {
		procShowWindow.Call(window, swHide)
	} else {
		showManagementWindow()
	}
	var event message
	for {
		result, _, callErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&event)), 0, 0, 0)
		if int32(result) == -1 {
			return fmt.Errorf("GetMessageW: %w", callErr)
		}
		if result == 0 {
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&event)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&event)))
	}
}

func windowProc(window windows.Handle, messageID uint32, wParam uintptr, lParam unsafe.Pointer) uintptr {
	if taskbarCreated != 0 && messageID == taskbarCreated {
		guiTrayAdded = false
		if err := addTrayIcon(window); err != nil {
			procShowWindow.Call(uintptr(window), swShow)
			messageBox(err.Error(), "Claude Patch", 0x10)
		}
		return 0
	}
	switch messageID {
	case wmCreate:
		guiWindow = window
		updateDPI(window)
		createGUIResources()
		createControls(window)
		applyRoundedChrome(window)
		taskbarCreated = registerWindowMessage("TaskbarCreated")
		if err := addTrayIcon(window); err != nil {
			messageBox(err.Error(), "Claude Patch", 0x10)
		}
		refreshStatus()
		return 0
	case wmPaint:
		paintWindow(window)
		return 0
	case wmCommand:
		handleCommand(uint16(wParam & 0xffff))
		return 0
	case wmDrawItem:
		if drawOwnerControl((*drawItemStruct)(lParam)) {
			return 1
		}
		return 0
	case wmCtlColorStatic:
		procSetBkMode.Call(wParam, 1)
		procSetTextColor.Call(wParam, colorMuted)
		return uintptr(guiCanvasBrush)
	case wmTrayIcon:
		switch uint16(uintptr(lParam) & 0xffff) {
		case wmLButtonDblClk:
			showManagementWindow()
		case wmRButtonUp:
			showTrayMenu(window)
		}
		return 0
	case wmShowManagement:
		showManagementWindow()
		return 0
	case wmUpdateComplete:
		handleUpdateComplete()
		return 0
	case wmDPIChanged:
		guiDPI = uint32(wParam & 0xffff)
		if guiDPI == 0 {
			guiDPI = 96
		}
		bounds := (*rect)(lParam)
		procSetWindowPos.Call(uintptr(window), 0, uintptr(bounds.Left), uintptr(bounds.Top), uintptr(bounds.Right-bounds.Left), uintptr(bounds.Bottom-bounds.Top), 0x0004|0x0010)
		createGUIResources()
		procSendMessageW.Call(uintptr(guiStatusControl), 0x0030, uintptr(guiFonts[fontSmall]), 1)
		layoutControls()
		procInvalidateRect.Call(uintptr(window), 0, 1)
		return 0
	case wmSettingChange, wmSysColorChange:
		refreshStatus()
		return 0
	case wmClose:
		if guiSettings.CloseToTray && guiTrayAdded && !guiExiting {
			procShowWindow.Call(uintptr(window), swHide)
			setFeedback("Claude Patch 仍在托盘运行。", false)
			return 0
		}
		guiExiting = true
		procDestroyWindow.Call(uintptr(window))
		return 0
	case wmDestroy:
		if guiUpdateCancel != nil {
			guiUpdateCancel()
			guiUpdateCancel = nil
		}
		guiUpdateBusy = false
		removeTrayIcon(window)
		destroyGUIResources()
		_ = guiRuntime.Stop()
		guiWindow = 0
		procPostQuitMessage.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProcW.Call(uintptr(window), uintptr(messageID), wParam, uintptr(lParam))
	return result
}

func createControls(parent windows.Handle) {
	guiStatusControl = createControl(parent, "STATIC", "准备就绪", wsChild|wsVisible, 0, 0, 0, 0, 0)
	procSendMessageW.Call(uintptr(guiStatusControl), 0x0030, uintptr(guiFonts[fontSmall]), 1)
	guiControls[buttonOpenWeb] = createControl(parent, "BUTTON", "打开 Web 管理", wsChild|wsVisible|wsTabStop|bsOwnerDraw, buttonOpenWeb, 0, 0, 0, 0)
	guiControls[buttonInstall] = createControl(parent, "BUTTON", "安装命令", wsChild|wsVisible|wsTabStop|bsOwnerDraw, buttonInstall, 0, 0, 0, 0)
	guiControls[buttonRemove] = createControl(parent, "BUTTON", "卸载命令", wsChild|wsVisible|wsTabStop|bsOwnerDraw, buttonRemove, 0, 0, 0, 0)
	guiControls[buttonUpdate] = createControl(parent, "BUTTON", "检查更新", wsChild|wsVisible|wsTabStop|bsOwnerDraw, buttonUpdate, 0, 0, 0, 0)
	guiControls[toggleStartup] = createControl(parent, "BUTTON", "登录 Windows 后启动", wsChild|wsVisible|wsTabStop|bsOwnerDraw, toggleStartup, 0, 0, 0, 0)
	guiControls[toggleTray] = createControl(parent, "BUTTON", "关闭窗口后隐藏到托盘", wsChild|wsVisible|wsTabStop|bsOwnerDraw, toggleTray, 0, 0, 0, 0)
	layoutControls()
}

func createControl(parent windows.Handle, class, text string, style uint32, id int, x, y, width, height int32) windows.Handle {
	classPtr, textPtr := windows.StringToUTF16Ptr(class), windows.StringToUTF16Ptr(text)
	handle, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(classPtr)), uintptr(unsafe.Pointer(textPtr)), uintptr(style), uintptr(x), uintptr(y), uintptr(width), uintptr(height), uintptr(parent), uintptr(id), uintptr(guiInstance), 0)
	return windows.Handle(handle)
}

func layoutControls() {
	if guiWindow == 0 {
		return
	}
	moveControl(guiStatusControl, 38, 552, 704, 24)
	moveControl(guiControls[buttonOpenWeb], 30, 483, 170, 46)
	moveControl(guiControls[buttonInstall], 214, 483, 170, 46)
	moveControl(guiControls[buttonRemove], 398, 483, 170, 46)
	moveControl(guiControls[buttonUpdate], 582, 483, 168, 46)
	moveControl(guiControls[toggleStartup], 46, 346, 668, 48)
	moveControl(guiControls[toggleTray], 46, 404, 668, 48)
}

func moveControl(control windows.Handle, x, y, width, height int32) {
	if control == 0 {
		return
	}
	procSetWindowPos.Call(uintptr(control), 0, uintptr(scale(x)), uintptr(scale(y)), uintptr(scale(width)), uintptr(scale(height)), 0x0004|0x0010)
}

func handleCommand(id uint16) {
	switch id {
	case buttonOpenWeb, trayOpenWeb:
		if err := OpenURL(guiRuntime.Router.ManagementURL()); err != nil {
			showError(err)
		}
	case buttonInstall:
		_, err := InstallCommand(guiRuntime.Executable)
		showOperation("命令代理已安装，新开终端后生效。", err)
	case buttonRemove:
		_, err := UninstallCommand(guiRuntime.Executable)
		showOperation("命令代理已卸载，Claude Code 本体未修改。", err)
	case buttonUpdate:
		startUpdateCheck()
	case toggleStartup:
		if guiSettings.StartupConflict {
			showError(fmt.Errorf("检测到同名但不属于 Claude Patch 的开机启动项，未做修改"))
			return
		}
		next := !guiSettings.StartupEnabled
		if guiSettings.StartupEnabled && !guiSettings.StartupHealthy {
			next = true
		}
		if err := SetStartupEnabled(guiRuntime.Executable, next); err != nil {
			showError(err)
			return
		}
		setFeedback(map[bool]string{true: "已开启：下次登录 Windows 后将在托盘启动。", false: "已关闭开机启动。"}[next], false)
	case toggleTray:
		next := !guiSettings.CloseToTray
		if err := SetCloseToTray(next); err != nil {
			showError(err)
			return
		}
		setFeedback(map[bool]string{true: "关闭按钮现在会隐藏到托盘。", false: "关闭按钮现在会退出 Claude Patch。"}[next], false)
	case trayShow:
		showManagementWindow()
	case trayExit:
		guiExiting = true
		procPostMessageW.Call(uintptr(guiWindow), wmClose, 0, 0)
	}
	refreshStatus()
}

func startUpdateCheck() {
	if guiUpdateBusy || guiWindow == 0 {
		return
	}
	guiUpdateBusy = true
	button := guiControls[buttonUpdate]
	if button != 0 {
		procEnableWindow.Call(uintptr(button), 0)
		setControlText(button, "检查中…")
	}
	setFeedback("正在检查 GitHub Releases…", false)
	ctx, cancel := context.WithCancel(context.Background())
	guiUpdateCancel = cancel
	window := guiWindow
	go func() {
		value, err := update.Check(ctx, version.Current, nil)
		guiUpdateResults <- guiUpdateResult{value: value, err: err}
		procPostMessageW.Call(uintptr(window), wmUpdateComplete, 0, 0)
	}()
}

func handleUpdateComplete() {
	result := <-guiUpdateResults
	if guiUpdateCancel != nil {
		guiUpdateCancel()
		guiUpdateCancel = nil
	}
	guiUpdateBusy = false
	button := guiControls[buttonUpdate]
	if button != 0 {
		procEnableWindow.Call(uintptr(button), 1)
		setControlText(button, "检查更新")
	}
	if result.err != nil {
		if !errors.Is(result.err, context.Canceled) {
			showError(result.err)
		}
		return
	}
	if result.value.DevelopmentBuild {
		setFeedback(fmt.Sprintf("当前为开发构建；最新公开版本为 %s，无法比较。", result.value.LatestVersion), false)
		return
	}
	if result.value.UpdateAvailable {
		setFeedback(fmt.Sprintf("发现新版本 %s，当前为 %s。", result.value.LatestVersion, result.value.CurrentVersion), false)
		if err := OpenURL(result.value.ReleaseURL); err != nil {
			showError(err)
		}
		return
	}
	if result.value.CurrentAhead {
		setFeedback(fmt.Sprintf("当前版本 %s 高于最新公开版本 %s。", result.value.CurrentVersion, result.value.LatestVersion), false)
		return
	}
	setFeedback(fmt.Sprintf("当前已是最新版本 %s。", result.value.CurrentVersion), false)
}

func setControlText(control windows.Handle, value string) {
	text := windows.StringToUTF16Ptr(value)
	procSetWindowTextW.Call(uintptr(control), uintptr(unsafe.Pointer(text)))
}

func showOperation(message string, err error) {
	if err != nil {
		showError(err)
		return
	}
	setFeedback(message, false)
}

func refreshStatus() {
	settings, settingsErr := ReadDesktopSettings(guiRuntime.Executable)
	if settingsErr == nil {
		guiSettings = settings
	}
	if settingsErr != nil {
		setFeedback(settingsErr.Error(), true)
	}
	procInvalidateRect.Call(uintptr(guiWindow), 0, 1)
	procInvalidateRect.Call(uintptr(guiControls[toggleStartup]), 0, 1)
	procInvalidateRect.Call(uintptr(guiControls[toggleTray]), 0, 1)
}

func setFeedback(value string, failed bool) {
	if guiStatusControl == 0 {
		return
	}
	if failed {
		value = "错误 · " + value
	}
	text := windows.StringToUTF16Ptr(value)
	procSetWindowTextW.Call(uintptr(guiStatusControl), uintptr(unsafe.Pointer(text)))
	procInvalidateRect.Call(uintptr(guiWindow), 0, 0)
}

func paintWindow(window windows.Handle) {
	var paint paintStruct
	dc, _, _ := procBeginPaint.Call(uintptr(window), uintptr(unsafe.Pointer(&paint)))
	if dc == 0 {
		return
	}
	defer procEndPaint.Call(uintptr(window), uintptr(unsafe.Pointer(&paint)))
	var client rect
	procGetClientRect.Call(uintptr(window), uintptr(unsafe.Pointer(&client)))
	procFillRect.Call(dc, uintptr(unsafe.Pointer(&client)), uintptr(guiCanvasBrush))
	procSetBkMode.Call(dc, 1)

	drawBrand(windows.Handle(dc))
	drawStatusCards(windows.Handle(dc))
	drawSectionTitles(windows.Handle(dc))
}

func drawBrand(dc windows.Handle) {
	drawRoundedBox(dc, rectS(30, 24, 68, 62), colorPurple, colorPurple, 12)
	drawText(dc, "ϟ", rectS(30, 26, 68, 60), fontTitle, colorCard, 0x00000001|0x00000004|0x00000020)
	drawText(dc, "Claude Patch", rectS(82, 22, 500, 52), fontHero, colorInk, 0x00000020)
	drawText(dc, "LOCAL CONTROL PLANE · CLAUDE CODE 2.1.233", rectS(84, 54, 550, 76), fontSmall, colorMuted, 0x00000020)
	drawPill(dc, "ROUTER 运行中", rectS(592, 30, 722, 58), colorGreen)
}

func drawSectionTitles(dc windows.Handle) {
	drawText(dc, "运行状态", rectS(30, 92, 300, 118), fontTitle, colorInk, 0x00000020)
	drawText(dc, "本机状态只读检测，不会启动或修改 Claude。", rectS(30, 117, 550, 140), fontSmall, colorMuted, 0x00000020)
	drawText(dc, "桌面设置", rectS(30, 296, 300, 322), fontTitle, colorInk, 0x00000020)
	drawText(dc, "所有开关仅在点击时写入当前用户设置。", rectS(30, 321, 520, 342), fontSmall, colorMuted, 0x00000020)
}

func drawStatusCards(dc windows.Handle) {
	discovery, discoveryErr := claude.Discover(guiRuntime.Router.Config().Claude.Executable)
	command, commandErr := CommandState(guiRuntime.Executable)
	claudeMetric, claudeNote := "未检测到", "需要 Claude Code 2.1.233"
	claudeColor := uint32(colorRed)
	if discoveryErr == nil {
		claudeMetric, claudeNote, claudeColor = discovery.PackageVersion, truncatePath(discovery.ExecutablePath, 29), colorGreen
	}
	commandMetric, commandNote := "未安装", "可使用下方按钮安装"
	commandColor := uint32(colorMuted)
	if commandErr == nil && command.Healthy && command.InPath {
		commandMetric, commandNote, commandColor = "正常", "claude 命令已接管", colorGreen
	} else if commandErr == nil && command.Installed {
		commandMetric, commandNote, commandColor = "异常", "点击卸载后重新安装", colorAmber
	}
	drawStatusCard(dc, rectS(30, 151, 258, 268), "CLAUDE CODE", claudeMetric, claudeNote, claudeColor)
	drawStatusCard(dc, rectS(274, 151, 502, 268), "命令代理", commandMetric, commandNote, commandColor)
	drawStatusCard(dc, rectS(518, 151, 746, 268), "本地 ROUTER", "在线", guiRuntime.Router.Origin, colorPurple)
}

func drawStatusCard(dc windows.Handle, bounds rect, label, metric, note string, accent uint32) {
	drawRoundedBox(dc, bounds, colorCard, colorLine, 14)
	dot := rect{bounds.Left + scale(18), bounds.Top + scale(18), bounds.Left + scale(28), bounds.Top + scale(28)}
	drawRoundedBox(dc, dot, accent, accent, 5)
	drawText(dc, label, rect{bounds.Left + scale(36), bounds.Top + scale(13), bounds.Right - scale(12), bounds.Top + scale(36)}, fontSmall, colorMuted, 0x00000020)
	drawText(dc, metric, rect{bounds.Left + scale(18), bounds.Top + scale(47), bounds.Right - scale(15), bounds.Top + scale(78)}, fontMetric, colorInk, 0x00000020|0x00000004)
	drawText(dc, note, rect{bounds.Left + scale(18), bounds.Top + scale(84), bounds.Right - scale(15), bounds.Bottom - scale(10)}, fontSmall, colorMuted, 0x00000020|0x00000004|0x00008000)
}

func startupDescription() string {
	if guiSettings.StartupConflict {
		return "存在同名陌生启动项，Claude Patch 不会覆盖"
	}
	if guiSettings.StartupEnabled && !guiSettings.StartupHealthy {
		return "启动路径已变化，点击开关修复为当前程序"
	}
	return "登录后静默进入托盘，不弹出主窗口"
}

func drawOwnerControl(item *drawItemStruct) bool {
	if item == nil || item.DC == 0 {
		return false
	}
	procSetBkMode.Call(uintptr(item.DC), 1)
	id := int(item.ControlID)
	switch id {
	case buttonOpenWeb, buttonInstall, buttonRemove, buttonUpdate:
		primary := id == buttonOpenWeb
		fill, border, textColor := uint32(colorCard), uint32(colorLine), uint32(colorInk)
		if primary {
			fill, border, textColor = colorPurple, colorPurple, colorCard
		}
		if item.ItemState&0x0001 != 0 {
			fill = colorPurple2
			if primary {
				fill = 0x00b94a59
			}
		}
		drawRoundedBox(item.DC, item.Bounds, fill, border, 11)
		drawText(item.DC, controlText(item.Window), item.Bounds, fontButton, textColor, 0x00000001|0x00000004|0x00000020)
		return true
	case toggleStartup, toggleTray:
		drawSettingsControl(item, id)
		return true
	}
	return false
}

func drawSettingsControl(item *drawItemStruct, id int) {
	fill, border := uint32(colorCard), uint32(colorLine)
	if item.ItemState&0x0001 != 0 {
		fill = 0x00faf7f4
	}
	drawRoundedBox(item.DC, item.Bounds, fill, border, 12)
	checked, title, note := guiSettings.StartupEnabled, "登录 Windows 后启动", startupDescription()
	if id == toggleTray {
		checked, title, note = guiSettings.CloseToTray, "关闭窗口后隐藏到托盘", "保持 Router 运行；从托盘菜单可明确退出"
	}
	textLeft := item.Bounds.Left + scale(16)
	drawText(item.DC, title, rect{textLeft, item.Bounds.Top + scale(7), item.Bounds.Right - scale(90), item.Bounds.Top + scale(27)}, fontBody, colorInk, 0x00000020|0x00000004)
	drawText(item.DC, note, rect{textLeft, item.Bounds.Top + scale(27), item.Bounds.Right - scale(90), item.Bounds.Bottom - scale(5)}, fontSmall, colorMuted, 0x00000020|0x00000004|0x00008000)
	drawToggle(item.DC, rect{item.Bounds.Right - scale(66), item.Bounds.Top + scale(11), item.Bounds.Right - scale(16), item.Bounds.Top + scale(37)}, checked)
}

func drawToggle(dc windows.Handle, bounds rect, checked bool) {
	fill := uint32(0x00ddd9d4)
	if checked {
		fill = colorPurple
	}
	drawRoundedBox(dc, bounds, fill, fill, 13)
	diameter := bounds.Bottom - bounds.Top - scale(6)
	left := bounds.Left + scale(3)
	if checked {
		left = bounds.Right - diameter - scale(3)
	}
	drawRoundedBox(dc, rect{left, bounds.Top + scale(3), left + diameter, bounds.Bottom - scale(3)}, colorCard, colorCard, diameter/2)
}

func drawRoundedBox(dc windows.Handle, bounds rect, fillColor, borderColor uint32, radius int32) {
	brush, _, _ := procCreateSolidBrush.Call(uintptr(fillColor))
	pen, _, _ := procCreatePen.Call(0, uintptr(scale(1)), uintptr(borderColor))
	oldBrush, _, _ := procSelectObject.Call(uintptr(dc), brush)
	oldPen, _, _ := procSelectObject.Call(uintptr(dc), pen)
	procRoundRect.Call(uintptr(dc), uintptr(bounds.Left), uintptr(bounds.Top), uintptr(bounds.Right), uintptr(bounds.Bottom), uintptr(scale(radius)), uintptr(scale(radius)))
	procSelectObject.Call(uintptr(dc), oldBrush)
	procSelectObject.Call(uintptr(dc), oldPen)
	procDeleteObject.Call(brush)
	procDeleteObject.Call(pen)
}

func drawText(dc windows.Handle, text string, bounds rect, font fontKind, color uint32, format uint32) {
	if text == "" {
		return
	}
	procSelectObject.Call(uintptr(dc), uintptr(guiFonts[font]))
	procSetTextColor.Call(uintptr(dc), uintptr(color))
	utf16 := windows.StringToUTF16(text)
	procDrawTextW.Call(uintptr(dc), uintptr(unsafe.Pointer(&utf16[0])), uintptr(len(utf16)-1), uintptr(unsafe.Pointer(&bounds)), uintptr(format))
}

func drawPill(dc windows.Handle, text string, bounds rect, accent uint32) {
	drawRoundedBox(dc, bounds, 0x00edf6ee, 0x00d5ead8, 14)
	drawText(dc, text, bounds, fontSmall, accent, 0x00000001|0x00000004|0x00000020)
}

func createGUIResources() {
	destroyGUIResources()
	guiCanvasBrush = createBrush(colorCanvas)
	guiFonts = map[fontKind]windows.Handle{
		fontHero:   createFont(25, 700),
		fontTitle:  createFont(17, 600),
		fontBody:   createFont(14, 600),
		fontSmall:  createFont(11, 400),
		fontButton: createFont(13, 600),
		fontMetric: createFont(20, 700),
	}
}

func destroyGUIResources() {
	for _, handle := range guiFonts {
		if handle != 0 {
			procDeleteObject.Call(uintptr(handle))
		}
	}
	guiFonts = nil
	if guiCanvasBrush != 0 {
		procDeleteObject.Call(uintptr(guiCanvasBrush))
	}
	guiCanvasBrush = 0
}

func createBrush(color uint32) windows.Handle {
	handle, _, _ := procCreateSolidBrush.Call(uintptr(color))
	return windows.Handle(handle)
}
func createFont(size, weight int32) windows.Handle {
	face := windows.StringToUTF16Ptr("Segoe UI")
	handle, _, _ := procCreateFontW.Call(uintptr(-scale(size)), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))
	return windows.Handle(handle)
}

func addTrayIcon(window windows.Handle) error {
	if guiTrayAdded {
		return nil
	}
	data := notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: window, ID: 1, Flags: nifMessage | nifIcon | nifTip | nifShowTip, CallbackMessage: wmTrayIcon, Icon: guiIcon}
	copy(data.Tip[:], windows.StringToUTF16("Claude Patch · Router 运行中"))
	result, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&data)))
	if result == 0 {
		return fmt.Errorf("Shell_NotifyIconW(add): %w", err)
	}
	data.TimeoutOrVersion = notifyV4
	procShellNotifyIconW.Call(nimSetVers, uintptr(unsafe.Pointer(&data)))
	guiTrayAdded = true
	return nil
}

func removeTrayIcon(window windows.Handle) {
	if !guiTrayAdded {
		return
	}
	data := notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: window, ID: 1}
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
	guiTrayAdded = false
}

func showTrayMenu(window windows.Handle) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	appendMenu(menu, mfString, trayShow, "显示主窗口")
	appendMenu(menu, mfString, trayOpenWeb, "打开 Web 管理")
	procAppendMenuW.Call(menu, mfSeparator, 0, 0)
	appendMenu(menu, mfString, trayExit, "退出 Claude Patch")
	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	procSetForegroundWindow.Call(uintptr(window))
	command, _, _ := procTrackPopupMenu.Call(menu, tpmRight|tpmBottom|tpmReturn, uintptr(cursor.X), uintptr(cursor.Y), 0, uintptr(window), 0)
	if command != 0 {
		handleCommand(uint16(command))
	}
}

func appendMenu(menu uintptr, flags, id uintptr, text string) {
	value := windows.StringToUTF16Ptr(text)
	procAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(value)))
}

func showManagementWindow() {
	if guiWindow == 0 {
		return
	}
	iconic, _, _ := procIsIconic.Call(uintptr(guiWindow))
	show := uintptr(swShow)
	if iconic != 0 {
		show = swRestore
	}
	procShowWindow.Call(uintptr(guiWindow), show)
	procSetForegroundWindow.Call(uintptr(guiWindow))
	procUpdateWindow.Call(uintptr(guiWindow))
}

func updateDPI(window windows.Handle) {
	dpi, _, _ := procGetDpiForWindow.Call(uintptr(window))
	if dpi >= 96 {
		guiDPI = uint32(dpi)
	}
}

func scale(value int32) int32 {
	return int32((int64(value)*int64(guiDPI) + 48) / 96)
}

func rectS(left, top, right, bottom int32) rect {
	return rect{scale(left), scale(top), scale(right), scale(bottom)}
}

func applyRoundedChrome(window windows.Handle) {
	corner := uint32(dwmCornerRound)
	_ = windows.DwmSetWindowAttribute(windows.HWND(window), dwmCornerPreference, unsafe.Pointer(&corner), uint32(unsafe.Sizeof(corner)))
	caption, border, text := uint32(colorCanvas), uint32(colorLine), uint32(colorInk)
	_ = windows.DwmSetWindowAttribute(windows.HWND(window), dwmCaptionColor, unsafe.Pointer(&caption), 4)
	_ = windows.DwmSetWindowAttribute(windows.HWND(window), dwmBorderColor, unsafe.Pointer(&border), 4)
	_ = windows.DwmSetWindowAttribute(windows.HWND(window), dwmTextColor, unsafe.Pointer(&text), 4)
}

func loadApplicationIcon(instance windows.Handle) windows.Handle {
	handle, _, _ := procLoadImageW.Call(uintptr(instance), 1, imageIcon, 0, 0, lrDefaultSize|lrShared)
	if handle != 0 {
		return windows.Handle(handle)
	}
	handle, _, _ = procLoadIconW.Call(0, uintptr(idiApplication))
	return windows.Handle(handle)
}

func loadArrowCursor() windows.Handle {
	handle, _, _ := procLoadImageW.Call(0, uintptr(32512), 2, 0, 0, lrDefaultSize|lrShared)
	return windows.Handle(handle)
}

func registerWindowMessage(name string) uint32 {
	value := windows.StringToUTF16Ptr(name)
	message, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(value)))
	return uint32(message)
}

func controlText(control windows.Handle) string {
	length, _, _ := procGetWindowTextLengthW.Call(uintptr(control))
	if length == 0 {
		return ""
	}
	buffer := make([]uint16, length+1)
	procGetWindowTextW.Call(uintptr(control), uintptr(unsafe.Pointer(&buffer[0])), length+1)
	return windows.UTF16ToString(buffer)
}

func truncatePath(value string, limit int) string {
	if len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return "…" + string(runes[len(runes)-limit+1:])
}

func showError(err error) {
	if err != nil {
		setFeedback(err.Error(), true)
		messageBox(err.Error(), "Claude Patch", 0x10)
	}
}

func ShowError(err error) {
	if err == nil {
		return
	}
	if hasConsole() {
		_, _ = fmt.Fprintln(os.Stderr, "Claude Patch:", err)
		return
	}
	messageBox(err.Error(), "Claude Patch", 0x10)
}

func messageBox(text, title string, flags uintptr) {
	textPtr, titlePtr := windows.StringToUTF16Ptr(strings.TrimSpace(text)), windows.StringToUTF16Ptr(title)
	procMessageBoxW.Call(uintptr(guiWindow), uintptr(unsafe.Pointer(textPtr)), uintptr(unsafe.Pointer(titlePtr)), flags)
}

func hasConsole() bool {
	window, _, _ := procGetConsoleWindow.Call()
	return window != 0
}

func OpenURL(value string) error {
	verb, target := windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(value)
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}
