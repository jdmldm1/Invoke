package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" || networkAccessEnabled {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"
	},
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
	Cpty       Pty
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
)

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
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

	cpty, err := startPty(cols, rows, env, cwd)
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
