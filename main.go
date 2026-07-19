package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func main() {
	initConfig()
	initLayouts()

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
	default:
		fmt.Printf("Unknown command: %s\nRun 'pt help' to see available commands.\n", command)
	}
}

func runTerminalMode() {
	exePath, err := os.Executable()
	if err != nil {
		exePath = os.Args[0]
	}
	exePath, _ = filepath.Abs(exePath)
	exeDir := filepath.Dir(exePath)
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

	cmd := exec.Command(shell, "-NoExit", "-NoLogo", "-Command", initCmd)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

func generateCommandOnly(query string) {
	config := loadConfig()
	host := cleanHost(config.OllamaHost)
	model := config.OllamaModel

	sysPrompt := "You are a PowerShell command generator. Output ONLY the single executable PowerShell command and nothing else. No markdown, no code fences, no explanation."
	fullPrompt := fmt.Sprintf("%s\n\nUser request: %s\n\nPowerShell Command:", sysPrompt, query)

	reqBody, _ := json.Marshal(map[string]any{
		"model":  model,
		"prompt": fullPrompt,
		"stream": false,
	})

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		},
	}
	resp, err := client.Post(host+"/api/generate", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}

	var respData struct {
		Response string `json:"response"`
	}
	if json.NewDecoder(resp.Body).Decode(&respData) != nil {
		return
	}

	out := strings.TrimSpace(respData.Response)
	for _, fence := range []string{"```powershell", "```cmd", "```"} {
		out = strings.TrimPrefix(out, fence)
	}
	out = strings.TrimSuffix(out, "```")
	fmt.Print(strings.TrimSpace(out))
}

func executeAIDo(query string) {
	fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#0ea5e9")).Bold(true).Render("\nThinking...") + lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(" (Contacting Gemma4 at Shiloh)"))
	config := loadConfig()
	host := cleanHost(config.OllamaHost)
	model := config.OllamaModel

	prompt := fmt.Sprintf("You are a command line generator. Output ONLY the exact PowerShell or cmd command(s) that should be executed to accomplish this user request: '%s'. Do not use markdown backticks, explanations, or templates. Output nothing else but the exact command.", query)

	client := &http.Client{}
	reqBody, _ := json.Marshal(map[string]any{
		"model":  model,
		"prompt": prompt,
		"stream": true,
	})

	resp, err := client.Post(host+"/api/generate", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444")).Render("Failed to connect to AI Generator: " + err.Error()))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("Ollama returned HTTP error status %d\n", resp.StatusCode)
		return
	}

	fmt.Print(lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8")).Bold(true).Render("Generated Command: "))

	dec := json.NewDecoder(resp.Body)
	var fullCommand strings.Builder
	for {
		var token struct {
			Response string `json:"response"`
			Done     bool   `json:"done"`
		}
		if err := dec.Decode(&token); err != nil {
			break
		}
		fmt.Print(token.Response)
		fullCommand.WriteString(token.Response)
		if token.Done {
			break
		}
	}
	fmt.Println()

	cmdText := strings.TrimSpace(fullCommand.String())
	cmdText = strings.TrimPrefix(cmdText, "```powershell")
	cmdText = strings.TrimPrefix(cmdText, "```powershell.exe")
	cmdText = strings.TrimPrefix(cmdText, "```cmd")
	cmdText = strings.TrimPrefix(cmdText, "```")
	cmdText = strings.TrimSuffix(cmdText, "```")
	cmdText = strings.TrimSpace(cmdText)

	if cmdText == "" {
		fmt.Println("No command generated.")
		return
	}

	home, err := os.UserHomeDir()
	if err == nil {
		actionPath := filepath.Join(home, ".powerterm_action.json")
		actionData := map[string]string{
			"cmd":     cmdText,
			"confirm": "true",
		}
		file, err := os.Create(actionPath)
		if err == nil {
			_ = json.NewEncoder(file).Encode(actionData)
			file.Close()
		}
	}
}

func askAI(question string) {
	config := loadConfig()
	host := cleanHost(config.OllamaHost)
	model := config.OllamaModel

	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#0ea5e9")).Bold(true)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280"))
	fmt.Println(accent.Render("\n? "+question) + "\n" + muted.Render("  asking "+model+" at "+host+"..."))

	prompt := fmt.Sprintf("You are a concise, knowledgeable assistant for a software developer working in a terminal. Answer the question clearly and directly. Use plain text suitable for a terminal (no heavy markdown). Keep it focused.\n\nQuestion: %s", question)

	reqBody, _ := json.Marshal(map[string]any{
		"model":  model,
		"prompt": prompt,
		"stream": true,
	})

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		},
	}

	resp, err := client.Post(host+"/api/generate", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444")).Render("Failed to reach AI at " + host + ": " + err.Error()))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		errMsg := errResp.Error
		if errMsg == "" {
			errMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		fmt.Printf("Ollama error: %s\n", errMsg)
		return
	}

	fmt.Println(accent.Render("\n┌─ Answer ───────────────────────────"))

	dec := json.NewDecoder(resp.Body)
	for {
		var token struct {
			Response string `json:"response"`
			Done     bool   `json:"done"`
		}
		if err := dec.Decode(&token); err != nil {
			break
		}
		fmt.Print(token.Response)
		if token.Done {
			break
		}
	}
	fmt.Println(accent.Render("\n└────────────────────────────────────") + "\n")
}

func explainError(errorText string) {
	fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#a855f7")).Bold(true).Render("\nQuerying AI Explainer..."))
	fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("Contacting Gemma4 at Shiloh..."))

	config := loadConfig()
	host := cleanHost(config.OllamaHost)
	model := config.OllamaModel

	prompt := fmt.Sprintf("A PowerShell command failed with the following error output:\n%s\n\nExplain the error briefly (2-3 sentences) and provide the corrected command if applicable. Do not use complex markdown templates, keep it clean for terminal.", errorText)
	client := &http.Client{}
	reqBody, _ := json.Marshal(map[string]any{
		"model":  model,
		"prompt": prompt,
		"stream": true,
	})

	resp, err := client.Post(host+"/api/generate", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444")).Render("Failed to connect to AI Explainer: " + err.Error()))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		var errResp struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		errMsg := errResp.Error
		if errMsg == "" {
			errMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		fmt.Printf("Ollama returned error: %s\n", errMsg)
		return
	}

	fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#34d399")).Bold(true).Render("\n=== AI EXPLANATION & FIX ==="))

	dec := json.NewDecoder(resp.Body)
	for {
		var token struct {
			Response string `json:"response"`
			Done     bool   `json:"done"`
		}
		if err := dec.Decode(&token); err != nil {
			break
		}
		fmt.Print(token.Response)
		if token.Done {
			break
		}
	}
	fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#34d399")).Bold(true).Render("\n============================\n"))
}

func getHoverInfo(targetCmd string) {
	config := loadConfig()
	host := cleanHost(config.OllamaHost)
	model := config.OllamaModel

	prompt := fmt.Sprintf("Provide a brief, VS Code style hover tooltip for the PowerShell command/keyword: '%s'. Format: 1-2 sentences explaining what it does, followed by one short practical example. Do not use markdown wrappers.", targetCmd)
	client := &http.Client{}
	reqBody, _ := json.Marshal(map[string]any{
		"model":  model,
		"prompt": prompt,
		"stream": false,
	})

	resp, err := client.Post(host+"/api/generate", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444")).Render("Failed to connect to AI Explainer: " + err.Error()))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return
	}

	var respData struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#38bdf8")).
		Padding(0, 1).
		Foreground(lipgloss.Color("#f8fafc"))

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#34d399")).Bold(true)

	content := titleStyle.Render("Command Info: "+targetCmd) + "\n\n" + strings.TrimSpace(respData.Response)
	fmt.Println("\n" + boxStyle.Render(content) + "\n")
}
