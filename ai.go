package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ollamaClient builds an HTTP client with a fast dial timeout (so unreachable
// hosts fail quickly) and an overall timeout for the whole request. Pass
// overall = 0 for streaming requests that may run long.
func ollamaClient(overall time.Duration) *http.Client {
	return &http.Client{
		Timeout: overall,
		Transport: &http.Transport{
			Proxy: nil, // Bypass system proxy to prevent hangs on local/intranet hosts
			DialContext: (&net.Dialer{
				Timeout:   3 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}
}

// aiGenerateOnce performs a single non-streaming generation and returns the
// cleaned response text (markdown fences stripped).
func aiGenerateOnce(prompt string, overall time.Duration) (string, error) {
	config := loadConfig()
	host := cleanHost(config.OllamaHost)
	model := config.OllamaModel

	reqBody, _ := json.Marshal(map[string]interface{}{
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

// aiCommitMessageFromDiff asks the model for a Conventional Commits message
// describing the given staged diff. Shared by the `gencommit` CLI and the TUI.
func aiCommitMessageFromDiff(diff string) (string, error) {
	if strings.TrimSpace(diff) == "" {
		return "", fmt.Errorf("no staged changes")
	}
	// Cap the diff so we stay within the model's context window.
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

// generateCommitMessageCLI reads the staged diff and prints ONLY the generated
// commit message to stdout. Backs `pt commit`.
func generateCommitMessageCLI() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	msg, err := aiCommitMessageFromDiff(gitGetStagedDiff(dir))
	if err != nil {
		return // Silent: invoke.ps1 treats empty output as "couldn't generate".
	}
	fmt.Print(msg)
}

// reviewDiffCLI streams an AI code review of the current uncommitted changes.
// Backs `pt review`.
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
	// Keep the prompt small so local models reach the first token quickly;
	// prefill time on a large diff dominates latency on modest hardware.
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

	reqBody, _ := json.Marshal(map[string]interface{}{
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
		fmt.Printf("Ollama error: HTTP %d\n", resp.StatusCode)
		return
	}

	fmt.Println(accent.Render("\n┌─ Code Review ──────────────────────"))
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		var token struct {
			Response string `json:"response"`
			Done     bool   `json:"done"`
		}
		if err := json.Unmarshal(line, &token); err == nil {
			fmt.Print(token.Response)
			if token.Done {
				break
			}
		}
	}
	fmt.Println(accent.Render("\n└────────────────────────────────────") + "\n")
}
