package main

import (
	"bytes"
	"context"
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

func ollamaClient(overall time.Duration) *http.Client {
	return &http.Client{
		Timeout: overall,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout:   3 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}
}

func aiGenerateOnce(prompt string, overall time.Duration) (string, error) {
	config := loadConfig()
	host := cleanHost(config.OllamaHost)
	model := config.OllamaModel

	reqBody, _ := json.Marshal(map[string]any{
		"model":      model,
		"prompt":     prompt,
		"stream":     false,
		"keep_alive": -1,
	})

	resp, err := ollamaClient(overall).Post(host+"/api/generate", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama HTTP %d", resp.StatusCode)
	}

	var rd struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rd); err != nil {
		return "", err
	}

	out := strings.TrimSpace(rd.Response)
	for _, fence := range []string{"```powershell", "```cmd", "```text", "```"} {
		out = strings.TrimPrefix(out, fence)
	}
	out = strings.TrimSuffix(out, "```")
	return strings.TrimSpace(out), nil
}

func aiCommitMessageFromDiff(diff string) (string, error) {
	if strings.TrimSpace(diff) == "" {
		return "", fmt.Errorf("no staged changes")
	}
	if len(diff) > 6000 {
		diff = diff[:6000] + "\n...(diff truncated)..."
	}

	prompt := fmt.Sprintf("You are writing a git commit message. Based on the staged diff below, write a single Conventional Commits message:\n"+
		"- A header line: type(scope): subject  (imperative mood, <= 72 chars; scope optional)\n"+
		"- Optionally a blank line then a short body with '- ' bullet points for notable changes.\n"+
		"Valid types: feat, fix, refactor, docs, test, chore, perf, style, build, ci.\n"+
		"Output ONLY the commit message. No code fences, no preamble, no quotes.\n\nDiff:\n%s", diff)

	return aiGenerateOnce(prompt, 45*time.Second)
}

func generateCommitMessageCLI() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	msg, err := aiCommitMessageFromDiff(gitGetStagedDiff(dir))
	if err != nil {
		return
	}
	fmt.Print(msg)
}

func reviewDiffCLI() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}

	diff := gitGetWorkingDiff(dir)
	if strings.TrimSpace(diff) == "" {
		diff = gitGetStagedDiff(dir)
	}
	if strings.TrimSpace(diff) == "" {
		fmt.Println("No uncommitted changes to review.")
		return
	}
	if len(diff) > 3000 {
		diff = diff[:3000] + "\n...(diff truncated; review focuses on the changes above)..."
	}

	config := loadConfig()
	host := cleanHost(config.OllamaHost)
	model := config.OllamaModel

	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#0ea5e9")).Bold(true)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280"))
	fmt.Println(accent.Render("\n⌕ Reviewing your changes") + "\n" +
		muted.Render("  "+model+" at "+host+" (local models can take up to a minute)..."))

	prompt := fmt.Sprintf("You are a senior software engineer reviewing a teammate's uncommitted changes. "+
		"Give a concise, terminal-friendly review as bullet points grouped by severity. Call out concrete bugs, "+
		"edge cases, security issues, and clear style/maintainability problems. Reference the relevant code. "+
		"If the change looks solid, say so briefly. Avoid heavy markdown.\n\nDiff:\n%s", diff)

	reqBody, _ := json.Marshal(map[string]any{
		"model":      model,
		"prompt":     prompt,
		"stream":     true,
		"keep_alive": -1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", host+"/api/generate", bytes.NewBuffer(reqBody))
	if err != nil {
		fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444")).Render("Failed to create request: " + err.Error()))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ollamaClient(0).Do(req)
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
		fmt.Printf("Ollama returned error: %s\n", errMsg)
		return
	}

	fmt.Println(accent.Render("\n=== AI CODE REVIEW ==="))

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
	fmt.Println(accent.Render("\n======================\n"))
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
		actionPath := filepath.Join(home, ".invoke_action.json")
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

func handleAutoconfigureAI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cmd := exec.Command("docker", "start", "ollama")
	hideConsoleWindow(cmd)
	err := cmd.Run()
	if err != nil {
		cmdRun := exec.Command("docker", "run", "-d", "-v", "ollama:/root/.ollama", "-p", "11434:11434", "--name", "ollama", "ollama/ollama")
		hideConsoleWindow(cmdRun)
		_ = cmdRun.Run()
	}

	time.Sleep(2 * time.Second)

	go func() {
		pullBody, _ := json.Marshal(map[string]any{"name": "phi4-mini"})
		client := &http.Client{Timeout: 10 * time.Minute}
		resp, err := client.Post("http://localhost:11434/api/pull", "application/json", bytes.NewBuffer(pullBody))
		if err == nil {
			resp.Body.Close()
		}
	}()

	config := loadConfig()
	config.OllamaHost = "http://localhost:11434"
	config.OllamaModel = "phi4-mini"
	userHome, _ := os.UserHomeDir()
	configPath := filepath.Join(userHome, ".invoke.json")
	if data, err := json.MarshalIndent(config, "", "  "); err == nil {
		_ = os.WriteFile(configPath, data, 0644)
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": "AI Autoconfigure initiated! Started Ollama Docker container, triggered phi4-mini model pull in background, and saved local config.",
	})
}
