package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/UserExistsError/conpty"
	"github.com/gorilla/websocket"
)

//go:embed web
var webFS embed.FS

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

var (
	connMu        sync.Mutex
	activeConns   int
	everConnected bool
	serverPort    int
)

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
	return fmt.Sprintf(`"%s" -NoLogo -NoExit -Command "%s"`, shell, initCmd)
}

func atoiDefault(s string, def int) int {
	n := 0
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 {
		return def
	}
	return n
}

func handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	cols := atoiDefault(r.URL.Query().Get("cols"), 80)
	rows := atoiDefault(r.URL.Query().Get("rows"), 24)
	cwd := r.URL.Query().Get("cwd")

	env := append(os.Environ(), fmt.Sprintf("INVOKE_HOST=http://127.0.0.1:%d", serverPort))
	opts := []conpty.ConPtyOption{conpty.ConPtyDimensions(cols, rows), conpty.ConPtyEnv(env)}
	if cwd != "" {
		if _, statErr := os.Stat(cwd); statErr == nil {
			opts = append(opts, conpty.ConPtyWorkDir(cwd))
		}
	}

	cpty, err := conpty.Start(shellCommandLine(), opts...)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("\x1b[31mFailed to start shell: "+err.Error()+"\x1b[0m\r\n"))
		return
	}
	defer cpty.Close()

	connMu.Lock()
	activeConns++
	everConnected = true
	connMu.Unlock()
	defer func() {
		connMu.Lock()
		activeConns--
		connMu.Unlock()
	}()

	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := cpty.Read(buf)
			if n > 0 {
				if conn.WriteMessage(websocket.BinaryMessage, buf[:n]) != nil {
					return
				}
			}
			if readErr != nil {
				conn.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[90m[session ended]\x1b[0m\r\n"))
				conn.Close()
				return
			}
		}
	}()

	for {
		_, data, readErr := conn.ReadMessage()
		if readErr != nil {
			return
		}
		var msg struct {
			T string `json:"t"`
			D string `json:"d"`
			C int    `json:"c"`
			R int    `json:"r"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.T {
		case "i":
			cpty.Write([]byte(msg.D))
		case "r":
			if msg.C > 0 && msg.R > 0 {
				cpty.Resize(msg.C, msg.R)
			}
		}
	}
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
	addr := "127.0.0.1:0"
	if p := os.Getenv("INVOKE_TERM_PORT"); p != "" {
		addr = "127.0.0.1:" + p
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("Error starting terminal server: %v\n", err)
		return
	}
	port := listener.Addr().(*net.TCPAddr).Port
	serverPort = port
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
		w.Write(htmlBytes)
	})
	mux.Handle("/web/", http.FileServer(http.FS(webFS)))
	mux.HandleFunc("/ws", handleTerminalWS)
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


	go func() {
		zero := 0
		for {
			time.Sleep(2 * time.Second)
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
		reqBody, _ := json.Marshal(map[string]interface{}{
			"model":      model,
			"keep_alive": -1,
		})
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Post(host+"/api/generate", "application/json", bytes.NewBuffer(reqBody))
		if err == nil {
			resp.Body.Close()
		}
	}()

	if err := http.Serve(listener, mux); err != nil {
		fmt.Printf("terminal server stopped: %v\n", err)
	}
}

func handleTransparency(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	pct, err := adjustOpacity(dir)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error(), "opacity": pct})
		return
	}

	state := readAppState()
	state.Opacity = pct
	writeAppState(state)

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"opacity": pct})
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		config := loadConfig()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(config)
		return
	}

	if r.Method == http.MethodPost {
		var config ConfigData
		if json.NewDecoder(r.Body).Decode(&config) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		saveConfig(config)
		w.WriteHeader(200)
		return
	}
	http.Error(w, "method not allowed", 405)
}

func handlePrompts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodGet {
		config := loadConfig()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(config.Prompts)
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			Name     string `json:"name"`
			Template string `json:"template"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		addPrompt(req.Name, req.Template)
		w.WriteHeader(200)
		return
	}

	if r.Method == http.MethodDelete {
		var req struct {
			Index int `json:"index"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		deletePrompt(req.Index)
		w.WriteHeader(200)
		return
	}

	http.Error(w, "method not allowed", 405)
}

func handleSnippetAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var snippet Snippet
	if json.NewDecoder(r.Body).Decode(&snippet) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	config := loadConfig()
	config.Snippets = append(config.Snippets, snippet)
	saveConfig(config)
	w.WriteHeader(200)
}

func handleScratchpadPath(w http.ResponseWriter, r *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	path := filepath.Join(home, ".powerterm_scratchpad.txt")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = os.WriteFile(path, []byte(""), 0644)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"path": path})
}

func handleFileSearch(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	q := strings.ToLower(r.URL.Query().Get("q"))

	var files []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		name := info.Name()
		if info.IsDir() {
			if name == ".git" || name == "node_modules" || name == ".gemini" || name == "dist" || name == "build" || name == "bin" || name == "obj" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		relLower := strings.ToLower(rel)
		if q == "" || strings.Contains(relLower, q) {
			files = append(files, rel)
		}
		if len(files) >= 15 {
			return fmt.Errorf("limit reached")
		}
		return nil
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(files)
}

func handleSessionScratchpadPath(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	if session == "" {
		session = "default"
	}

	// Clean session name to be safe for filenames (alphanumeric and underscores only)
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
	filename := fmt.Sprintf("invoke_scratchpad_%s.txt", safeStr)
	path := filepath.Join(home, filename)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = os.WriteFile(path, []byte(""), 0644)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"path": path})
}

type SessionState struct {
	Name           string `json:"name"`
	Cwd            string `json:"cwd"`
	ScratchpadOpen bool   `json:"scratchpad_open"`
	ChatOpen       bool   `json:"chat_open"`
}

type AppState struct {
	LastSessions   []SessionState `json:"last_sessions"`
	RecentSessions []SessionState `json:"recent_sessions"`
	Opacity        int            `json:"opacity"`
}

func getAppStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".powerterm_state.json")
}

func readAppState() AppState {
	statePath := getAppStatePath()
	var state AppState
	data, err := os.ReadFile(statePath)
	if err != nil {
		return AppState{
			LastSessions:   []SessionState{},
			RecentSessions: []SessionState{},
			Opacity:        90,
		}
	}
	if json.Unmarshal(data, &state) != nil {
		return AppState{
			LastSessions:   []SessionState{},
			RecentSessions: []SessionState{},
			Opacity:        90,
		}
	}
	if state.Opacity == 0 {
		state.Opacity = 90
	}
	return state
}

func writeAppState(state AppState) {
	statePath := getAppStatePath()
	out, _ := json.MarshalIndent(state, "", "  ")
	_ = os.WriteFile(statePath, out, 0644)
}

func handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		state := readAppState()
		_ = json.NewEncoder(w).Encode(state)
		return
	}

	if r.Method == http.MethodPost {
		var newState AppState
		if json.NewDecoder(r.Body).Decode(&newState) != nil {
			http.Error(w, "bad request", 400)
			return
		}

		oldState := readAppState()
		oldState.LastSessions = newState.LastSessions

		for _, ns := range newState.LastSessions {
			name := strings.TrimSpace(ns.Name)
			if name == "" || name == "Session 1" || strings.HasPrefix(name, "Session ") {
				continue
			}
			exists := false
			for _, rs := range oldState.RecentSessions {
				if strings.EqualFold(rs.Name, ns.Name) {
					exists = true
					break
				}
			}
			if !exists {
				oldState.RecentSessions = append([]SessionState{ns}, oldState.RecentSessions...)
			}
		}
		if len(oldState.RecentSessions) > 10 {
			oldState.RecentSessions = oldState.RecentSessions[:10]
		}

		writeAppState(oldState)
		w.WriteHeader(200)
		return
	}
	http.Error(w, "method not allowed", 405)
}

type SearchResult struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

func handleSearchInFiles(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]SearchResult{})
		return
	}

	var results []SearchResult
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		name := info.Name()
		if info.IsDir() {
			if name == ".git" || name == "node_modules" || name == ".gemini" || name == "dist" || name == "build" || name == "bin" || name == "obj" {
				return filepath.SkipDir
			}
			return nil
		}

		if info.Size() > 1024*1024 {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		contentStr := string(data)
		if !strings.Contains(strings.ToLower(contentStr), strings.ToLower(q)) {
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}

		lines := strings.Split(contentStr, "\n")
		for idx, line := range lines {
			if strings.Contains(strings.ToLower(line), strings.ToLower(q)) {
				results = append(results, SearchResult{
					File:    rel,
					Line:    idx + 1,
					Content: strings.TrimSpace(line),
				})
				if len(results) >= 50 {
					return fmt.Errorf("limit reached")
				}
			}
		}
		return nil
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func handleRunScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Code == "" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "invalid request payload"})
		return
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	dir := filepath.Join(userHome, ".gemini", "antigravity")
	_ = os.MkdirAll(dir, 0755)

	filePath := filepath.Join(dir, "powerterm_run.ps1")
	err = os.WriteFile(filePath, []byte(req.Code), 0644)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "path": filePath})
}

func handleFreePort(w http.ResponseWriter, r *http.Request) {
	portStr := r.URL.Query().Get("port")
	w.Header().Set("Content-Type", "application/json")
	if portStr == "" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "port parameter required"})
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "invalid port number"})
		return
	}

	pid, err := findPIDHoldingPort(port)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	if pid == 0 {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": fmt.Sprintf("No active process found on port %d.", port)})
		return
	}

	err = killProcess(pid)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": fmt.Sprintf("Failed to release port: %v", err)})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"pid":     pid,
		"message": fmt.Sprintf("Successfully released port %d (terminated process %d).", port, pid),
	})
}

func findPIDHoldingPort(port int) (int, error) {
	cmd := exec.Command("netstat", "-ano")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	targetLocalAddr1 := fmt.Sprintf(":%d", port)
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		localAddr := fields[1]
		if strings.HasSuffix(localAddr, targetLocalAddr1) {
			pidStr := fields[len(fields)-1]
			pid, err := strconv.Atoi(pidStr)
			if err == nil {
				return pid, nil
			}
		}
	}
	return 0, nil
}

func killProcess(pid int) error {
	cmd := exec.Command("taskkill", "/f", "/pid", strconv.Itoa(pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
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
		pullBody, _ := json.Marshal(map[string]interface{}{"name": "phi4-mini"})
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
	configPath := filepath.Join(userHome, ".powerterm.json")
	if data, err := json.MarshalIndent(config, "", "  "); err == nil {
		_ = os.WriteFile(configPath, data, 0644)
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "AI Autoconfigure initiated! Started Ollama Docker container, triggered phi4-mini model pull in background, and saved local config.",
	})
}
