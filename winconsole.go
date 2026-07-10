package main

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const _CREATE_NO_WINDOW = 0x08000000

// consoleOwnedByUs reports whether this process is the only one attached to its
// console — i.e. it was double-clicked / launched standalone rather than run
// from an existing shell (where the shell is also attached).
func consoleOwnedByUs() bool {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	proc := kernel32.NewProc("GetConsoleProcessList")
	pids := make([]uint32, 8)
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return r == 1
}

// hideOwnConsole hides this process's console window (used when we own it, so
// the default window mode doesn't leave a stray black console on screen).
func hideOwnConsole() {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	user32 := windows.NewLazySystemDLL("user32.dll")
	hwnd, _, _ := kernel32.NewProc("GetConsoleWindow").Call()
	if hwnd != 0 {
		user32.NewProc("ShowWindow").Call(hwnd, 0) // SW_HIDE
	}
}

// spawnDetachedTerm launches "invoke.exe term" in its own hidden console so
// it never shares (and cannot corrupt) the calling shell's console.
func spawnDetachedTerm() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "term")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: _CREATE_NO_WINDOW}
	_ = cmd.Start()
}

// launchDefaultWindow is the no-argument entry point: open the Invoke window.
// When double-clicked we own the console, so hide it and serve in-process; when
// run from a shell we detach so the prompt returns immediately.
func launchDefaultWindow() {
	if consoleOwnedByUs() {
		hideOwnConsole()
		serveTerminalWindow()
	} else {
		spawnDetachedTerm()
	}
}
