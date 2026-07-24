package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/gorilla/websocket"
)

type ChatHistoryEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatState struct {
	History []ChatHistoryEntry `json:"history"`
	Paths   []string           `json:"paths"`
}

type wsMessage struct {
	Type      string              `json:"type"`
	Question  string              `json:"question"`
	Paths     []string            `json:"paths"`
	History   []map[string]string `json:"history"`
	Status    string              `json:"status"`
	Command   string              `json:"command"`
	UseJarvis bool                `json:"use_jarvis"`
}

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

func handleAIComplete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Line string `json:"line"`
		CWD  string `json:"cwd"`
		OS   string `json:"os"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Line) == "" {
		json.NewEncoder(w).Encode(map[string]string{"completion": ""})
		return
	}

	cfg := loadConfig()
	osHint := "Windows PowerShell"
	if strings.ToLower(req.OS) == "linux" {
		osHint = "Linux bash"
	}

	systemPrompt := "You are a " + osHint + " shell autocomplete engine. " +
		"Complete the partial command the user is typing. " +
		"Reply with ONLY the completion suffix (the characters after the cursor), nothing else. " +
		"If you are unsure, reply with an empty string."

	userPrompt := "Current directory: " + req.CWD + "\nPartial command: " + req.Line

	payload := map[string]any{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"stream":      false,
		"num_predict": 40,
		"temperature": 0.1,
	}

	body, _ := json.Marshal(payload)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"completion": ""})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"completion": ""})
		return
	}

	completion := strings.TrimSpace(ollamaResp.Message.Content)
	completion = strings.TrimPrefix(completion, "`")
	completion = strings.TrimSuffix(completion, "`")
	if idx := strings.IndexByte(completion, '\n'); idx >= 0 {
		completion = completion[:idx]
	}

	json.NewEncoder(w).Encode(map[string]string{"completion": completion})
}

func handleAITranslate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Query string `json:"query"`
		CWD   string `json:"cwd"`
		OS    string `json:"os"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		http.Error(w, `{"error":"query, cwd and os required"}`, 400)
		return
	}

	cfg := loadConfig()
	osHint := "Windows PowerShell"
	if strings.ToLower(req.OS) == "linux" {
		osHint = "Linux bash"
	}

	systemPrompt := "You are a terminal command translator. Translate the user's natural language request into a single executable shell command " +
		"for " + osHint + " in the directory: " + req.CWD + ".\n" +
		"Output ONLY the raw command, without markdown code fences, without quotes, and without any explanation."

	payload := map[string]any{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": req.Query},
		},
		"stream": false,
	}

	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"command": ""})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(respBody, &ollamaResp)

	cmd := strings.TrimSpace(ollamaResp.Message.Content)
	cmd = strings.TrimPrefix(cmd, "`")
	cmd = strings.TrimSuffix(cmd, "`")

	json.NewEncoder(w).Encode(map[string]string{"command": cmd})
}

func handleAIEditCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Code        string `json:"code"`
		Instruction string `json:"instruction"`
		Lang        string `json:"lang"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" || strings.TrimSpace(req.Instruction) == "" {
		http.Error(w, `{"error":"code and instruction required"}`, 400)
		return
	}

	cfg := loadConfig()
	systemPrompt := "You are a professional code refactoring and generation tool. " +
		"Perform the user's edit instruction on the provided " + req.Lang + " code.\n" +
		"Output ONLY the corrected code block. Do NOT include markdown code fences, do NOT include explanations, and do NOT write preamble/postamble."

	userPrompt := "Code:\n" + req.Code + "\n\nInstruction:\n" + req.Instruction

	payload := map[string]any{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"stream": false,
	}

	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"code": ""})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(respBody, &ollamaResp)

	output := strings.TrimSpace(ollamaResp.Message.Content)
	output = strings.TrimPrefix(output, "```"+req.Lang)
	output = strings.TrimPrefix(output, "```")
	output = strings.TrimSuffix(output, "```")
	output = strings.TrimSpace(output)

	json.NewEncoder(w).Encode(map[string]string{"code": output})
}

func handleAIScaffoldScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Prompt) == "" {
		http.Error(w, `{"error":"prompt required"}`, 400)
		return
	}

	cfg := loadConfig()
	systemPrompt := "You are a PowerShell scripting assistant. Write a functional PowerShell script (.ps1) to accomplish the user's task.\n" +
		"Output ONLY the raw PowerShell script code, with no markdown code fences, no quotes, and no formatting explanations. Start directly with the code."

	payload := map[string]any{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": req.Prompt},
		},
		"stream": false,
	}

	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"code": ""})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(respBody, &ollamaResp)

	output := strings.TrimSpace(ollamaResp.Message.Content)
	output = strings.TrimPrefix(output, "```powershell")
	output = strings.TrimPrefix(output, "```")
	output = strings.TrimSuffix(output, "```")
	output = strings.TrimSpace(output)

	json.NewEncoder(w).Encode(map[string]string{"code": output})
}

func handleAIExplainHover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Symbol  string `json:"symbol"`
		Line    string `json:"line"`
		Context string `json:"context"`
		Lang    string `json:"lang"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Symbol == "" {
		http.Error(w, `{"error":"symbol required"}`, 400)
		return
	}

	cfg := loadConfig()
	systemPrompt := "You are a code explainer tooltip generator. Briefly explain what the selected code keyword, symbol, or function does in 1-3 sentences.\n" +
		"Keep it clear and precise. Do not write complex markdown tags. Do not repeat the symbol name."

	userPrompt := fmt.Sprintf("Language: %s\nSymbol: %s\nLine context: %s\nCode context:\n%s", req.Lang, req.Symbol, req.Line, req.Context)

	payload := map[string]any{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"stream": false,
	}

	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"explanation": ""})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(respBody, &ollamaResp)

	explanation := strings.TrimSpace(ollamaResp.Message.Content)
	json.NewEncoder(w).Encode(map[string]string{"explanation": explanation})
}

func handleAIEditorComplete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Before string `json:"before"`
		Lang   string `json:"lang"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Before == "" {
		json.NewEncoder(w).Encode(map[string]string{"completion": ""})
		return
	}

	cfg := loadConfig()
	systemPrompt := "You are a code completion model. Write the exact immediate next characters/lines of code that should continue from the user's cursor position.\n" +
		"Output ONLY the completion suffix, without markdown backticks, without formatting, and without any explanation."

	payload := map[string]any{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": req.Before},
		},
		"stream":      false,
		"num_predict": 60,
		"temperature": 0.2,
	}

	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"completion": ""})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(respBody, &ollamaResp)

	completion := strings.TrimSpace(ollamaResp.Message.Content)
	completion = strings.TrimPrefix(completion, "```"+req.Lang)
	completion = strings.TrimPrefix(completion, "```")
	completion = strings.TrimSuffix(completion, "```")
	completion = strings.TrimSpace(completion)

	json.NewEncoder(w).Encode(map[string]string{"completion": completion})
}

func buildChatContext(paths []string) string {
	var b strings.Builder
	total := 0
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			fmt.Fprintf(&b, "\n# Directory listing: %s\n", p)
			entries, _ := os.ReadDir(p)
			for i, e := range entries {
				if i > 200 {
					b.WriteString("  ...(more)\n")
					break
				}
				name := e.Name()
				if e.IsDir() {
					name += "/"
				}
				fmt.Fprintf(&b, "  %s\n", name)
			}
		} else {
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			content := string(data)
			if len(content) > 12000 {
				content = content[:12000] + "\n...(truncated)..."
			}
			fmt.Fprintf(&b, "\n# File: %s\n```\n%s\n```\n", p, content)
			total += len(content)
		}
		if total > 40000 {
			b.WriteString("\n...(context truncated to fit the model)...\n")
			break
		}
	}
	return b.String()
}

func sendChat(conn *websocket.Conn, typ, text string) {
	b, _ := json.Marshal(map[string]string{"type": typ, "text": text})
	conn.WriteMessage(websocket.TextMessage, b)
}

func extractAttr(header, attr string) string {
	search := attr + "=\""
	idx := strings.Index(header, search)
	if idx < 0 {
		return ""
	}
	start := idx + len(search)
	end := strings.Index(header[start:], "\"")
	if end < 0 {
		return ""
	}
	return header[start : start+end]
}

func writeFileTool(path, content string) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "/" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0644)
}

func readFileTool(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func runCommandTool(cmdStr string) (string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", cmdStr)
	} else {
		cmd = exec.Command("bash", "-c", cmdStr)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func handleChatWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	responseChan := make(chan string, 1)
	msgChan := make(chan wsMessage)
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				close(msgChan)
				return
			}
			var m wsMessage
			if err := json.Unmarshal(data, &m); err == nil {
				if m.Type == "" {
					m.Type = "question"
				}
				msgChan <- m
			}
		}
	}()

	var currentCancel context.CancelFunc

	for msg := range msgChan {
		switch msg.Type {
		case "tool_response":
			select {
			case responseChan <- msg.Status:
			default:
			}

		case "run_command":
			if currentCancel != nil {
				currentCancel()
			}
			var ctx context.Context
			ctx, currentCancel = context.WithCancel(context.Background())

			go func(c context.Context, cmd string) {
				reqPayload, _ := json.Marshal(map[string]string{
					"tool": "execute_command",
					"args": cmd,
				})
				sendChat(conn, "tool_request", string(reqPayload))

				select {
				case status := <-responseChan:
					if status == "approved" {
						sendChat(conn, "tool_status", "Executing command: "+cmd)
						output, err := runCommandTool(cmd)
						var result string
						if err != nil {
							result = fmt.Sprintf("Error running command: %v\nOutput: %s", err, output)
						} else {
							result = output
						}
						sendChat(conn, "tool_result", result)
						sendChat(conn, "done", "")
					} else {
						sendChat(conn, "tool_status", "Command rejected.")
						sendChat(conn, "done", "")
					}
				case <-c.Done():
					return
				}
			}(ctx, msg.Command)

		case "question":
			if currentCancel != nil {
				currentCancel()
			}

			var ctx context.Context
			ctx, currentCancel = context.WithCancel(context.Background())

			go func(c context.Context, q string, p []string, h []map[string]string, uj bool) {
				streamChatAgent(c, conn, q, p, h, responseChan, uj)
			}(ctx, msg.Question, msg.Paths, msg.History, msg.UseJarvis)
		}
	}
	if currentCancel != nil {
		currentCancel()
	}
}

func streamChatAgent(ctx context.Context, conn *websocket.Conn, question string, paths []string, history []map[string]string, responseChan chan string, useJarvisFromClient bool) {
	config := loadConfig()
	host := cleanHost(config.OllamaHost)
	model := config.OllamaModel

	sysPrompt := "You are an agentic AI coding assistant running inside a developer terminal. " +
		"You can read files, write/modify files, and run terminal commands to help the user. " +
		"Every time you need to execute an action, you MUST use one of the following XML-style tags. " +
		"You must write the complete tag, including both the opening and closing tags, and then stop generating. " +
		"Do NOT write any explanation, text, or multiple tags together. Just write the complete tag and stop. " +
		"Here are the tools:\n" +
		"1. Run terminal command:\n" +
		"<execute_command>your_command_here</execute_command>\n" +
		"2. Write a new file or modify an existing file:\n" +
		"<write_file path=\"file_path\">file_content_here</write_file>\n" +
		"3. Read a file from workspace:\n" +
		"<read_file>file_path</read_file>\n\n" +
		"When a tool finishes running, the system will provide the output in a <result> tag. " +
		"Review the output, explain what you did, or proceed to the next step. " +
		"Prefer PowerShell for command examples on Windows."

	if fileCtx := buildChatContext(paths); fileCtx != "" {
		sysPrompt += "\n\nThe user attached the following context:\n" + fileCtx
	}

	useJarvis := false
	var jarvisResponse string
	if useJarvisFromClient && config.JarvisPath != "" {
		if _, err := exec.LookPath(config.JarvisPath); err == nil {
			useJarvis = true
		}
	}

	if useJarvis {
		sendChat(conn, "tool_status", "Querying enterprise LLM via Jarvis...")
		var args []string
		args = append(args, paths...)
		args = append(args, question)

		cmd := exec.CommandContext(ctx, config.JarvisPath, args...)
		cmd.Dir = "."
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf

		err := cmd.Run()
		if err != nil {
			sendChat(conn, "tool_status", fmt.Sprintf("Jarvis error: %v. Stderr: %s. Falling back to local Ollama...", err, errBuf.String()))
			useJarvis = false
		} else {
			jarvisResponse = outBuf.String()
			sendChat(conn, "tool_status", "Jarvis query complete.")
			sendChat(conn, "tool_result", "🧠 Enterprise LLM Response:\n\n"+jarvisResponse)
		}
	}

	activeHistory := append([]map[string]string{}, history...)
	if useJarvis {
		ollamaQuestion := fmt.Sprintf("Here is the response from our enterprise LLM for the user's query:\n\n"+
			"%s\n\n"+
			"Translate this response into a single agentic action using one of the XML-style tags "+
			"(<execute_command>, <write_file path=\"...\">, or <read_file>) if a command, file modification, or file read is suggested. "+
			"If multiple actions are suggested, write ONLY the FIRST action tag. "+
			"Do NOT write any explanation, text, or multiple tags together. Just write the complete tag and stop. "+
			"If no action is suggested, summarize the response in clean natural language.", jarvisResponse)
		activeHistory = append(activeHistory, map[string]string{"role": "user", "content": ollamaQuestion})
	} else {
		activeHistory = append(activeHistory, map[string]string{"role": "user", "content": question})
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		sendChat(conn, "step_start", "")

		var promptBuilder strings.Builder
		promptBuilder.WriteString(sysPrompt)
		promptBuilder.WriteString("\n\n")
		for _, h := range activeHistory {
			role := h["role"]
			content := h["content"]
			switch role {
			case "user":
				fmt.Fprintf(&promptBuilder, "User: %s\n\n", content)
			case "assistant":
				fmt.Fprintf(&promptBuilder, "Assistant: %s\n\n", content)
			}
		}
		promptBuilder.WriteString("Assistant:")
		fullPrompt := promptBuilder.String()

		fmt.Printf("[AI Chat Agent] Connecting to Ollama (model: %s)...\n", model)

		reqBody, _ := json.Marshal(map[string]any{"model": model, "prompt": fullPrompt, "stream": true, "keep_alive": -1})
		req, err := http.NewRequestWithContext(ctx, "POST", host+"/api/generate", bytes.NewBuffer(reqBody))
		if err != nil {
			sendChat(conn, "error", "Failed to create request: "+err.Error())
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := ollamaClient(0).Do(req)
		if err != nil {
			sendChat(conn, "error", "Failed to reach AI at "+host+": "+err.Error())
			return
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			sendChat(conn, "error", fmt.Sprintf("AI error: HTTP %d", resp.StatusCode))
			return
		}

		var fullResponse strings.Builder
		scanner := bufio.NewScanner(resp.Body)

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				resp.Body.Close()
				return
			default:
			}

			line := scanner.Bytes()
			var chunk struct {
				Response string `json:"response"`
				Done     bool   `json:"done"`
			}
			if err := json.Unmarshal(line, &chunk); err == nil {
				if chunk.Response != "" {
					fullResponse.WriteString(chunk.Response)
					sendChat(conn, "delta", chunk.Response)
				}
				if chunk.Done {
					break
				}
			}
		}
		if err := scanner.Err(); err != nil {
			sendChat(conn, "error", "Stream read error: "+err.Error())
			resp.Body.Close()
			return
		}
		resp.Body.Close()

		fullText := fullResponse.String()

		if strings.Contains(fullText, "<execute_command>") && !strings.Contains(fullText, "</execute_command>") {
			fullText += "</execute_command>"
		}
		if strings.Contains(fullText, "<write_file") && !strings.Contains(fullText, "</write_file>") {
			fullText += "</write_file>"
		}
		if strings.Contains(fullText, "<read_file>") && !strings.Contains(fullText, "</read_file>") {
			fullText += "</read_file>"
		}

		if startIdx := strings.Index(fullText, "<execute_command>"); startIdx >= 0 {
			endIdx := strings.Index(fullText, "</execute_command>")
			if endIdx > startIdx {
				cmd := strings.TrimSpace(fullText[startIdx+len("<execute_command>") : endIdx])

				reqPayload, _ := json.Marshal(map[string]string{
					"tool": "execute_command",
					"args": cmd,
				})
				sendChat(conn, "tool_request", string(reqPayload))

				select {
				case status := <-responseChan:
					if status == "approved" {
						sendChat(conn, "tool_status", "Executing command: "+cmd)
						output, err := runCommandTool(cmd)
						var result string
						if err != nil {
							result = fmt.Sprintf("Error running command: %v\nOutput: %s", err, output)
						} else {
							result = output
						}

						sendChat(conn, "tool_result", result)

						activeHistory = append(activeHistory, map[string]string{"role": "assistant", "content": fullText})
						activeHistory = append(activeHistory, map[string]string{"role": "user", "content": fmt.Sprintf("<result>%s</result>", result)})
						continue
					} else {
						activeHistory = append(activeHistory, map[string]string{"role": "assistant", "content": fullText})
						activeHistory = append(activeHistory, map[string]string{"role": "user", "content": "<result>Command execution was rejected by the user.</result>"})
						continue
					}
				case <-ctx.Done():
					return
				}
			}
		}

		if startIdx := strings.Index(fullText, "<write_file"); startIdx >= 0 {
			endIdx := strings.Index(fullText, "</write_file>")
			if endIdx > startIdx {
				tagHeaderEnd := strings.Index(fullText[startIdx:], ">")
				if tagHeaderEnd >= 0 {
					tagHeader := fullText[startIdx : startIdx+tagHeaderEnd]
					path := extractAttr(tagHeader, "path")
					content := fullText[startIdx+tagHeaderEnd+1 : endIdx]

					reqPayload, _ := json.Marshal(map[string]string{
						"tool":    "write_file",
						"args":    path,
						"content": content,
					})
					sendChat(conn, "tool_request", string(reqPayload))

					select {
					case status := <-responseChan:
						if status == "approved" {
							sendChat(conn, "tool_status", "Writing file: "+path)
							err := writeFileTool(path, content)
							var result string
							if err != nil {
								result = fmt.Sprintf("Error writing file: %v", err)
							} else {
								result = fmt.Sprintf("File '%s' successfully written/modified.", path)
							}

							sendChat(conn, "tool_result", result)

							activeHistory = append(activeHistory, map[string]string{"role": "assistant", "content": fullText})
							activeHistory = append(activeHistory, map[string]string{"role": "user", "content": fmt.Sprintf("<result>%s</result>", result)})
							continue
						} else {
							activeHistory = append(activeHistory, map[string]string{"role": "assistant", "content": fullText})
							activeHistory = append(activeHistory, map[string]string{"role": "user", "content": "<result>File writing was rejected by the user.</result>"})
							continue
						}
					case <-ctx.Done():
						return
					}
				}
			}
		}

		if startIdx := strings.Index(fullText, "<read_file>"); startIdx >= 0 {
			endIdx := strings.Index(fullText, "</read_file>")
			if endIdx > startIdx {
				path := strings.TrimSpace(fullText[startIdx+len("<read_file>") : endIdx])

				reqPayload, _ := json.Marshal(map[string]string{
					"tool": "read_file",
					"args": path,
				})
				sendChat(conn, "tool_request", string(reqPayload))

				select {
				case status := <-responseChan:
					if status == "approved" {
						sendChat(conn, "tool_status", "Reading file: "+path)
						content, err := readFileTool(path)
						var result string
						if err != nil {
							result = fmt.Sprintf("Error reading file: %v", err)
						} else {
							result = content
						}

						sendChat(conn, "tool_result", result)

						activeHistory = append(activeHistory, map[string]string{"role": "assistant", "content": fullText})
						activeHistory = append(activeHistory, map[string]string{"role": "user", "content": fmt.Sprintf("<result>%s</result>", result)})
						continue
					} else {
						activeHistory = append(activeHistory, map[string]string{"role": "assistant", "content": fullText})
						activeHistory = append(activeHistory, map[string]string{"role": "user", "content": "<result>File reading was rejected by the user.</result>"})
						continue
					}
				case <-ctx.Done():
					return
				}
			}
		}

		activeHistory = append(activeHistory, map[string]string{"role": "assistant", "content": fullText})

		histBytes, _ := json.Marshal(activeHistory)
		sendChat(conn, "sync_history", string(histBytes))

		sendChat(conn, "done", "")
		fmt.Println("[AI Chat Agent] Finished generation run.")
		return
	}
}

func handleChatPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	htmlBytes, err := webFS.ReadFile("web/chat.html")
	if err != nil {
		http.Error(w, "Chat template not found", 404)
		return
	}
	w.Write(htmlBytes)
}

func handleChatState(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	if session == "" {
		session = "default"
	}

	var safeSession strings.Builder
	for _, char := range session {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			safeSession.WriteRune(char)
		} else if char == ' ' {
			safeSession.WriteRune('_')
		}
	}
	safeStr := safeSession.String()
	if safeStr == "" {
		safeStr = "default"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	path := filepath.Join(home, fmt.Sprintf("invoke_chat_%s.json", safeStr))

	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		data, err := os.ReadFile(path)
		if err != nil {
			_ = json.NewEncoder(w).Encode(ChatState{History: []ChatHistoryEntry{}, Paths: []string{}})
			return
		}
		w.Write(data)
		return
	}

	if r.Method == http.MethodPost {
		var state ChatState
		if json.NewDecoder(r.Body).Decode(&state) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		out, _ := json.MarshalIndent(state, "", "  ")
		_ = os.WriteFile(path, out, 0644)
		w.WriteHeader(200)
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func handleAutoconfigureAI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cmd := exec.Command("docker", "start", "ollama")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	err := cmd.Run()
	if err != nil {
		cmdRun := exec.Command("docker", "run", "-d", "-v", "ollama:/root/.ollama", "-p", "11434:11434", "--name", "ollama", "ollama/ollama")
		cmdRun.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
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
