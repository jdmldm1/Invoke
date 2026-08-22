package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleHistoryEndpoint(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "invoke_server_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldConfigPath := configPath
	configPath = filepath.Join(tempDir, ".invoke_test.json")
	defer func() { configPath = oldConfigPath }()

	cfg := loadConfig()
	cfg.History = []string{"git status", "npm run dev", "docker ps"}
	saveConfig(cfg)

	req := httptest.NewRequest(http.MethodGet, "/history", nil)
	w := httptest.NewRecorder()

	handleHistory(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected HTTP 200, got %d", resp.StatusCode)
	}

	var hist []string
	if err := json.NewDecoder(resp.Body).Decode(&hist); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if len(hist) == 0 {
		t.Errorf("expected history items, got empty slice")
	}
}

func TestHandleConfigEndpoint(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "invoke_cfg_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldConfigPath := configPath
	configPath = filepath.Join(tempDir, ".invoke_test.json")
	defer func() { configPath = oldConfigPath }()

	reqGet := httptest.NewRequest(http.MethodGet, "/config", nil)
	wGet := httptest.NewRecorder()
	handleConfig(wGet, reqGet)

	if wGet.Code != http.StatusOK {
		t.Errorf("GET /config returned status %d", wGet.Code)
	}

	cfg := loadConfig()
	cfg.OllamaHost = "http://localhost:11434"
	body, _ := json.Marshal(cfg)

	reqPost := httptest.NewRequest(http.MethodPost, "/config", bytes.NewBuffer(body))
	wPost := httptest.NewRecorder()
	handleConfig(wPost, reqPost)

	if wPost.Code != http.StatusOK {
		t.Errorf("POST /config returned status %d", wPost.Code)
	}
}
