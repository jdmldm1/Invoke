package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type PortInfo struct {
	Port        int    `json:"Port"`
	PID         int    `json:"PID"`
	ProcessName string `json:"ProcessName"`
}

const _CREATE_NO_WINDOW = 0x08000000

var (
	invokeWindowHandle syscall.Handle
	currentOpacityPct  = 90
	opacityMu          sync.Mutex
)

func consoleOwnedByUs() bool {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	proc := kernel32.NewProc("GetConsoleProcessList")
	pids := make([]uint32, 8)
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return r == 1
}

func hideOwnConsole() {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	user32 := windows.NewLazySystemDLL("user32.dll")
	hwnd, _, _ := kernel32.NewProc("GetConsoleWindow").Call()
	if hwnd != 0 {
		user32.NewProc("ShowWindow").Call(hwnd, 0)
	}
}

func spawnDetachedTerm() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "term")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: _CREATE_NO_WINDOW}
	_ = cmd.Start()
}

func launchDefaultWindow() {
	if consoleOwnedByUs() {
		hideOwnConsole()
		serveTerminalWindow()
	} else {
		spawnDetachedTerm()
	}
}

func styleInvokeWindow() {
	defer func() {
		if r := recover(); r != nil {
		}
	}()

	if os.Getenv("INVOKE_NO_WINDOW") != "" {
		return
	}

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
			return 0
		}
		return 1
	})

	icoPath := ""
	if iconBytes, err := webFS.ReadFile("web/favicon.ico"); err == nil {
		p := filepath.Join(os.TempDir(), "invoke_favicon.ico")
		if os.WriteFile(p, iconBytes, 0644) == nil {
			icoPath = p
		}
	}

	for range 50 {
		time.Sleep(200 * time.Millisecond)
		found = 0
		enumProc.Call(cb, 0)
		if found != 0 {
			opacityMu.Lock()
			alpha := byte(255 * currentOpacityPct / 100)
			applyWindowAlpha(found, alpha)
			applyWindowIcon(found, icoPath)
			opacityMu.Unlock()
			return
		}
	}
}

func adjustOpacity(direction string) (int, error) {
	opacityMu.Lock()
	unlock := true
	defer func() {
		if unlock {
			opacityMu.Unlock()
		}
	}()

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
				return 0
			}
			return 1
		})
		opacityMu.Unlock()
		unlock = false
		enumProc.Call(cb, 0)
		opacityMu.Lock()
		unlock = true
	}

	if invokeWindowHandle == 0 {
		return currentOpacityPct, fmt.Errorf("window not found")
	}

	switch direction {
	case "up":
		currentOpacityPct += 5
	case "down":
		currentOpacityPct -= 5
	case "opaque", "solid":
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

	idx := int32(-20)
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

func setAppUserModelID() {
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	proc := shell32.NewProc("SetCurrentProcessExplicitAppUserModelID")

	appID, err := windows.UTF16PtrFromString("Invoke.Terminal.App")
	if err != nil {
		return
	}
	proc.Call(uintptr(unsafe.Pointer(appID)))
}

func handlePorts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	cmdText := `Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue | ForEach-Object { [PSCustomObject]@{ Port = $_.LocalPort; PID = $_.OwningProcess; ProcessName = (Get-Process -Id $_.OwningProcess -ErrorAction SilentlyContinue).Name } } | ConvertTo-Json`

	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", cmdText)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		http.Error(w, fmt.Sprintf("Error fetching ports: %v, %s", err, stderr.String()), 500)
		return
	}

	output := stdout.Bytes()
	if len(bytes.TrimSpace(output)) == 0 {
		w.Write([]byte("[]"))
		return
	}

	w.Write(output)
}

func handlePortsKill(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	pidStr := r.URL.Query().Get("pid")
	if pidStr == "" {
		http.Error(w, `{"error":"pid parameter required"}`, 400)
		return
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		http.Error(w, `{"error":"invalid pid"}`, 400)
		return
	}

	cmdText := fmt.Sprintf("Stop-Process -Id %d -Force", pid)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", cmdText)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": stderr.String()})
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func handlePortsHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	htmlBytes, err := webFS.ReadFile("web/ports.html")
	if err != nil {
		http.Error(w, "Ports template not found", 404)
		return
	}
	w.Write(htmlBytes)
}

func cleanHost(host string) string {
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		return "http://" + host
	}
	return host
}

func handleFreePort(w http.ResponseWriter, r *http.Request) {
	portStr := r.URL.Query().Get("port")
	w.Header().Set("Content-Type", "application/json")
	if portStr == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "port parameter required"})
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "invalid port number"})
		return
	}

	pid, err := findPIDHoldingPort(port)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": err.Error()})
		return
	}
	if pid == 0 {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": fmt.Sprintf("No active process found on port %d.", port)})
		return
	}

	err = killProcess(pid)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": fmt.Sprintf("Failed to release port: %v", err)})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"pid":     pid,
		"message": fmt.Sprintf("Successfully released port %d (terminated process %d).", port, pid),
	})
}

func findPIDHoldingPort(port int) (int, error) {
	cmd := exec.Command("netstat", "-ano")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	targetLocalAddr1 := fmt.Sprintf(":%d", port)
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		localAddr := fields[1]
		if strings.HasSuffix(localAddr, targetLocalAddr1) {
			pidStr := fields[len(fields)-1]
			pid, err := strconv.Atoi(pidStr)
			if err == nil {
				return pid, nil
			}
		}
	}
	return 0, nil
}

func killProcess(pid int) error {
	cmd := exec.Command("taskkill", "/f", "/pid", strconv.Itoa(pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}

func readWindowsClipboardText() string {
	user32 := windows.NewLazySystemDLL("user32.dll")
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")

	openClipboard := user32.NewProc("OpenClipboard")
	closeClipboard := user32.NewProc("CloseClipboard")
	getClipboardData := user32.NewProc("GetClipboardData")
	globalLock := kernel32.NewProc("GlobalLock")
	globalUnlock := kernel32.NewProc("GlobalUnlock")

	const CF_UNICODETEXT = 13

	r, _, _ := openClipboard.Call(0)
	if r == 0 {
		return ""
	}
	defer closeClipboard.Call()

	hData, _, _ := getClipboardData.Call(CF_UNICODETEXT)
	if hData == 0 {
		return ""
	}

	ptr, _, _ := globalLock.Call(hData)
	if ptr == 0 {
		return ""
	}
	defer globalUnlock.Call(hData)

	return windows.UTF16ToString((*[1 << 20]uint16)(unsafe.Pointer(ptr))[:])
}

func handleClipboardRead(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	text := readWindowsClipboardText()
	_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
}
