package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

type ViewerInfo struct {
	RemoteAddr  string    `json:"remoteAddr"`
	ConnectedAt time.Time `json:"connectedAt"`
	UserAgent   string    `json:"userAgent"`
}

type ActiveSession struct {
	ID         string
	Cwd        string
	Clients    map[*websocket.Conn]*ViewerInfo
	ClientMu   sync.Mutex
	History    []byte
	HistoryMu  sync.Mutex
	Cols, Rows int
	SizeMu     sync.Mutex
	Cpty       *conpty.ConPty
	DataChan   chan []byte
	StopChan   chan struct{}
}

var (
	activeSessions   = make(map[string]*ActiveSession)
	activeSessionsMu sync.Mutex
)

func (s *ActiveSession) startCoalescing() {
	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()
	var buffer []byte
	for {
		select {
		case data, ok := <-s.DataChan:
			if !ok {
				return
			}
			buffer = append(buffer, data...)
		case <-ticker.C:
			if len(buffer) > 0 {
				s.ClientMu.Lock()
				for conn := range s.Clients {
					_ = conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
					err := conn.WriteMessage(websocket.BinaryMessage, buffer)
					if err != nil {
						conn.Close()
						delete(s.Clients, conn)
					}
				}
				s.ClientMu.Unlock()
				buffer = nil
			}
		case <-s.StopChan:
			return
		}
	}
}

var (
	connMu        sync.Mutex
	activeConns   int
	everConnected bool
	serverPort    int
	ctrlMu        sync.Mutex
	ctrlConns     = map[*websocket.Conn]bool{}
)

const (
	sessionCookieName = "invoke_session"
	sessionTTL        = 24 * time.Hour
)

var (
	listenerMu           sync.Mutex
	httpListener         net.Listener
	httpHandler          http.Handler
	networkAccessEnabled bool

	sessionMu sync.Mutex
	sessions  = map[string]time.Time{}
)

func hashPassword(password, salt string) string {
	sum := sha256.Sum256([]byte(salt + ":" + password))
	return hex.EncodeToString(sum[:])
}

func generateSalt() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func checkPassword(password, salt, wantHash string) bool {
	if wantHash == "" {
		return false
	}
	got := hashPassword(password, salt)
	return subtle.ConstantTimeCompare([]byte(got), []byte(wantHash)) == 1
}

func newSessionToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func createSession() string {
	tok := newSessionToken()
	sessionMu.Lock()
	sessions[tok] = time.Now().Add(sessionTTL)
	sessionMu.Unlock()
	return tok
}

func validSession(token string) bool {
	if token == "" {
		return false
	}
	sessionMu.Lock()
	defer sessionMu.Unlock()
	exp, ok := sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(sessions, token)
		return false
	}
	return true
}

func isLoopbackAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func localLANAddresses() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil {
			continue
		}
		out = append(out, ip4.String())
	}
	return out
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func serveRemoteLoginPage(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	errHTML := ""
	if errMsg != "" {
		errHTML = `<div style="color:#c4756e;margin-bottom:12px;font-size:13px">` + html.EscapeString(errMsg) + `</div>`
	}
	fmt.Fprintf(w, `<!doctype html><html><head><title>Invoke - Remote Access</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
body{background:#141414;color:#e2e2e2;font-family:Segoe UI,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
form{background:#1c1c1c;padding:32px;border-radius:8px;min-width:280px;border:1px solid #2a2a2a}
h1{font-size:16px;margin:0 0 16px;font-weight:600}
input{width:100%%;padding:9px;margin-bottom:12px;background:#141414;border:1px solid #333;color:#e2e2e2;border-radius:4px;box-sizing:border-box;font-size:14px}
button{width:100%%;padding:9px;background:#0ea5e9;border:none;color:#fff;border-radius:4px;cursor:pointer;font-size:14px}
</style></head><body>
<form method="POST" action="/remote-login">
<h1>Invoke &mdash; Remote Access</h1>
%s
<input type="password" name="password" placeholder="Password" autofocus>
<button type="submit">Unlock</button>
</form></body></html>`, errHTML)
}

func handleRemoteLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		serveRemoteLoginPage(w, "")
		return
	}
	if err := r.ParseForm(); err != nil {
		serveRemoteLoginPage(w, "Bad request")
		return
	}
	password := r.FormValue("password")
	config := loadConfig()
	if !checkPassword(password, config.NetworkPasswordSalt, config.NetworkPasswordHash) {
		serveRemoteLoginPage(w, "Incorrect password")
		return
	}
	token := createSession()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func networkAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !networkAccessEnabled || isLoopbackAddr(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/web/xterm.min.js" || r.URL.Path == "/web/xterm.min.css" || r.URL.Path == "/web/xterm-addon-fit.min.js" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/cast" || r.URL.Path == "/ws/view" || strings.HasPrefix(r.URL.Path, "/tunnel/route/") {
			sessionID := r.URL.Query().Get("session")
			if sessionID != "" {
				activeSessionsMu.Lock()
				_, exists := activeSessions[sessionID]
				activeSessionsMu.Unlock()
				if exists {
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		if r.URL.Path == "/remote-login" {
			handleRemoteLogin(w, r)
			return
		}
		if cookie, err := r.Cookie(sessionCookieName); err == nil && validSession(cookie.Value) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			serveRemoteLoginPage(w, "")
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func handleNetworkAccess(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config := loadConfig()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"enabled":     config.NetworkAccess,
			"hasPassword": config.NetworkPasswordHash != "",
			"port":        serverPort,
			"addresses":   localLANAddresses(),
		})

	case http.MethodPost:
		var req struct {
			Enabled  bool   `json:"enabled"`
			Password string `json:"password"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeJSONError(w, 400, "bad request")
			return
		}
		config := loadConfig()
		if req.Enabled {
			if req.Password != "" {
				config.NetworkPasswordSalt = generateSalt()
				config.NetworkPasswordHash = hashPassword(req.Password, config.NetworkPasswordSalt)
			}
			if config.NetworkPasswordHash == "" {
				writeJSONError(w, 400, "a password is required to enable network access")
				return
			}
		}
		config.NetworkAccess = req.Enabled
		saveConfig(config)
		if err := rebindNetworkAccess(req.Enabled); err != nil {
			writeJSONError(w, 500, "failed to rebind server: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"enabled":   config.NetworkAccess,
			"port":      serverPort,
			"addresses": localLANAddresses(),
		})

	default:
		writeJSONError(w, 405, "method not allowed")
	}
}

func rebindNetworkAccess(enabled bool) error {
	listenerMu.Lock()
	defer listenerMu.Unlock()

	host := "127.0.0.1"
	if enabled {
		host = "0.0.0.0"
	}
	addr := fmt.Sprintf("%s:%d", host, serverPort)

	if httpListener != nil {
		httpListener.Close()
		httpListener = nil
	}
	newListener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	httpListener = newListener
	networkAccessEnabled = enabled
	go serveHTTP(newListener, httpHandler)
	return nil
}

func serveHTTP(l net.Listener, h http.Handler) {
	if err := http.Serve(l, h); err != nil {
		fmt.Printf("terminal server stopped: %v\n", err)
	}
}

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
	sessionID := r.URL.Query().Get("session")

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

	var session *ActiveSession
	if sessionID != "" {
		session = &ActiveSession{
			ID:       sessionID,
			Cwd:      cwd,
			Clients:  make(map[*websocket.Conn]*ViewerInfo),
			Cols:     cols,
			Rows:     rows,
			Cpty:     cpty,
			DataChan: make(chan []byte, 100),
			StopChan: make(chan struct{}),
		}
		go session.startCoalescing()
		activeSessionsMu.Lock()
		activeSessions[sessionID] = session
		activeSessionsMu.Unlock()
		defer func() {
			close(session.StopChan)
			activeSessionsMu.Lock()
			delete(activeSessions, sessionID)
			activeSessionsMu.Unlock()
		}()
	}

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
				data := buf[:n]
				if conn.WriteMessage(websocket.BinaryMessage, data) != nil {
					return
				}
				if session != nil {
					session.HistoryMu.Lock()
					session.History = append(session.History, data...)
					if len(session.History) > 100000 {
						session.History = session.History[len(session.History)-100000:]
					}
					session.HistoryMu.Unlock()

					select {
					case session.DataChan <- data:
					default:
					}
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
				if session != nil {
					session.SizeMu.Lock()
					session.Cols = msg.C
					session.Rows = msg.R
					session.SizeMu.Unlock()

					session.ClientMu.Lock()
					sizeMsg := []byte(fmt.Sprintf("size:%d,%d", msg.C, msg.R))
					for viewer := range session.Clients {
						_ = viewer.WriteMessage(websocket.TextMessage, sizeMsg)
					}
					session.ClientMu.Unlock()
				}
			}
		}
	}
}

func handleCastPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	htmlBytes, err := webFS.ReadFile("web/cast.html")
	if err != nil {
		http.Error(w, "Cast template not found", 404)
		return
	}
	w.Write(htmlBytes)
}

func handleTerminalViewWS(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		http.Error(w, "Missing session ID", 400)
		return
	}

	activeSessionsMu.Lock()
	session, exists := activeSessions[sessionID]
	activeSessionsMu.Unlock()

	if !exists {
		http.Error(w, "Session not found", 404)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	info := &ViewerInfo{
		RemoteAddr:  r.RemoteAddr,
		ConnectedAt: time.Now(),
		UserAgent:   r.Header.Get("User-Agent"),
	}
	session.ClientMu.Lock()
	session.Clients[conn] = info
	session.ClientMu.Unlock()

	defer func() {
		session.ClientMu.Lock()
		delete(session.Clients, conn)
		session.ClientMu.Unlock()
	}()

	session.SizeMu.Lock()
	c, rows := session.Cols, session.Rows
	session.SizeMu.Unlock()

	sizeMsg := []byte(fmt.Sprintf("size:%d,%d", c, rows))
	if err := conn.WriteMessage(websocket.TextMessage, sizeMsg); err != nil {
		return
	}

	session.HistoryMu.Lock()
	history := make([]byte, len(session.History))
	copy(history, session.History)
	session.HistoryMu.Unlock()

	if len(history) > 0 {
		if err := conn.WriteMessage(websocket.BinaryMessage, history); err != nil {
			return
		}
	}

	if session.Cpty != nil {
		_, _ = session.Cpty.Write([]byte{12})
	}

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			return
		}
	}
}

func handleRemoteStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		activeSessionsMu.Lock()

		type ViewerData struct {
			RemoteAddr  string `json:"remoteAddr"`
			ConnectedAt string `json:"connectedAt"`
			UserAgent   string `json:"userAgent"`
		}
		type CastingStatus struct {
			Count   int          `json:"count"`
			Viewers []ViewerData `json:"viewers"`
		}

		casting := make(map[string]CastingStatus)
		for id, session := range activeSessions {
			session.ClientMu.Lock()
			count := len(session.Clients)
			var viewers []ViewerData
			for _, info := range session.Clients {
				if info != nil {
					viewers = append(viewers, ViewerData{
						RemoteAddr:  info.RemoteAddr,
						ConnectedAt: info.ConnectedAt.Format(time.RFC3339),
						UserAgent:   info.UserAgent,
					})
				}
			}
			session.ClientMu.Unlock()
			if count > 0 {
				casting[id] = CastingStatus{
					Count:   count,
					Viewers: viewers,
				}
			}
		}
		activeSessionsMu.Unlock()

		response := map[string]any{
			"networkAccess": networkAccessEnabled,
			"casting":       casting,
		}
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	if r.Method == http.MethodPost {
		activeSessionsMu.Lock()
		for _, session := range activeSessions {
			session.ClientMu.Lock()
			for conn := range session.Clients {
				_ = conn.WriteMessage(websocket.TextMessage, []byte("stop"))
				conn.Close()
				delete(session.Clients, conn)
			}
			session.ClientMu.Unlock()
		}
		activeSessionsMu.Unlock()
		w.WriteHeader(200)
		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

var (
	activeTunnels   = make(map[string]bool)
	activeTunnelsMu sync.Mutex
)

func handleTunnelEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	port := r.URL.Query().Get("port")
	if port == "" {
		http.Error(w, "missing port", 400)
		return
	}
	activeTunnelsMu.Lock()
	activeTunnels[port] = true
	activeTunnelsMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func handleTunnelDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	port := r.URL.Query().Get("port")
	if port == "" {
		http.Error(w, "missing port", 400)
		return
	}
	activeTunnelsMu.Lock()
	delete(activeTunnels, port)
	activeTunnelsMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func handleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	activeTunnelsMu.Lock()
	list := []string{}
	for port := range activeTunnels {
		list = append(list, port)
	}
	activeTunnelsMu.Unlock()
	_ = json.NewEncoder(w).Encode(list)
}

func handleTunnelProxy(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "bad request", 400)
		return
	}
	portStr := parts[2]

	activeTunnelsMu.Lock()
	enabled := activeTunnels[portStr]
	activeTunnelsMu.Unlock()

	if !enabled {
		http.Error(w, "Tunnel not active", http.StatusForbidden)
		return
	}

	targetURL, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%s", portStr))
	if err != nil {
		http.Error(w, "invalid port", 400)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	r.URL.Path = "/" + strings.Join(parts[3:], "/")

	r.Header.Set("X-Forwarded-Host", r.Header.Get("Host"))
	proxy.ServeHTTP(w, r)
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
		w.Write(htmlBytes)
	})
	mux.Handle("/web/", http.FileServer(http.FS(webFS)))
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

	httpHandler = networkAuthMiddleware(mux)
	listenerMu.Lock()
	httpListener = listener
	listenerMu.Unlock()
	go serveHTTP(listener, httpHandler)

	select {}
}

func handleTransparency(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	pct, err := adjustOpacity(dir)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "opacity": pct})
		return
	}

	state := readAppState()
	state.Opacity = pct
	writeAppState(state)

	_ = json.NewEncoder(w).Encode(map[string]any{"opacity": pct})
}

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
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func handleSnippetAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
	path := filepath.Join(home, ".invoke_scratchpad.txt")
	oldPath := filepath.Join(home, ".powerterm_scratchpad.txt")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if _, errOld := os.Stat(oldPath); errOld == nil {
			_ = os.Rename(oldPath, path)
		} else {
			_ = os.WriteFile(path, []byte(""), 0644)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"path": path})
}

func handleSessionScratchpadPath(w http.ResponseWriter, r *http.Request) {
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
	newPath := filepath.Join(home, ".invoke_state.json")
	oldPath := filepath.Join(home, ".powerterm_state.json")
	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		if _, errOld := os.Stat(oldPath); errOld == nil {
			_ = os.Rename(oldPath, newPath)
		}
	}
	return newPath
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
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func handleWorkspaceLoad(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	cfg, ok := loadWorkspaceConfig(dir)
	if !ok {
		_ = json.NewEncoder(w).Encode(map[string]any{"found": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"found": true, "config": cfg})
}

func handleWorkspaceSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Dir    string          `json:"dir"`
		Config WorkspaceConfig `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Dir == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := saveWorkspaceConfig(req.Dir, req.Config); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

