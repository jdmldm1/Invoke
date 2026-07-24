//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	setWindowLongPtr = func() *windows.LazyProc {
		p := user32.NewProc("SetWindowLongPtrW")
		if p.Find() != nil {
			return user32.NewProc("SetWindowLongW")
		}
		return p
	}()
	callWindowProc      = user32.NewProc("CallWindowProcW")
	showWindow          = user32.NewProc("ShowWindow")
	trackPopupMenu      = user32.NewProc("TrackPopupMenu")
	createPopupMenu     = user32.NewProc("CreatePopupMenu")
	appendMenu          = user32.NewProc("AppendMenuW")
	destroyMenu         = user32.NewProc("DestroyMenu")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	getCursorPos        = user32.NewProc("GetCursorPos")
)

type POINT struct {
	X, Y int32
}

const (
	GWLP_WNDPROC     = -4
	WM_CLOSE         = 0x0010
	WM_COMMAND       = 0x0111
	WM_TRAY_CALLBACK = 0x0400 + 100

	ID_TRAY_OPEN = 1001
	ID_TRAY_EXIT = 1002
)

type NOTIFYICONDATAW struct {
	CbSize           uint32
	HWnd             syscall.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            syscall.Handle
	Tip              [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	TimeoutOrVersion uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     syscall.Handle
}

const (
	NIM_ADD     = 0x00000000
	NIM_DELETE  = 0x00000002
	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004
)

var (
	originalWndProc uintptr
	appHwnd         syscall.Handle
)

func isKeepAliveEnabled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	configBytes, err := os.ReadFile(filepath.Join(home, ".invoke.json"))
	if err != nil {
		return false
	}
	var cfg struct {
		KeepAlive bool `json:"keep_alive"`
	}
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return false
	}
	return cfg.KeepAlive
}

func wndProc(hWnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_CLOSE:
		if isKeepAliveEnabled() {
			showWindow.Call(uintptr(hWnd), 0)
			return 0
		}
	case WM_TRAY_CALLBACK:
		switch lParam {
		case 0x0203:
			showWindow.Call(uintptr(hWnd), 9)
			setForegroundWindow.Call(uintptr(hWnd))
			return 0
		case 0x0205:
			var pt POINT
			getCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

			setForegroundWindow.Call(uintptr(hWnd))

			hMenu, _, _ := createPopupMenu.Call()
			defer destroyMenu.Call(hMenu)

			openStr, _ := windows.UTF16PtrFromString("Open Invoke")
			exitStr, _ := windows.UTF16PtrFromString("Exit")

			appendMenu.Call(hMenu, 0, ID_TRAY_OPEN, uintptr(unsafe.Pointer(openStr)))
			appendMenu.Call(hMenu, 0x0800, 0, 0)
			appendMenu.Call(hMenu, 0, ID_TRAY_EXIT, uintptr(unsafe.Pointer(exitStr)))

			trackPopupMenu.Call(hMenu, 0x0002|0x0100, uintptr(pt.X), uintptr(pt.Y), 0, uintptr(hWnd), 0)
			return 0
		}
	case WM_COMMAND:
		switch wParam & 0xffff {
		case ID_TRAY_OPEN:
			showWindow.Call(uintptr(hWnd), 9)
			setForegroundWindow.Call(uintptr(hWnd))
			return 0
		case ID_TRAY_EXIT:
			removeTrayIcon()
			if serverCmd != nil && serverCmd.Process != nil {
				_ = serverCmd.Process.Kill()
			}
			os.Exit(0)
			return 0
		}
	}
	r, _, _ := callWindowProc.Call(originalWndProc, uintptr(hWnd), uintptr(msg), wParam, lParam)
	return r
}

func setupSystemTray(w webview2.WebView) {
	hwndVal := w.Window()
	if hwndVal == nil {
		return
	}
	appHwnd = syscall.Handle(uintptr(hwndVal))

	addTrayIcon(appHwnd)

	wndProcCallback := syscall.NewCallback(wndProc)
	var gwlpWndProc int32 = GWLP_WNDPROC
	r1, _, _ := setWindowLongPtr.Call(uintptr(appHwnd), uintptr(gwlpWndProc), wndProcCallback)
	originalWndProc = r1
}

func addTrayIcon(hWnd syscall.Handle) {
	var nid NOTIFYICONDATAW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hWnd
	nid.UID = 1
	nid.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
	nid.UCallbackMessage = WM_TRAY_CALLBACK

	hIcon, _, _ := user32.NewProc("SendMessageW").Call(uintptr(hWnd), 0x007f, 1, 0)
	if hIcon == 0 {
		hIcon, _, _ = user32.NewProc("SendMessageW").Call(uintptr(hWnd), 0x007f, 0, 0)
	}
	if hIcon == 0 {
		hIcon, _, _ = user32.NewProc("LoadIconW").Call(0, 32512)
	}
	nid.HIcon = syscall.Handle(hIcon)

	copy(nid.Tip[:], windows.StringToUTF16("Invoke Terminal"))

	shell32 := windows.NewLazySystemDLL("shell32.dll")
	shell32.NewProc("Shell_NotifyIconW").Call(NIM_ADD, uintptr(unsafe.Pointer(&nid)))
}

func removeTrayIcon() {
	if appHwnd == 0 {
		return
	}
	var nid NOTIFYICONDATAW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = appHwnd
	nid.UID = 1

	shell32 := windows.NewLazySystemDLL("shell32.dll")
	shell32.NewProc("Shell_NotifyIconW").Call(NIM_DELETE, uintptr(unsafe.Pointer(&nid)))
}
