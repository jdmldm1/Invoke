package main

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const _CREATE_NO_WINDOW = 0x08000000

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
