//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/UserExistsError/conpty"
)

type WindowsPty struct {
	cpty *conpty.ConPty
}

func (w *WindowsPty) Read(p []byte) (n int, err error) {
	return w.cpty.Read(p)
}

func (w *WindowsPty) Write(p []byte) (n int, err error) {
	return w.cpty.Write(p)
}

func (w *WindowsPty) Resize(cols, rows int) error {
	return w.cpty.Resize(cols, rows)
}

func (w *WindowsPty) Close() error {
	return w.cpty.Close()
}

func shellCommandLine() string {
	exePath, err := os.Executable()
	if err != nil {
		exePath = os.Args[0]
	}
	exePath, _ = filepath.Abs(exePath)
	scriptPath := filepath.Join(filepath.Dir(exePath), "invoke.ps1")
	escExe := strings.ReplaceAll(exePath, "'", "''")
	escScript := strings.ReplaceAll(scriptPath, "'", "''")

	initCmd := fmt.Sprintf("$env:INVOKE_EXE='%s'; if (Test-Path '%s') { . '%s' }", escExe, escScript, escScript)

	shell := "powershell.exe"
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		shell = p
	} else if p, err := exec.LookPath("pwsh"); err == nil {
		shell = p
	}
	return fmt.Sprintf(`"%s" -ExecutionPolicy Bypass -NoLogo -NoExit -Command "%s"`, shell, initCmd)
}

func startPty(cols, rows int, env []string, cwd string) (Pty, error) {
	opts := []conpty.ConPtyOption{conpty.ConPtyDimensions(cols, rows), conpty.ConPtyEnv(env)}
	if cwd != "" {
		if _, statErr := os.Stat(cwd); statErr == nil {
			opts = append(opts, conpty.ConPtyWorkDir(cwd))
		}
	}
	cpty, err := conpty.Start(shellCommandLine(), opts...)
	if err != nil {
		return nil, err
	}
	return &WindowsPty{cpty: cpty}, nil
}
