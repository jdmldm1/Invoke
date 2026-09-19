package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
)

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
