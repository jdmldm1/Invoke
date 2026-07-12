package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/gorilla/websocket"
)

func handleFS(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}

	type ent struct {
		Name string `json:"name"`
		Dir  bool   `json:"dir"`
	}
	out := struct {
		Dir     string `json:"dir"`
		Parent  string `json:"parent"`
		Entries []ent  `json:"entries"`
	}{Dir: abs, Parent: filepath.Dir(abs), Entries: []ent{}}

	entries, err := os.ReadDir(abs)
	if err == nil {
		var dirs, files []ent
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, ent{e.Name(), true})
			} else {
				files = append(files, ent{e.Name(), false})
			}
		}
		sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
		sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
		out.Entries = append(out.Entries, dirs...)
		out.Entries = append(out.Entries, files...)
		if len(out.Entries) > 800 {
			out.Entries = out.Entries[:800]
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
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
			b.WriteString("\n# Directory listing: " + p + "\n")
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
				b.WriteString("  " + name + "\n")
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
			b.WriteString("\n# File: " + p + "\n```\n" + content + "\n```\n")
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

type wsMessage struct {
	Type      string              `json:"type"`
	Question  string              `json:"question"`
	Paths     []string            `json:"paths"`
	History   []map[string]string `json:"history"`
	Status    string              `json:"status"`
	Command   string              `json:"command"`
	UseJarvis bool                `json:"use_jarvis"`
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

	for {
		select {
		case msg, ok := <-msgChan:
			if !ok {
				if currentCancel != nil {
					currentCancel()
				}
				return
			}

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
		promptBuilder.WriteString(sysPrompt + "\n\n")
		for _, h := range activeHistory {
			role := h["role"]
			content := h["content"]
			if role == "user" {
				promptBuilder.WriteString("User: " + content + "\n\n")
			} else if role == "assistant" {
				promptBuilder.WriteString("Assistant: " + content + "\n\n")
			}
		}
		promptBuilder.WriteString("Assistant:")
		fullPrompt := promptBuilder.String()

		fmt.Printf("[AI Chat Agent] Connecting to Ollama (model: %s)...\n", model)

		reqBody, _ := json.Marshal(map[string]interface{}{"model": model, "prompt": fullPrompt, "stream": true, "keep_alive": -1})
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

type ChatHistoryEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatState struct {
	History []ChatHistoryEntry `json:"history"`
	Paths   []string           `json:"paths"`
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
	http.Error(w, "method not allowed", 405)
}
