package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed web
var webFS embed.FS

func serveHTTP(l net.Listener, h http.Handler) {
	if err := http.Serve(l, h); err != nil {
		fmt.Printf("terminal server stopped: %v\n", err)
	}
}

func handleExecBackground(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Cmd string `json:"cmd"`
		Cwd string `json:"cwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", req.Cmd)
	if req.Cwd != "" {
		cmd.Dir = req.Cwd
	}

	output, err := cmd.CombinedOutput()
	result := map[string]any{
		"output": string(output),
		"error":  "",
	}
	if err != nil {
		result["error"] = err.Error()
	}

	json.NewEncoder(w).Encode(result)
}

func openAppWindow(url string) {
	if os.Getenv("INVOKE_NO_WINDOW") != "" {
		return
	}

	icoPath := ""
	if iconBytes, err := webFS.ReadFile("web/favicon.ico"); err == nil {
		p := filepath.Join(os.TempDir(), "invoke_favicon.ico")
		if os.WriteFile(p, iconBytes, 0644) == nil {
			icoPath = p
		}
	}

	candidates := []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			profile := filepath.Join(os.TempDir(), "invoke-app")
			args := []string{
				"--app=" + url,
				"--window-size=1280,820",
				"--user-data-dir=" + profile,
				"--no-first-run",
			}
			if icoPath != "" {
				args = append(args, "--app-icon="+icoPath)
			}
			_ = exec.Command(c, args...).Start()
			return
		}
	}
	openBrowser(url)
}

func serveTerminalWindow() {
	startConfig := loadConfig()
	networkAccessEnabled = startConfig.NetworkAccess

	host := "127.0.0.1"
	if networkAccessEnabled {
		host = "0.0.0.0"
	}
	portStr := "0"
	if startConfig.ServerPort > 0 {
		portStr = fmt.Sprintf("%d", startConfig.ServerPort)
	}
	if p := os.Getenv("INVOKE_TERM_PORT"); p != "" {
		portStr = p
	}
	addr := host + ":" + portStr
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("Error starting terminal server: %v\n", err)
		return
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if startConfig.ServerPort > 0 {
		serverPort = startConfig.ServerPort
	} else {
		serverPort = port
	}
	url := fmt.Sprintf("http://localhost:%d", port)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		htmlBytes, err := webFS.ReadFile("web/terminal_app.html")
		if err != nil {
			http.Error(w, "Template not found", 404)
			return
		}
		htmlStr := string(htmlBytes)
		if runtime.GOOS == "linux" {
			htmlStr = strings.ReplaceAll(htmlStr, "os: 'windows'", "os: 'linux'")
			htmlStr = strings.Replace(htmlStr, "<option value=\"powershell\">PowerShell (.ps1)</option>", "<option value=\"powershell\">PowerShell (.ps1)</option>", 1)
			htmlStr = strings.Replace(htmlStr, "<option value=\"shell\">Bash / Shell (.sh)</option>", "<option value=\"shell\" selected>Bash / Shell (.sh)</option>", 1)
		}
		w.Write([]byte(htmlStr))
	})
	mux.Handle("/web/", http.FileServer(http.FS(webFS)))
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		b, err := webFS.ReadFile("web/manifest.json")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	})
	mux.HandleFunc("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Service-Worker-Allowed", "/")
		b, err := webFS.ReadFile("web/sw.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	})
	mux.HandleFunc("/ws", handleTerminalWS)
	mux.HandleFunc("/ws/view", handleTerminalViewWS)
	mux.HandleFunc("/cast", handleCastPage)
	mux.HandleFunc("/remote-status", handleRemoteStatus)
	mux.HandleFunc("/tunnel/enable", handleTunnelEnable)
	mux.HandleFunc("/tunnel/disable", handleTunnelDisable)
	mux.HandleFunc("/tunnel/status", handleTunnelStatus)
	mux.HandleFunc("/tunnel/route/", handleTunnelProxy)
	mux.HandleFunc("/control", handleControlWS)
	mux.HandleFunc("/open", handleOpenRequest)
	mux.HandleFunc("/diff", handleDiffRoute)
	mux.HandleFunc("/edit", handleEditRoute)
	mux.HandleFunc("/gitview", handleGitOverviewRoute)
	mux.HandleFunc("/free-port", handleFreePort)
	mux.HandleFunc("/converter", handleConverterRoute)
	mux.HandleFunc("/converter/run", handleConverterRun)
	mux.HandleFunc("/autoconfigure-ai", handleAutoconfigureAI)
	mux.HandleFunc("/run-script", handleRunScript)
	mux.HandleFunc("/file-save", handleFileSave)
	mux.HandleFunc("/chat", handleChatPage)
	mux.HandleFunc("/fs", handleFS)
	mux.HandleFunc("/chatws", handleChatWS)
	mux.HandleFunc("/transparency", handleTransparency)
	mux.HandleFunc("/config", handleConfig)
	mux.HandleFunc("/snippet/add", handleSnippetAdd)
	mux.HandleFunc("/scratchpad-path", handleScratchpadPath)
	mux.HandleFunc("/file-search", handleFileSearch)
	mux.HandleFunc("/search-in-files", handleSearchInFiles)
	mux.HandleFunc("/session-scratchpad-path", handleSessionScratchpadPath)
	mux.HandleFunc("/chat/state", handleChatState)
	mux.HandleFunc("/prompts", handlePrompts)
	mux.HandleFunc("/state", handleState)
	mux.HandleFunc("/layout", handleLayout)
	mux.HandleFunc("/git/stage", handleGitStage)
	mux.HandleFunc("/git/unstage", handleGitUnstage)
	mux.HandleFunc("/git/commit", handleGitCommit)
	mux.HandleFunc("/ai-complete", handleAIComplete)
	mux.HandleFunc("/ai-translate", handleAITranslate)
	mux.HandleFunc("/ai-edit-code", handleAIEditCode)
	mux.HandleFunc("/ai-scaffold-script", handleAIScaffoldScript)
	mux.HandleFunc("/ai-explain-hover", handleAIExplainHover)
	mux.HandleFunc("/ports", handlePorts)
	mux.HandleFunc("/ports/kill", handlePortsKill)
	mux.HandleFunc("/ports-html", handlePortsHTML)
	mux.HandleFunc("/sidebar-info", handleSidebarInfo)
	mux.HandleFunc("/history", handleHistory)
	mux.HandleFunc("/git/branches", handleGitBranches)
	mux.HandleFunc("/git/checkout", handleGitCheckout)
	mux.HandleFunc("/git/create-branch", handleGitCreateBranch)
	mux.HandleFunc("/ssh/endpoints", handleSSHEndpoints)
	mux.HandleFunc("/ssh/connect", handleSSHConnect)
	mux.HandleFunc("/scp", handleSCPPage)
	mux.HandleFunc("/fs/tree", handleFSTree)
	mux.HandleFunc("/ai-editor-complete", handleAIEditorComplete)
	mux.HandleFunc("/exec-bg", handleExecBackground)
	mux.HandleFunc("/network-access", handleNetworkAccess)
	mux.HandleFunc("/remote-login", handleRemoteLogin)
	mux.HandleFunc("/ai-cli-schema", handleAICLISchema)
	mux.HandleFunc("/workspace/load", handleWorkspaceLoad)
	mux.HandleFunc("/workspace/save", handleWorkspaceSave)
	mux.HandleFunc("/clipboard/read", handleClipboardRead)

	go func() {
		if os.Getenv("INVOKE_NO_WINDOW") != "" {
			return
		}
		zero := 0
		for {
			time.Sleep(2 * time.Second)
			config := loadConfig()
			if config.KeepAlive {
				continue
			}
			connMu.Lock()
			ended := everConnected && activeConns <= 0
			connMu.Unlock()
			if ended {
				zero++
				if zero >= 2 {
					os.Exit(0)
				}
			} else {
				zero = 0
			}
		}
	}()

	fmt.Printf("Invoke window running at %s\nClose the window to return to the shell.\n", url)
	setAppUserModelID()
	openAppWindow(url)
	go styleInvokeWindow()

	go func() {
		config := loadConfig()
		host := cleanHost(config.OllamaHost)
		model := config.OllamaModel
		reqBody, _ := json.Marshal(map[string]any{
			"model":      model,
			"keep_alive": -1,
		})
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Post(host+"/api/generate", "application/json", bytes.NewBuffer(reqBody))
		if err == nil {
			resp.Body.Close()
		}
	}()

	httpHandler = networkAuthMiddleware(localAuthMiddleware(mux))
	listenerMu.Lock()
	httpListener = listener
	listenerMu.Unlock()
	go serveHTTP(listener, httpHandler)

	select {}
}

var (
	ctrlMu    sync.Mutex
	ctrlConns = map[*websocket.Conn]bool{}
)

func broadcastControl(v any) {
	b, _ := json.Marshal(v)
	ctrlMu.Lock()
	defer ctrlMu.Unlock()
	for c := range ctrlConns {
		_ = c.WriteMessage(websocket.TextMessage, b)
	}
}

func handleControlWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ctrlMu.Lock()
	ctrlConns[conn] = true
	ctrlMu.Unlock()
	for {
		if _, _, e := conn.ReadMessage(); e != nil {
			break
		}
	}
	ctrlMu.Lock()
	delete(ctrlConns, conn)
	ctrlMu.Unlock()
	conn.Close()
}

func handleOpenRequest(w http.ResponseWriter, r *http.Request) {
	var req struct{ Type, File string }
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.File == "" {
		http.Error(w, "bad request", 400)
		return
	}
	abs, err := filepath.Abs(req.File)
	if err != nil {
		abs = req.File
	}
	typ := "diff"
	switch req.Type {
	case "edit":
		typ = "edit"
	case "git":
		typ = "git"
	}
	broadcastControl(map[string]string{
		"action": "openTab", "type": typ, "file": abs, "name": filepath.Base(abs),
	})
	w.WriteHeader(200)
}

func handleDiffRoute(w http.ResponseWriter, r *http.Request) {
	abs, _ := filepath.Abs(r.URL.Query().Get("file"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(monacoDiffHTML(filepath.Base(abs), filepath.Ext(abs), gitFileAtHead(abs), readFileOrEmpty(abs))))
}

func handleEditRoute(w http.ResponseWriter, r *http.Request) {
	abs, _ := filepath.Abs(r.URL.Query().Get("file"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(monacoEditHTML(filepath.Base(abs), filepath.Ext(abs), abs, readFileOrEmpty(abs))))
}

func handleRunScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Code == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "invalid request payload"})
		return
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": err.Error()})
		return
	}

	dir := filepath.Join(userHome, ".gemini", "antigravity")
	_ = os.MkdirAll(dir, 0755)

	filePath := filepath.Join(dir, "invoke_run.ps1")
	err = os.WriteFile(filePath, []byte(req.Code), 0644)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "path": filePath})
}
