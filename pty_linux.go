//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/creack/pty"
)

type LinuxPty struct {
	ptmx *os.File
	cmd  *exec.Cmd
}

func (p *LinuxPty) Read(b []byte) (n int, err error) {
	return p.ptmx.Read(b)
}

func (p *LinuxPty) Write(b []byte) (n int, err error) {
	return p.ptmx.Write(b)
}

func (p *LinuxPty) Resize(cols, rows int) error {
	ws := &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	}
	return pty.Setsize(p.ptmx, ws)
}

func (p *LinuxPty) Close() error {
	if p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
	return p.ptmx.Close()
}

func getShellCommand(env []string) (*exec.Cmd, []string, func()) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "bash"
	}

	exePath, err := os.Executable()
	if err != nil {
		exePath = os.Args[0]
	}
	scriptPath := filepath.Join(filepath.Dir(exePath), "invoke.sh")

	cleanup := func() {}

	if strings.HasSuffix(shell, "bash") {
		tmpRC, err := os.CreateTemp("", "invoke_bashrc_*")
		if err == nil {
			tmpRC.WriteString("if [ -f ~/.bashrc ]; then source ~/.bashrc; fi\n")
			tmpRC.WriteString(fmt.Sprintf("export INVOKE_EXE='%s'\n", exePath))
			tmpRC.WriteString(fmt.Sprintf("if [ -f '%s' ]; then source '%s'; fi\n", scriptPath, scriptPath))
			tmpRC.Close()
			cleanup = func() { os.Remove(tmpRC.Name()) }
			return exec.Command(shell, "--rcfile", tmpRC.Name()), env, cleanup
		}
	} else if strings.HasSuffix(shell, "zsh") {
		tmpDir, err := os.MkdirTemp("", "invoke_zsh_*")
		if err == nil {
			zshrcPath := filepath.Join(tmpDir, ".zshrc")
			f, _ := os.Create(zshrcPath)
			f.WriteString("if [ -f ~/.zshrc ]; then source ~/.zshrc; fi\n")
			f.WriteString(fmt.Sprintf("export INVOKE_EXE='%s'\n", exePath))
			f.WriteString(fmt.Sprintf("if [ -f '%s' ]; then source '%s'; fi\n", scriptPath, scriptPath))
			f.Close()
			cleanup = func() { os.RemoveAll(tmpDir) }
			env = append(env, "ZDOTDIR="+tmpDir)
			return exec.Command(shell), env, cleanup
		}
	}

	return exec.Command(shell), env, cleanup
}

func startPty(cols, rows int, env []string, cwd string) (Pty, error) {
	cmd, finalEnv, cleanup := getShellCommand(env)
	defer cleanup()

	cmd.Env = finalEnv
	if cwd != "" {
		if _, statErr := os.Stat(cwd); statErr == nil {
			cmd.Dir = cwd
		}
	}

	ws := &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	}

	ptmx, err := pty.StartWithSize(cmd, ws)
	if err != nil {
		return nil, err
	}

	return &LinuxPty{ptmx: ptmx, cmd: cmd}, nil
}
