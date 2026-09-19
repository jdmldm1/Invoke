package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

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
