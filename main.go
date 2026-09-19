package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	// If started without arguments, check if we're running as a Windows Service
	if len(os.Args) < 2 {
		if checkServiceAndRun() {
			return
		}
	} else if len(os.Args) >= 4 && os.Args[3] == "--system" {
		// MSI installer custom actions pass --system to indicate a system-wide install
		os.Setenv("INVOKE_SYSTEM", "1")
	}

	initConfig()
	initLayouts()

	// Handle explicit commands
	if len(os.Args) > 1 {
		command := os.Args[1]
		if command == "set-network-password" {
			if len(os.Args) < 3 {
				fmt.Println("Usage: invoke-server set-network-password <password> [--system]")
				return
			}
			setNetworkPasswordCLI(os.Args[2])
			return
		}
		// Let other commands fall through to the switch statement below
	}

	if len(os.Args) < 2 {
		launchDefaultWindow()
		return
	}

	command := os.Args[1]
	switch command {
	case "term", "window":
		serveTerminalWindow()
	case "shell":
		runTerminalMode()
	case "git":
		dir, err := os.Getwd()
		if err != nil {
			dir = "."
		}
		absDir, _ := filepath.Abs(dir)
		if host := os.Getenv("INVOKE_HOST"); host != "" {
			url := fmt.Sprintf("%s/open", host)
			body, _ := json.Marshal(map[string]string{"type": "git", "file": absDir})
			resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
			if err == nil && resp.StatusCode == 200 {
				resp.Body.Close()
				fmt.Println("Opened git overview in a new tab.")
				return
			}
		}
		serveGitOverview(absDir)
	case "do":
		if len(os.Args) < 3 {
			fmt.Println("Usage: pt do <natural language request>")
			return
		}
		query := strings.Join(os.Args[2:], " ")
		executeAIDo(query)
	case "ask":
		if len(os.Args) < 3 {
			fmt.Println("Usage: pt ask <your question>")
			return
		}
		askAI(strings.Join(os.Args[2:], " "))
	case "explain":
		if len(os.Args) < 3 {
			fmt.Println("Usage: pt explain \"<error text>\"")
			return
		}
		errorText := strings.Join(os.Args[2:], " ")
		explainError(errorText)
	case "edit":
		if len(os.Args) < 3 {
			fmt.Println("Usage: pt edit <file>")
			return
		}
		serveMonacoEditor(os.Args[2])
	case "diff":
		if len(os.Args) < 3 {
			fmt.Println("Usage: pt diff <file>")
			return
		}
		serveMonacoDiff(os.Args[2])
	case "gen":
		if len(os.Args) < 3 {
			return
		}
		generateCommandOnly(strings.Join(os.Args[2:], " "))
	case "gencommit":
		generateCommitMessageCLI()
	case "review":
		reviewDiffCLI()
	case "info":
		if len(os.Args) < 3 {
			return
		}
		getHoverInfo(os.Args[2])
	case "wsl":
		runWSLMode()
	case "install-service":
		err := installService("InvokeService", "Invoke Remote Terminal Service")
		if err != nil {
			fmt.Printf("Failed to install service: %v\n", err)
		} else {
			fmt.Println("Service installed successfully.")
		}
	case "uninstall-service":
		err := removeService("InvokeService")
		if err != nil {
			fmt.Printf("Failed to remove service: %v\n", err)
		} else {
			fmt.Println("Service removed successfully.")
		}
	case "service":
		runService("InvokeService", true)
	default:
		fmt.Printf("Unknown command: %s\nRun 'pt help' to see available commands.\n", command)
	}
}

func runWSLMode() {
	wslCmd := "wsl.exe"
	var cmd *exec.Cmd
	if len(os.Args) >= 3 {
		distro := os.Args[2]
		cmd = exec.Command(wslCmd, "-d", distro)
	} else {
		cmd = exec.Command(wslCmd)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

func runTerminalMode() {
	exePath, err := os.Executable()
	if err != nil {
		exePath = os.Args[0]
	}
	exePath, _ = filepath.Abs(exePath)
	exeDir := filepath.Dir(exePath)

	if runtime.GOOS == "linux" {
		scriptPath := filepath.Join(exeDir, "invoke.sh")

		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "bash"
		}

		tmpRC, err := os.CreateTemp("", "invoke_bashrc_*")
		if err == nil {
			tmpRC.WriteString("if [ -f ~/.bashrc ]; then source ~/.bashrc; fi\n")
			tmpRC.WriteString(fmt.Sprintf("export INVOKE_EXE='%s'\n", exePath))
			tmpRC.WriteString(fmt.Sprintf("if [ -f '%s' ]; then source '%s'; else echo 'invoke.sh not found'; fi\n", scriptPath, scriptPath))
			tmpRC.Close()
			defer os.Remove(tmpRC.Name())

			cmd := exec.Command(shell, "--rcfile", tmpRC.Name())
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			_ = cmd.Run()
		}
		return
	}

	scriptPath := filepath.Join(exeDir, "invoke.ps1")

	escapedExe := strings.ReplaceAll(exePath, "'", "''")
	escapedScript := strings.ReplaceAll(scriptPath, "'", "''")

	initCmd := fmt.Sprintf(
		`$env:INVOKE_EXE = '%s'; `+
			`if (Test-Path '%s') { . '%s' } `+
			`else { Write-Host "invoke.ps1 not found next to invoke.exe" -ForegroundColor Red }`,
		escapedExe, escapedScript, escapedScript,
	)

	shell := "powershell.exe"
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		shell = p
	} else if p, err := exec.LookPath("pwsh"); err == nil {
		shell = p
	}

	cmd := exec.Command(shell, "-ExecutionPolicy", "Bypass", "-NoExit", "-NoLogo", "-Command", initCmd)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}
