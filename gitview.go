package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type gitDeltaFile struct {
	Path     string `json:"path"`
	Status   string `json:"status"`
	Original string `json:"original"`
	Modified string `json:"modified"`
	Lang     string `json:"lang"`
}

func langForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".go":
		return "go"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".json":
		return "json"
	case ".html", ".htm", ".svelte":
		return "html"
	case ".csproj", ".vbproj", ".fsproj", ".xsd":
		return "xml"
	case ".fs":
		return "fsharp"
	case ".vb":
		return "vb"
	case ".tpl":
		return "yaml"
	case ".css":
		return "css"
	case ".md":
		return "markdown"
	case ".ps1", ".psm1", ".psd1":
		return "powershell"
	case ".py":
		return "python"
	case ".sh", ".bash":
		return "shell"
	case ".xml":
		return "xml"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "ini"
	case ".sql":
		return "sql"
	case ".c", ".h":
		return "c"
	case ".cpp", ".hpp", ".cc", ".cxx":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	default:
		return "plaintext"
	}
}

func serveGitOverview(dir string) {
	status := getGitStatus(dir)
	if !status.IsRepo {
		fmt.Println("Not a git repository.")
		return
	}
	if len(status.Files) == 0 {
		fmt.Println("Working tree clean - no deltas to show.")
		return
	}

	files := make([]gitDeltaFile, 0, len(status.Files))
	for _, f := range status.Files {
		abs := filepath.Join(dir, f.Path)
		original := gitFileAtHead(abs)
		modified := ""
		if b, err := os.ReadFile(abs); err == nil {
			modified = string(b)
		}
		if len(original) > 400000 {
			original = original[:400000] + "\n...(truncated)..."
		}
		if len(modified) > 400000 {
			modified = modified[:400000] + "\n...(truncated)..."
		}
		files = append(files, gitDeltaFile{
			Path:     filepath.ToSlash(f.Path),
			Status:   f.Status,
			Original: original,
			Modified: modified,
			Lang:     langForExt(filepath.Ext(f.Path)),
		})
	}

	filesJSON, _ := json.Marshal(files)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Printf("Error starting local server: %v\n", err)
		return
	}
	port := listener.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://localhost:%d", port)

	mux := http.NewServeMux()
	mux.Handle("/web/", http.FileServer(http.FS(webFS)))
	mux.HandleFunc("/ai-explain-hover", handleAIExplainHover)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t, err := template.ParseFS(webFS, "web/gitview.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		data := struct {
			Branch    string
			Dir       string
			FileCount int
			FilesJSON template.JS
		}{
			Branch:    status.Branch,
			Dir:       dir,
			FileCount: len(files),
			FilesJSON: template.JS(filesJSON),
		}
		w.Header().Set("Content-Type", "text/html")
		t.Execute(w, data)
	})

	shutdownCh := make(chan struct{})
	mux.HandleFunc("/close", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		select {
		case shutdownCh <- struct{}{}:
		default:
		}
	})

	server := &http.Server{Handler: mux}
	go func() {
		fmt.Printf("Git delta view running at %s\nBranch: %s  (%d changed file(s))\nClose the browser tab when done.\n", url, status.Branch, len(files))
		openBrowser(url)
		server.Serve(listener)
	}()

	<-shutdownCh
	server.Close()
	fmt.Println("Git view closed.")
}

func handleGitOverviewRoute(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}

	status := getGitStatus(absDir)
	if !status.IsRepo {
		http.Error(w, "Not a git repository.", 400)
		return
	}

	files := make([]gitDeltaFile, 0, len(status.Files))
	for _, f := range status.Files {
		abs := filepath.Join(absDir, f.Path)
		original := gitFileAtHead(abs)
		modified := ""
		if b, err := os.ReadFile(abs); err == nil {
			modified = string(b)
		}
		if len(original) > 400000 {
			original = original[:400000] + "\n...(truncated)..."
		}
		if len(modified) > 400000 {
			modified = modified[:400000] + "\n...(truncated)..."
		}
		files = append(files, gitDeltaFile{
			Path:     filepath.ToSlash(f.Path),
			Status:   f.Status,
			Original: original,
			Modified: modified,
			Lang:     langForExt(filepath.Ext(f.Path)),
		})
	}

	filesJSON, _ := json.Marshal(files)
	t, err := template.ParseFS(webFS, "web/gitview.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	data := struct {
		Branch    string
		Dir       string
		FileCount int
		FilesJSON template.JS
	}{
		Branch:    status.Branch,
		Dir:       absDir,
		FileCount: len(files),
		FilesJSON: template.JS(filesJSON),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t.Execute(w, data)
}

func handleGitStage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	dir := r.URL.Query().Get("dir")
	file := r.URL.Query().Get("file")
	if dir == "" || file == "" {
		http.Error(w, `{"error":"dir and file required"}`, 400)
		return
	}
	cmd := exec.Command("git", "add", "--", file)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": string(out)})
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func handleGitUnstage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	dir := r.URL.Query().Get("dir")
	file := r.URL.Query().Get("file")
	if dir == "" || file == "" {
		http.Error(w, `{"error":"dir and file required"}`, 400)
		return
	}
	cmd := exec.Command("git", "restore", "--staged", "--", file)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": string(out)})
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func handleGitCommit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		Dir     string `json:"dir"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Dir == "" || body.Message == "" {
		http.Error(w, `{"error":"dir and message required"}`, 400)
		return
	}
	cmd := exec.Command("git", "commit", "-m", body.Message)
	cmd.Dir = body.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": string(out)})
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
