package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
)

type GitBranchInfo struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
	Remote  bool   `json:"remote"`
}

func handleGitBranches(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	dir := r.URL.Query().Get("dir")
	if dir == "" {
		http.Error(w, `{"error":"dir parameter required"}`, 400)
		return
	}

	cmd := exec.Command("git", "branch", "--list", "-a")
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		json.NewEncoder(w).Encode([]GitBranchInfo{})
		return
	}

	branches := []GitBranchInfo{}
	lines := strings.Split(stdout.String(), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}

		isCurrent := strings.HasPrefix(l, "*")
		name := strings.TrimSpace(strings.TrimPrefix(l, "*"))
		isRemote := strings.HasPrefix(name, "remotes/")

		if isRemote {
			name = strings.TrimPrefix(name, "remotes/")
		}

		branches = append(branches, GitBranchInfo{
			Name:    name,
			Current: isCurrent,
			Remote:  isRemote,
		})
	}

	json.NewEncoder(w).Encode(branches)
}

func handleGitCheckout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Dir    string `json:"dir"`
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Dir == "" || req.Branch == "" {
		http.Error(w, `{"error":"dir and branch parameters required"}`, 400)
		return
	}

	branch := req.Branch
	if strings.HasPrefix(branch, "origin/") {
		localName := strings.TrimPrefix(branch, "origin/")
		cmd := exec.Command("git", "checkout", "-b", localName, "--track", branch)
		cmd.Dir = req.Dir
		if err := cmd.Run(); err != nil {
			cmdFallback := exec.Command("git", "checkout", localName)
			cmdFallback.Dir = req.Dir
			_ = cmdFallback.Run()
		}
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		return
	}

	cmd := exec.Command("git", "checkout", branch)
	cmd.Dir = req.Dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": stderr.String()})
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func handleGitCreateBranch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Dir  string `json:"dir"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Dir == "" || req.Name == "" {
		http.Error(w, `{"error":"dir and name parameters required"}`, 400)
		return
	}

	cmd := exec.Command("git", "checkout", "-b", req.Name)
	cmd.Dir = req.Dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": stderr.String()})
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
