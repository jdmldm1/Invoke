package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	invokeWindowHandle syscall.Handle
	currentOpacityPct  = 90
	opacityMu          sync.Mutex
)

// styleInvokeWindow polls for the Invoke app window (by title) after it launches
// and applies layered-window alpha so the whole window is semi-transparent.
// Tunable with INVOKE_OPACITY (40-100, percent); 100 disables transparency.
func styleInvokeWindow() {
	if os.Getenv("INVOKE_NO_WINDOW") != "" {
		return
	}

	// Tell Windows this process belongs to a distinct app so it gets its own
	// taskbar button/icon rather than being grouped under msedge.exe/chrome.exe.
	setAppUserModelID()

	state := readAppState()
	pct := state.Opacity
	if pct == 0 {
		pct = 90
	}
	if v := os.Getenv("INVOKE_OPACITY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			pct = n
		}
	}
	if pct > 100 {
		pct = 100
	}
	if pct < 40 {
		pct = 40
	}

	opacityMu.Lock()
	currentOpacityPct = pct
	opacityMu.Unlock()

	user32 := windows.NewLazySystemDLL("user32.dll")
	enumProc := user32.NewProc("EnumWindows")
	getText := user32.NewProc("GetWindowTextW")
	isVisible := user32.NewProc("IsWindowVisible")

	var found syscall.Handle
	cb := syscall.NewCallback(func(h syscall.Handle, l uintptr) uintptr {
		if found != 0 {
			return 0
		}
		if vis, _, _ := isVisible.Call(uintptr(h)); vis == 0 {
			return 1
		}
		buf := make([]uint16, 300)
		getText.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if strings.HasPrefix(windows.UTF16ToString(buf), "Invoke") {
			found = h
			opacityMu.Lock()
			invokeWindowHandle = h
			opacityMu.Unlock()
			return 0 // stop enumeration
		}
		return 1 // continue
	})

	icoPath := ""
	if iconBytes, err := webFS.ReadFile("web/favicon.ico"); err == nil {
		p := filepath.Join(os.TempDir(), "invoke_favicon.ico")
		if os.WriteFile(p, iconBytes, 0644) == nil {
			icoPath = p
		}
	}

	for i := 0; i < 50; i++ { // ~10s of polling
		time.Sleep(200 * time.Millisecond)
		found = 0
		enumProc.Call(cb, 0)
		if found != 0 {
			opacityMu.Lock()
			alpha := byte(255 * currentOpacityPct / 100)
			applyWindowAlpha(found, alpha)
			applyWindowIcon(found, icoPath)
			applyWindowAppID(found, icoPath)
			opacityMu.Unlock()
			return
		}
	}
}

func adjustOpacity(direction string) (int, error) {
	opacityMu.Lock()
	defer opacityMu.Unlock()

	if invokeWindowHandle == 0 {
		user32 := windows.NewLazySystemDLL("user32.dll")
		enumProc := user32.NewProc("EnumWindows")
		getText := user32.NewProc("GetWindowTextW")
		isVisible := user32.NewProc("IsWindowVisible")

		cb := syscall.NewCallback(func(h syscall.Handle, l uintptr) uintptr {
			if vis, _, _ := isVisible.Call(uintptr(h)); vis == 0 {
				return 1
			}
			buf := make([]uint16, 300)
			getText.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
			if strings.HasPrefix(windows.UTF16ToString(buf), "Invoke") {
				invokeWindowHandle = h
				return 0 // stop
			}
			return 1 // continue
		})
		enumProc.Call(cb, 0)
	}

	if invokeWindowHandle == 0 {
		return currentOpacityPct, fmt.Errorf("window not found")
	}

	if direction == "up" {
		currentOpacityPct += 5
	} else if direction == "down" {
		currentOpacityPct -= 5
	} else if direction == "opaque" || direction == "solid" {
		currentOpacityPct = 100
	}

	if currentOpacityPct > 100 {
		currentOpacityPct = 100
	}
	if currentOpacityPct < 40 {
		currentOpacityPct = 40
	}

	alpha := byte(255 * currentOpacityPct / 100)
	applyWindowAlpha(invokeWindowHandle, alpha)
	return currentOpacityPct, nil
}

func applyWindowAlpha(h syscall.Handle, alpha byte) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	getLong := user32.NewProc("GetWindowLongW")
	setLong := user32.NewProc("SetWindowLongW")
	setLWA := user32.NewProc("SetLayeredWindowAttributes")

	idx := int32(-20) // GWL_EXSTYLE
	const wsExLayered = 0x00080000
	const lwaAlpha = 0x2

	ex, _, _ := getLong.Call(uintptr(h), uintptr(idx))
	setLong.Call(uintptr(h), uintptr(idx), ex|wsExLayered)
	setLWA.Call(uintptr(h), 0, uintptr(alpha), lwaAlpha)
}

func applyWindowIcon(h syscall.Handle, icoPath string) {
	if icoPath == "" {
		return
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	loadImage := user32.NewProc("LoadImageW")
	sendMessage := user32.NewProc("SendMessageW")

	pathPtr, err := windows.UTF16PtrFromString(icoPath)
	if err != nil {
		return
	}

	const (
		IMAGE_ICON      = 1
		LR_LOADFROMFILE = 0x0010
		WM_SETICON      = 0x0080
		ICON_SMALL      = 0
		ICON_BIG        = 1
	)

	hIconSmall, _, _ := loadImage.Call(
		0, uintptr(unsafe.Pointer(pathPtr)), uintptr(IMAGE_ICON),
		uintptr(32), uintptr(32), uintptr(LR_LOADFROMFILE),
	)
	hIconBig, _, _ := loadImage.Call(
		0, uintptr(unsafe.Pointer(pathPtr)), uintptr(IMAGE_ICON),
		uintptr(256), uintptr(256), uintptr(LR_LOADFROMFILE),
	)
	if hIconSmall != 0 {
		sendMessage.Call(uintptr(h), uintptr(WM_SETICON), uintptr(ICON_SMALL), hIconSmall)
	}
	if hIconBig != 0 {
		sendMessage.Call(uintptr(h), uintptr(WM_SETICON), uintptr(ICON_BIG), hIconBig)
	}
}

// applyWindowAppID sets the Windows Shell AppUserModelID and relaunch icon
// on the Edge/Chrome window's IPropertyStore via SHGetPropertyStoreForWindow.
// This makes the taskbar show Invoke's icon instead of the browser's icon.
func applyWindowAppID(h syscall.Handle, icoPath string) {
	// IID_IPropertyStore = {886D8EEB-8CF2-4446-8D02-CDBA1DBDCF99}
	iid := windows.GUID{
		Data1: 0x886D8EEB,
		Data2: 0x8CF2,
		Data3: 0x4446,
		Data4: [8]byte{0x8D, 0x02, 0xCD, 0xBA, 0x1D, 0xBD, 0xCF, 0x99},
	}
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	getPropStore := shell32.NewProc("SHGetPropertyStoreForWindow")

	var pStore uintptr
	hr, _, _ := getPropStore.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(&iid)),
		uintptr(unsafe.Pointer(&pStore)),
	)
	if hr != 0 || pStore == 0 {
		return
	}
	defer func() {
		vtbl := *(*[8]uintptr)(unsafe.Pointer(pStore))
		syscall.SyscallN(vtbl[2], pStore, 0, 0) // Release
	}()

	vtbl := *(*[8]uintptr)(unsafe.Pointer(pStore))

	// PROPERTYKEY layout: GUID (16 bytes) + DWORD pid (4 bytes) = 20 bytes
	type PROPERTYKEY struct {
		fmtid windows.GUID
		pid   uint32
	}
	// PROPVARIANT layout on 64-bit: vt(2)+res(6)+ptr(8) = 16 bytes
	type PROPVARIANT struct {
		vt   uint16
		res1 uint16
		res2 uint16
		res3 uint16
		ptr  uintptr
	}

	setVal := func(key PROPERTYKEY, value string) {
		wstr, err := windows.UTF16PtrFromString(value)
		if err != nil {
			return
		}
		pv := PROPVARIANT{vt: 31, ptr: uintptr(unsafe.Pointer(wstr))} // VT_LPWSTR=31
		syscall.SyscallN(vtbl[6], pStore,                              // SetValue (index 6)
			uintptr(unsafe.Pointer(&key)),
			uintptr(unsafe.Pointer(&pv)),
		)
	}

	// PKEY_AppUserModel_ID = {9F4C2855-9F79-4B39-A8D0-E1D42DE1D5F3}, pid=5
	setVal(PROPERTYKEY{
		fmtid: windows.GUID{Data1: 0x9F4C2855, Data2: 0x9F79, Data3: 0x4B39,
			Data4: [8]byte{0xA8, 0xD0, 0xE1, 0xD4, 0x2D, 0xE1, 0xD5, 0xF3}},
		pid: 5,
	}, "Invoke.Terminal.App")

	// PKEY_AppUserModel_RelaunchIconResource = same fmtid, pid=3
	if icoPath != "" {
		setVal(PROPERTYKEY{
			fmtid: windows.GUID{Data1: 0x9F4C2855, Data2: 0x9F79, Data3: 0x4B39,
				Data4: [8]byte{0xA8, 0xD0, 0xE1, 0xD4, 0x2D, 0xE1, 0xD5, 0xF3}},
			pid: 3,
		}, icoPath)
	}

	syscall.SyscallN(vtbl[7], pStore, 0, 0) // Commit (index 7)
}

// setAppUserModelID assigns a Windows Application User Model ID to this process.
// This tells the taskbar to group Invoke's window under its own icon/entry
// instead of the generic msedge.exe/chrome.exe group.
func setAppUserModelID() {
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	proc := shell32.NewProc("SetCurrentProcessExplicitAppUserModelID")

	appID, err := windows.UTF16PtrFromString("Invoke.Terminal.App")
	if err != nil {
		return
	}
	proc.Call(uintptr(unsafe.Pointer(appID)))
}

