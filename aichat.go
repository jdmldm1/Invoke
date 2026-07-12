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
	w.Write([]byte(chatPageHTML))
}

const chatPageHTML = `<!DOCTYPE html><html><head><meta charset="utf-8"><title>AI Chat</title>
<style>
 :root{--bg:#141414;--panel:#1c1c1c;--panel2:#262626;--border:#2b2b2b;--border2:#3a3a3a;
   --text:#e2e2e2;--muted:#8a8a8a;--accent:#9aa6ad;--good:#8fae74;--bad:#c4756e;--ink:#0d0d0d}
 *{box-sizing:border-box}
 html,body{margin:0;height:100%;background:var(--bg);color:var(--text);font-family:'Segoe UI',system-ui,sans-serif}
 #wrap{display:flex;height:100vh}
 #ctx{width:300px;flex:0 0 300px;background:var(--panel);border-right:1px solid var(--border);display:flex;flex-direction:column}
 #ctx h3{margin:0;padding:10px 12px;font-size:12px;color:var(--muted);text-transform:uppercase;letter-spacing:.5px;border-bottom:1px solid var(--border)}
 #crumb{padding:6px 12px;font-size:11px;color:var(--muted);border-bottom:1px solid var(--border);word-break:break-all}
 #browser{flex:1;overflow-y:auto;padding:4px 0}
 .row{display:flex;align-items:center;gap:8px;padding:5px 12px;font-size:13px;cursor:default}
 .row:hover{background:var(--panel2)}
 .row .nm{flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;cursor:pointer}
 .row .dir{color:var(--accent)}
 .row .add{color:var(--muted);cursor:pointer;font-size:15px;padding:0 4px}
 .row .add:hover{color:var(--good)}
 #added{border-top:1px solid var(--border);max-height:40%;overflow-y:auto;padding:6px 0}
 #added .pill{display:flex;align-items:center;gap:6px;padding:4px 12px;font-size:12px;color:var(--good)}
 #added .pill .rm{margin-left:auto;color:var(--muted);cursor:pointer}
 #added .pill .rm:hover{color:var(--bad)}
 #added .empty{padding:8px 12px;color:var(--muted);font-size:12px}
 #main{flex:1;display:flex;flex-direction:column;min-width:0}
 #head{height:40px;flex:0 0 40px;display:flex;align-items:center;gap:8px;padding:0 14px;border-bottom:1px solid var(--border);background:var(--panel)}
 #head b{color:var(--accent)}
  #head .model{margin-left:auto;color:var(--accent);font-size:12px;cursor:pointer;text-decoration:underline dashed;padding:4px 8px;border-radius:4px;transition:all 0.2s}
  #head .model:hover{background:var(--panel2);color:var(--text)}
  #clear-chat:hover{color:var(--text);background:var(--panel2);border-color:var(--accent)}
  #log{flex:1;overflow-y:auto;padding:16px;display:flex;flex-direction:column;gap:14px}
  @keyframes thinking-dots{0%{content:''}25%{content:'.'}50%{content:'..'}75%{content:'...'}100%{content:''}}
  .msg .body.thinking{color:var(--muted);font-style:italic}
  .msg .body.thinking::after{content:'';animation:thinking-dots 1.5s steps(4,end) infinite;display:inline-block;width:12px;text-align:left}
 .msg{max-width:860px}
 .msg .who{font-size:11px;color:var(--muted);margin-bottom:4px;text-transform:uppercase;letter-spacing:.5px}
  .msg .body{line-height:1.5;font-size:14px;background:var(--panel);border:1px solid var(--border);
    border-radius:10px;padding:10px 13px}
  .msg.user .body{background:#1f2937aa;border-color:var(--border2);white-space:pre-wrap}
  .msg.user .who{color:var(--accent)}
  .code-container{background:#0c0f13;border:1px solid var(--border);border-radius:8px;margin:12px 0;overflow:hidden}
  .code-header{display:flex;align-items:center;justify-content:space-between;background:var(--panel2);padding:6px 12px;border-bottom:1px solid var(--border)}
  .code-lang{font-size:11px;color:var(--muted);text-transform:uppercase;font-weight:600;letter-spacing:.5px}
  .code-actions{display:flex;gap:8px}
  .code-btn{background:none;border:1px solid var(--border2);border-radius:4px;color:var(--muted);font-size:11px;padding:3px 8px;cursor:pointer;font-weight:600;transition:all 0.15s}
  .code-btn:hover{color:var(--text);background:var(--panel);border-color:var(--accent)}
  .code-container pre{margin:0;padding:12px;overflow-x:auto}
  .msg .body code{font-family:'Consolas','Courier New',monospace;font-size:13px;background:#0c0f13;padding:2px 4px;border-radius:4px;color:var(--accent)}
  .code-container code{font-family:'Consolas','Courier New',monospace;font-size:13px;color:var(--text);background:none;padding:0}
  .msg .body ul{margin:8px 0;padding-left:20px}
  .msg .body li{margin:4px 0}
  .msg .body strong{color:var(--accent)}
  #inbar{flex:0 0 auto;border-top:1px solid var(--border);padding:10px 12px;display:flex;gap:8px;background:var(--panel)}
  #q{flex:1;resize:none;height:46px;max-height:160px;background:var(--bg);color:var(--text);border:1px solid var(--border2);
    border-radius:8px;padding:9px 11px;font-family:inherit;font-size:14px;outline:none}
  #q:focus{border-color:var(--accent)}
  #send{padding:0 18px;border:0;border-radius:8px;background:var(--accent);color:var(--ink);font-weight:600;cursor:pointer}
  #send:disabled{opacity:.5;cursor:default}
  #chat-suggest{border-top:1px solid var(--border);background:var(--panel);max-height:150px;overflow-y:auto;display:none;padding:4px}
  .chat-suggest-item{padding:6px 12px;cursor:pointer;font-size:13px;border-radius:4px}
  .chat-suggest-item:hover,.chat-suggest-item.active{background:var(--accent);color:var(--ink)}
  #prompt-popup{position:absolute;bottom:64px;left:14px;background:#181818;border:1px solid var(--border2);
    border-radius:8px;box-shadow:0 12px 32px rgba(0,0,0,.6);z-index:100;width:240px;display:none;flex-direction:column;max-height:220px;overflow-y:auto}
  .prompt-popup-item{padding:8px 12px;cursor:pointer;font-size:13px;border-bottom:1px solid var(--border);color:var(--text)}
  .prompt-popup-item:last-child{border-bottom:none}
  .prompt-popup-item:hover{background:var(--panel2);color:var(--accent)}
  </style></head><body>
<div id="wrap">
  <div id="ctx">
    <h3>Context</h3>
    <div id="crumb"></div>
    <div id="browser"></div>
    <h3>Attached</h3>
    <div id="added"></div>
  </div>
  <div id="main">
    <div id="head">
      <b>&#10022; AI Chat</b>
      <span style="color:var(--muted)">&middot; add files/folders as context</span>
      <button id="clear-chat" style="background:none;border:1px solid var(--border2);border-radius:4px;color:var(--muted);font-size:11px;padding:3px 8px;cursor:pointer;margin-left:12px;transition:all 0.15s" title="Clear all message history in this session">Clear History</button>
      <label id="jarvis-toggle" style="display:none;align-items:center;gap:6px;font-size:11px;color:var(--muted);cursor:pointer;user-select:none;margin-left:12px" title="Use enterprise LLM via Jarvis for the initial query">
        <input type="checkbox" id="use-jarvis-chk" style="accent-color:var(--accent);margin:0;cursor:pointer" checked> Use Jarvis
      </label>
      <span class="model" id="model"></span>
    </div>
    <div id="log"></div>
    <div id="chat-suggest"></div>
    <div id="prompt-popup"></div>
    <div id="inbar">
      <button id="prompt-lib-btn" title="Saved AI Prompts Templates" style="background:none;border:1px solid var(--border2);border-radius:6px;color:var(--muted);font-size:16px;padding:0 12px;cursor:pointer;transition:all 0.15s">📚</button>
      <textarea id="q" placeholder="Ask about your code... (Enter to send, Shift+Enter for newline)"></textarea>
      <button id="send">Send</button>
    </div>
  </div>
</div>
<script>
(function(){
  var params=new URLSearchParams(location.search);
  var curDir=params.get('dir')||'';
  var prefill=params.get('q')||'';
  var sessionName=params.get('session')||'global';
  var added=[], history=[], streaming=false;
  var log=document.getElementById('log'), q=document.getElementById('q'), sendBtn=document.getElementById('send');

  function esc(s){ return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); }
  function browse(dir){
    fetch('/fs?dir='+encodeURIComponent(dir||'')).then(function(r){return r.json();}).then(function(d){
      curDir=d.dir;
      document.getElementById('crumb').textContent=d.dir;
      var b=document.getElementById('browser'); b.innerHTML='';
      var up=document.createElement('div'); up.className='row';
      up.innerHTML='<span class="nm dir">.. (up)</span><span class="add" title="Add this folder">+</span>';
      up.querySelector('.nm').onclick=function(){ browse(d.parent); };
      up.querySelector('.add').onclick=function(){ addPath(d.dir); };
      b.appendChild(up);
      d.entries.forEach(function(e){
        var sep = d.dir.indexOf('/')>=0 ? '/' : '\\';
        var full = d.dir.replace(/[\\\/]$/,'') + sep + e.name;
        var row=document.createElement('div'); row.className='row';
        var nm=document.createElement('span'); nm.className='nm'+(e.dir?' dir':''); nm.textContent=e.name+(e.dir?'/':''); nm.title=full;
        var add=document.createElement('span'); add.className='add'; add.textContent='+'; add.title='Add as context';
        if(e.dir) nm.onclick=function(){ browse(full); };
        add.onclick=function(){ addPath(full); };
        row.appendChild(nm); row.appendChild(add); b.appendChild(row);
      });
    });
  }
  function addPath(p){ if(added.indexOf(p)<0){ added.push(p); renderAdded(); saveChatState(); } }
  function removePath(p){ added=added.filter(function(x){return x!==p;}); renderAdded(); saveChatState(); }
  function renderAdded(){
    var a=document.getElementById('added'); a.innerHTML='';
    if(!added.length){ a.innerHTML='<div class="empty">No context yet. Click + next to a file or folder.</div>'; return; }
    added.forEach(function(p){
      var base=p.split(/[\\\/]/).pop()||p;
      var pill=document.createElement('div'); pill.className='pill';
      var nm=document.createElement('span'); nm.textContent=base; nm.title=p; nm.style.overflow='hidden'; nm.style.textOverflow='ellipsis'; nm.style.whiteSpace='nowrap';
      var rm=document.createElement('span'); rm.className='rm'; rm.textContent='×';
      rm.onclick=function(){ removePath(p); };
      pill.appendChild(nm); pill.appendChild(rm); a.appendChild(pill);
    });
  }
  var blockCodes = [];
  window.copyBlockCode = function(btn, index) {
    const code = blockCodes[index];
    if (!code) return;
    navigator.clipboard.writeText(code);
    const old = btn.textContent;
    btn.textContent = 'Copied!';
    setTimeout(function(){ btn.textContent = old; }, 1500);
  };
  window.runBlockCode = function(index) {
    const code = blockCodes[index];
    if (!code) return;
    window.parent.postMessage({action: 'runCode', code: code}, '*');
  };

  function renderMarkdown(text) {
    try {
      if (!text) return '';
      let html = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
      
      let closedText = html;
      const ticks = (html.match(/\x60{3}/g) || []).length;
      if (ticks % 2 !== 0) {
        closedText += '\n' + String.fromCharCode(96, 96, 96);
      }
      
      const codeBlocks = [];
      const blockRegex = new RegExp('\\x60{3}([\\s\\S]*?)\\x60{3}', 'g');
      closedText = closedText.replace(blockRegex, function(match, codePart) {
        const lines = codePart.split('\n');
        let lang = 'code';
        let code = codePart;
        if (lines[0] && !lines[0].includes(' ') && lines[0].length < 15) {
          lang = lines[0].trim();
          code = lines.slice(1).join('\n');
        }
        code = code.trim();
        
        let codeIndex = blockCodes.indexOf(code);
        if (codeIndex < 0) {
          blockCodes.push(code);
          codeIndex = blockCodes.length - 1;
        }
        
        let blockHTML = '<div class="code-container">';
        blockHTML += '  <div class="code-header">';
        blockHTML += '    <span class="code-lang">' + lang + '</span>';
        blockHTML += '    <div class="code-actions">';
        blockHTML += '      <button class="code-btn" onclick="copyBlockCode(this, ' + codeIndex + ')">Copy</button>';
        blockHTML += '      <button class="code-btn" onclick="runBlockCode(' + codeIndex + ')">Run in Term</button>';
        blockHTML += '    </div>';
        blockHTML += '  </div>';
        blockHTML += '  <pre><code class="language-' + lang + '">' + code + '</code></pre>';
        blockHTML += '</div>';
        
        const placeholder = '___CODE_BLOCK_PLACEHOLDER_' + codeBlocks.length + '___';
        codeBlocks.push(blockHTML);
        return placeholder;
      });
      
      const inlineRegex = new RegExp('\\x60([^\\x60]+)\\x60', 'g');
      closedText = closedText.replace(inlineRegex, '<code>$1</code>');
      
      closedText = closedText.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
      closedText = closedText.replace(/\*([^*]+)\*/g, '<em>$1</em>');
      
      closedText = closedText.replace(/^### (.*?)$/gm, '<h3>$1</h3>');
      closedText = closedText.replace(/^## (.*?)$/gm, '<h2>$1</h2>');
      closedText = closedText.replace(/^# (.*?)$/gm, '<h1>$1</h1>');

      const lines = closedText.split('\n');
      let inList = false;
      for (let i = 0; i < lines.length; i++) {
        const line = lines[i].trim();
        if (line.startsWith('- ') || line.startsWith('* ')) {
          const content = line.substring(2);
          if (!inList) {
            lines[i] = '<ul><li>' + content + '</li>';
            inList = true;
          } else {
            lines[i] = '<li>' + content + '</li>';
          }
        } else {
          if (inList) {
            lines[i-1] += '</ul>';
            inList = false;
          }
        }
      }
      if (inList) lines[lines.length - 1] += '</ul>';
      closedText = lines.join('\n');
      closedText = closedText.replace(/\n/g, '<br>');
      
      for (let i = 0; i < codeBlocks.length; i++) {
        closedText = closedText.replace('___CODE_BLOCK_PLACEHOLDER_' + i + '___', () => codeBlocks[i]);
      }
      return closedText;
    } catch (err) {
      console.error("renderMarkdown error:", err);
      return text + '<div style="color:red;font-weight:bold;margin-top:10px">Render Error: ' + err.message + '</div>';
    }
  }

  function addMsg(role, text){
    var m=document.createElement('div'); m.className='msg '+role;
    var who=document.createElement('div'); who.className='who'; who.textContent=role==='user'?'You':'Assistant';
    var body=document.createElement('div'); body.className='body';
    if(role==='assistant'){
      body.innerHTML=renderMarkdown(text);
    } else {
      body.textContent=text;
    }
    m.appendChild(who); m.appendChild(body); log.appendChild(m); log.scrollTop=log.scrollHeight;
    return body;
  }

  var ws, curBody=null, curResponseText='';
  function saveChatState(){
    if(!sessionName) return;
    fetch('/chat/state?session='+encodeURIComponent(sessionName), {
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({history:history, paths:added})
    });
  }
  function connect(){
    ws=new WebSocket((location.protocol==='https:'?'wss':'ws')+'://'+location.host+'/chatws');
    ws.onmessage=function(e){
      try {
        var m; try{ m=JSON.parse(e.data); }catch(_){ return; }
        if(m.type==='step_start'){
          if(curBody && curBody.classList.contains('thinking')){
            curResponseText='';
          } else {
            curBody=addMsg('assistant','Thinking...');
            curBody.classList.add('thinking');
            curResponseText='';
          }
        }
        else if(m.type==='delta'){
          if(curBody){
            if(curBody.classList.contains('thinking')){
              curBody.textContent='';
              curBody.classList.remove('thinking');
            }
            curResponseText+=m.text;
            
            var displayText = curResponseText
              .replace(/<execute_command>[^]*?<\/execute_command>/g, '')
              .replace(/<write_file[^]*?>[^]*?<\/write_file>/g, '')
              .replace(/<read_file>[^]*?<\/read_file>/g, '');
              
            curBody.innerHTML = renderMarkdown(displayText);
            log.scrollTop=log.scrollHeight;
          }
        }
        else if(m.type==='tool_request'){
          var req; try{ req=JSON.parse(m.text); }catch(_){ req=m; }
          q.disabled = true;
          sendBtn.disabled = true;
          
          var card = document.createElement('div');
          card.className = 'tool-card';
          card.style.cssText = 'border:1px solid var(--border2);border-radius:6px;padding:10px;margin-top:10px;background:var(--panel2)';
          
          var title = document.createElement('div');
          title.style.cssText = 'font-weight:600;font-size:12px;color:var(--accent);margin-bottom:6px';
          title.textContent = '⚙️ AI Requests Permission';
          card.appendChild(title);
          
          var details = document.createElement('div');
          details.style.cssText = 'font-family:monospace;font-size:11px;background:var(--bg);padding:6px;border-radius:4px;color:var(--text);margin-bottom:8px;word-break:break-all;white-space:pre-wrap';
          
          var label = '';
          if (req.tool === 'execute_command') label = 'Run Command: ' + req.args;
          else if (req.tool === 'write_file') label = 'Write File: ' + req.args + '\n\n' + (req.content.length > 500 ? req.content.substring(0, 500) + '...' : req.content);
          else if (req.tool === 'read_file') label = 'Read File: ' + req.args;
          details.textContent = label;
          card.appendChild(details);
          
          var actions = document.createElement('div');
          actions.style.cssText = 'display:flex;gap:8px';
          
          var approve = document.createElement('button');
          approve.textContent = 'Approve';
          approve.style.cssText = 'background:var(--good);color:var(--ink);border:none;border-radius:4px;padding:4px 10px;font-size:11px;font-weight:600;cursor:pointer';
          
          var reject = document.createElement('button');
          reject.textContent = 'Reject';
          reject.style.cssText = 'background:var(--bad);color:#fff;border:none;border-radius:4px;padding:4px 10px;font-size:11px;font-weight:600;cursor:pointer';
          
          var statusTxt = document.createElement('div');
          statusTxt.style.cssText = 'font-size:11px;color:var(--muted);margin-top:6px;display:none';
          
          approve.onclick = function() {
            approve.disabled = true;
            reject.disabled = true;
            statusTxt.textContent = 'Approved. Running...';
            statusTxt.style.display = 'block';
            ws.send(JSON.stringify({type: 'tool_response', status: 'approved'}));
          };
          
          reject.onclick = function() {
            approve.disabled = true;
            reject.disabled = true;
            statusTxt.textContent = 'Rejected by user.';
            statusTxt.style.display = 'block';
            ws.send(JSON.stringify({type: 'tool_response', status: 'rejected'}));
            q.disabled = false;
            sendBtn.disabled = false;
          };
          
          actions.appendChild(approve);
          actions.appendChild(reject);
          card.appendChild(actions);
          card.appendChild(statusTxt);
          
          if(curBody) {
            if(curBody.classList.contains('thinking')){
              curBody.textContent='';
              curBody.classList.remove('thinking');
            }
            curBody.appendChild(card);
            log.scrollTop=log.scrollHeight;
          }
        }
        else if(m.type==='tool_status'){
          var cards = document.querySelectorAll('.tool-card');
          if (cards.length > 0) {
            var lastCard = cards[cards.length - 1];
            var statusTxt = lastCard.querySelector('div:last-child');
            if (statusTxt) {
              statusTxt.textContent = m.text;
              statusTxt.style.display = 'block';
            }
          }
        }
        else if(m.type==='tool_result'){
          var resultBox = document.createElement('div');
          resultBox.className = 'system-msg';
          resultBox.style.cssText = 'background:#1a1a1a;border-left:3px solid var(--accent);padding:8px 12px;margin:8px 0;font-size:12px;border-radius:0 4px 4px 0';
          
          var title = document.createElement('div');
          title.style.cssText = 'font-weight:600;color:var(--accent);margin-bottom:4px';
          title.textContent = '💻 Execution Result';
          resultBox.appendChild(title);
          
          var pre = document.createElement('pre');
          pre.style.cssText = 'margin:0;font-family:monospace;white-space:pre-wrap;word-break:break-all;color:var(--text)';
          
          var outText = m.text;
          if (outText.length > 2000) {
            outText = outText.substring(0, 2000) + '\n\n...(output truncated for display)...';
          }
          pre.textContent = outText;
          resultBox.appendChild(pre);
          
          log.appendChild(resultBox);
          log.scrollTop=log.scrollHeight;
        }
        else if(m.type==='sync_history'){
          try {
            history = JSON.parse(m.text);
            saveChatState();
          } catch(e) {}
        }
        else if(m.type==='done'){
          curBody=null; curResponseText=''; streaming=false;
          q.disabled = false;
          sendBtn.disabled = false;
          q.focus();
        }
        else if(m.type==='error'){
          if(curBody){
            if(curBody.classList.contains('thinking')){
              curBody.textContent='';
              curBody.classList.remove('thinking');
            }
            curResponseText+='\n['+m.text+']';
            curBody.innerHTML = renderMarkdown(curResponseText);
          } else {
            addMsg('assistant','['+m.text+']');
          }
          curBody=null; curResponseText=''; streaming=false;
          q.disabled = false;
          sendBtn.disabled = false;
        }
      } catch (err) {
        console.error("WS Message Error:", err);
        if (curBody) {
          curBody.innerHTML += '<div style="color:red;font-weight:bold;margin-top:10px">JS Error: ' + err.message + '<br>' + err.stack.replace(/\n/g, '<br>') + '</div>';
        }
        streaming=false;
        q.disabled = false;
        sendBtn.disabled = false;
      }
    };
    ws.onclose=function(){ setTimeout(connect,1500); };
  }
  function send(){
    var text=q.value.trim(); if(!text||streaming) return;
    
    if (text.startsWith('/')) {
      var parts = text.split(' ');
      var cmd = parts[0].toLowerCase();
      var arg = parts.slice(1).join(' ').trim();
      
      if (cmd === '/clear') {
        document.getElementById('log').innerHTML = '';
        history = [];
        q.value = '';
        saveChatState();
        addMsg('assistant', 'Chat history cleared.');
        return;
      }
      else if (cmd === '/add') {
        if (arg) {
          addPath(arg);
          addMsg('user', text);
          addMsg('assistant', 'Added context file/folder: ' + arg);
        } else {
          addMsg('assistant', 'Usage: /add <file_path>');
        }
        q.value = '';
        return;
      }
      else if (cmd === '/remove') {
        if (arg) {
          removePath(arg);
          addMsg('user', text);
          addMsg('assistant', 'Removed context file/folder: ' + arg);
        } else {
          addMsg('assistant', 'Usage: /remove <file_path>');
        }
        q.value = '';
        return;
      }
      else if (cmd === '/git') {
        text = '/run git status';
      }
      else if (cmd === '/ports') {
        text = '/run pt ports';
      }
      else if (cmd === '/help') {
        addMsg('user', text);
        var helpText = '**Available Slash Commands:**\n\n' +
          '* <code>/run &lt;command&gt;</code> : Execute a terminal command with your permission.\n' +
          '* <code>/git</code> : Run git status to see changed files.\n' +
          '* <code>/ports</code> : List listening network ports and owning processes.\n' +
          '* <code>/add &lt;file&gt;</code> : Add a file/folder to active context.\n' +
          '* <code>/remove &lt;file&gt;</code> : Remove a file/folder from context.\n' +
          '* <code>/clear</code> : Clear conversation history log.\n' +
          '* <code>/help</code> : List these commands.';
        var body = addMsg('assistant', '');
        body.innerHTML = renderMarkdown(helpText);
        q.value = '';
        log.scrollTop = log.scrollHeight;
        return;
      }
    }

    if(!ws||ws.readyState!==1){ addMsg('assistant','[connecting... try again in a moment]'); return; }
    
    if (text.startsWith('/run ')) {
      var cmdToRun = text.substring(5).trim();
      addMsg('user', text);
      q.value=''; streaming=true; sendBtn.disabled=true;
      
      curBody=addMsg('assistant','Thinking...');
      curBody.classList.add('thinking');
      
      ws.send(JSON.stringify({type: 'run_command', command: cmdToRun}));
      return;
    }

    addMsg('user',text); history.push({role:'user',content:text});
    saveChatState();
    q.value=''; streaming=true; sendBtn.disabled=true;
    curBody=addMsg('assistant','Thinking...');
    curBody.classList.add('thinking');
    var useJarvis = false;
    var chk = document.getElementById('use-jarvis-chk');
    if (chk) useJarvis = chk.checked;
    ws.send(JSON.stringify({question:text, paths:added, history:history.slice(0,-1), use_jarvis:useJarvis}));
  }
  sendBtn.onclick=send;

  const chatCommands = [
    { cmd: "/run", desc: "Run a shell command locally (requires permission)" },
    { cmd: "/git", desc: "Check git status of the workspace" },
    { cmd: "/ports", desc: "List all listening ports and details" },
    { cmd: "/add", desc: "Add a file/folder to attached context list" },
    { cmd: "/remove", desc: "Remove a file/folder from attached context" },
    { cmd: "/clear", desc: "Clear all message history in this chat window" },
    { cmd: "/explain", desc: "Explain the attached code files in detail" },
    { cmd: "/refactor", desc: "Refactor code for better quality/performance" },
    { cmd: "/test", desc: "Generate comprehensive unit tests" },
    { cmd: "/translate", desc: "Translate code between Linux and Windows" },
    { cmd: "/freeport", desc: "Free a locked port on your machine" },
    { cmd: "/help", desc: "List all available slash commands" }
  ];
  let chatSuggestIdx = 0;
  let matchingChatSuggests = [];
  let chatSuggestMode = '';
  let chatSuggestQuery = '';

  function showChatSuggests() {
    const el = document.getElementById('chat-suggest');
    if (!el || matchingChatSuggests.length === 0) {
      hideChatSuggest();
      return;
    }
    el.innerHTML = '';
    matchingChatSuggests.forEach((item, idx) => {
      const div = document.createElement('div');
      div.className = 'chat-suggest-item' + (idx === chatSuggestIdx ? ' active' : '');
      div.innerHTML = '<strong>' + item.cmd + '</strong> <span style="color:var(--muted); font-size:11px; margin-left:8px;">' + item.desc + '</span>';
      div.onclick = function() {
        completeChatSuggest(item.cmd);
      };
      el.appendChild(div);
    });
    el.style.display = 'block';
  }

  function hideChatSuggest() {
    const el = document.getElementById('chat-suggest');
    if (el) el.style.display = 'none';
    chatSuggestIdx = 0;
  }

  function completeChatSuggest(val) {
    const text = q.value;
    const cursor = q.selectionStart;
    const textBefore = text.substring(0, cursor);
    const textAfter = text.substring(cursor);
    
    const lastIdx = chatSuggestMode === '/' ? textBefore.lastIndexOf('/') : textBefore.lastIndexOf('@');
    if (lastIdx >= 0) {
      if (chatSuggestMode === '@') {
        const cleanPath = val.substring(1);
        addPath(cleanPath);
        q.value = textBefore.substring(0, lastIdx) + textAfter;
        q.setSelectionRange(lastIdx, lastIdx);
      } else {
        q.value = textBefore.substring(0, lastIdx) + val + ' ' + textAfter;
        q.focus();
        const newCursor = lastIdx + val.length + 1;
        q.setSelectionRange(newCursor, newCursor);
      }
    }
    hideChatSuggest();
  }

  q.addEventListener('input', function() {
    const val = q.value;
    const cursor = q.selectionStart;
    const textBefore = val.substring(0, cursor);
    const lastSlash = textBefore.lastIndexOf('/');
    const lastAt = textBefore.lastIndexOf('@');
    
    if (lastSlash >= 0 && lastSlash >= lastAt && textBefore.substring(lastSlash).indexOf(' ') < 0) {
      chatSuggestMode = '/';
      chatSuggestQuery = textBefore.substring(lastSlash);
      matchingChatSuggests = chatCommands.filter(c => c.cmd.startsWith(chatSuggestQuery));
      showChatSuggests();
    } else if (lastAt >= 0 && lastAt >= lastSlash && textBefore.substring(lastAt).indexOf(' ') < 0) {
      chatSuggestMode = '@';
      chatSuggestQuery = textBefore.substring(lastAt + 1);
      const dir = crumb.textContent || '';
      fetch('/file-search?q=' + encodeURIComponent(chatSuggestQuery) + '&dir=' + encodeURIComponent(dir))
        .then(r => r.json())
        .then(files => {
          matchingChatSuggests = (files || []).slice(0, 10).map(f => ({ cmd: '@' + f, desc: f }));
          showChatSuggests();
        });
    } else {
      hideChatSuggest();
    }
  });

  let historyIndex = -1;
  let tempInput = '';

  q.addEventListener('keydown', function(e) {
    const el = document.getElementById('chat-suggest');
    const suggestVisible = el && el.style.display === 'block';
    
    if (suggestVisible) {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        chatSuggestIdx = (chatSuggestIdx + 1) % matchingChatSuggests.length;
        showChatSuggests();
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        chatSuggestIdx = (chatSuggestIdx - 1 + matchingChatSuggests.length) % matchingChatSuggests.length;
        showChatSuggests();
      } else if (e.key === 'Enter' || e.key === 'Tab') {
        e.preventDefault();
        completeChatSuggest(matchingChatSuggests[chatSuggestIdx].cmd);
      } else if (e.key === 'Escape') {
        e.preventDefault();
        hideChatSuggest();
      }
    } else {
      const userQueries = history.filter(h => h.role === 'user').map(h => h.content);
      if (e.key === 'ArrowUp') {
        if (q.selectionStart === 0) {
          if (historyIndex === -1) tempInput = q.value;
          if (historyIndex < userQueries.length - 1) {
            historyIndex++;
            q.value = userQueries[userQueries.length - 1 - historyIndex];
            e.preventDefault();
          }
        }
      } else if (e.key === 'ArrowDown') {
        if (q.selectionStart === q.value.length) {
          if (historyIndex > 0) {
            historyIndex--;
            q.value = userQueries[userQueries.length - 1 - historyIndex];
            e.preventDefault();
          } else if (historyIndex === 0) {
            historyIndex = -1;
            q.value = tempInput;
            e.preventDefault();
          }
        }
      } else if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        historyIndex = -1;
        send();
      }
    }
  });

  document.addEventListener('click', hideChatSuggest);

  document.getElementById('clear-chat').onclick = function() {
    if(confirm('Clear all chat history for this session?')) {
      history = [];
      log.innerHTML = '';
      saveChatState();
    }
  };

  const promptPopup = document.getElementById('prompt-popup');
  const promptBtn = document.getElementById('prompt-lib-btn');

  function hidePromptPopup() {
    promptPopup.style.display = 'none';
  }

  promptBtn.onclick = function(e) {
    e.stopPropagation();
    if (promptPopup.style.display === 'flex') {
      hidePromptPopup();
      return;
    }
    
    fetch('/prompts')
      .then(r => r.json())
      .then(prompts => {
        promptPopup.innerHTML = '';
        if (!prompts || prompts.length === 0) {
          promptPopup.innerHTML = '<div style="padding:10px;font-size:12px;color:var(--muted);text-align:center">No prompts saved.</div>';
        } else {
          prompts.forEach(p => {
            const item = document.createElement('div');
            item.className = 'prompt-popup-item';
            item.textContent = p.name;
            item.title = p.template;
            item.onclick = function() {
              hidePromptPopup();
              let tmpl = p.template;
              const regex = /\{([a-zA-Z0-9_]+)\}/g;
              let match;
              const vars = [];
              while ((match = regex.exec(tmpl)) !== null) {
                if (!vars.includes(match[1])) {
                  vars.push(match[1]);
                }
              }

              let cancelled = false;
              vars.forEach(v => {
                if (cancelled) return;
                const val = prompt('Enter value for {' + v + '}:');
                if (val === null) {
                  cancelled = true;
                  return;
                }
                tmpl = tmpl.replaceAll('{' + v + '}', val);
              });

              if (!cancelled) {
                q.value = tmpl;
                q.focus();
              }
            };
            promptPopup.appendChild(item);
          });
        }
        promptPopup.style.display = 'flex';
      });
  };

  document.addEventListener('click', function(e) {
    if (promptPopup.style.display === 'flex' && !promptPopup.contains(e.target) && e.target !== promptBtn) {
      hidePromptPopup();
    }
  });

  renderAdded(); browse(curDir); connect();
  q.focus();

  if(sessionName){
    fetch('/chat/state?session='+encodeURIComponent(sessionName))
      .then(function(r){return r.json();})
      .then(function(st){
        added=st.paths||[];
        renderAdded();
        history=st.history||[];
        log.innerHTML='';
        history.forEach(function(h){
          addMsg(h.role, h.content);
        });
        
        if(prefill){
          q.value=prefill;
          setTimeout(function(){ if(ws&&ws.readyState===1) send(); else setTimeout(send,800); }, 100);
          prefill = '';
        }
      });
  } else {
    if(prefill){
      q.value=prefill;
      setTimeout(function(){ if(ws&&ws.readyState===1) send(); else setTimeout(send,800); }, 400);
      prefill = '';
    }
  }

  fetch('/config')
    .then(r => r.json())
    .then(cfg => {
      document.getElementById('model').textContent = (cfg.ollama_model || 'gemma4:e4b') + ' @ ' + (cfg.ollama_host || 'http://shiloh:11434') + (cfg.jarvis_path ? ' (+Jarvis)' : '');
      if (cfg.jarvis_path) {
        document.getElementById('jarvis-toggle').style.display = 'flex';
      }
      document.getElementById('model').onclick = function() {
        const newHost = prompt('Ollama Host:', cfg.ollama_host || 'http://shiloh:11434');
        if (newHost === null) return;
        const newModel = prompt('Ollama Model:', cfg.ollama_model || 'gemma4:e4b');
        if (newModel === null) return;
        const newJarvis = prompt('Jarvis Path (optional enterprise LLM CLI tool):', cfg.jarvis_path || '');
        if (newJarvis === null) return;
        
        fetch('/config', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            ollama_host: newHost.trim(),
            ollama_model: newModel.trim(),
            jarvis_path: newJarvis.trim()
          })
        }).then(r => {
          if (r.ok) {
            location.reload();
          }
        });
      };
    });
})();
</script></body></html>`

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
