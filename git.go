package main

import (
	"bytes"
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

type GitBranchInfo struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
	Remote  bool   `json:"remote"`
}

type GitFileStatus struct {
	Path   string
	Status string
}

type GitRepoStatus struct {
	IsRepo bool
	Branch string
	Files  []GitFileStatus
}

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

func getGitStatus(dir string) GitRepoStatus {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	err := cmd.Run()
	if err != nil {
		return GitRepoStatus{IsRepo: false}
	}

	branchCmd := exec.Command("git", "branch", "--show-current")
	branchCmd.Dir = dir
	branchOut, _ := branchCmd.Output()
	branch := strings.TrimSpace(string(branchOut))
	if branch == "" {
		branchCmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
		branchCmd.Dir = dir
		branchOut, _ = branchCmd.Output()
		branch = strings.TrimSpace(string(branchOut))
	}

	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusOut, _ := statusCmd.Output()

	var files []GitFileStatus
	lines := strings.Split(string(statusOut), "\n")
	for _, line := range lines {
		if len(line) < 3 {
			continue
		}
		statusCode := line[:2]
		filePath := strings.TrimSpace(line[2:])

		status := "modified"
		if strings.Contains(statusCode, "?") {
			status = "untracked"
		} else if strings.Contains(statusCode, "A") {
			status = "added"
		} else if strings.Contains(statusCode, "D") {
			status = "deleted"
		} else if statusCode[0] != ' ' && statusCode[1] == ' ' {
			status = "staged"
		}

		files = append(files, GitFileStatus{
			Path:   filePath,
			Status: status,
		})
	}

	return GitRepoStatus{
		IsRepo: true,
		Branch: branch,
		Files:  files,
	}
}

func gitStageFile(dir, file string) error {
	cmd := exec.Command("git", "add", file)
	cmd.Dir = dir
	return cmd.Run()
}

func gitUnstageFile(dir, file string) error {
	cmd := exec.Command("git", "restore", "--staged", file)
	cmd.Dir = dir
	return cmd.Run()
}

func gitGetDiff(dir, file string) string {
	var cmd *exec.Cmd
	status := getGitStatus(dir)
	isStaged := false
	for _, f := range status.Files {
		if f.Path == file && f.Status == "staged" {
			isStaged = true
			break
		}
	}

	if isStaged {
		cmd = exec.Command("git", "diff", "--cached", file)
	} else {
		cmd = exec.Command("git", "diff", file)
	}
	cmd.Dir = dir
	var outBytes bytes.Buffer
	cmd.Stdout = &outBytes
	_ = cmd.Run()
	return outBytes.String()
}

func gitCommit(dir, msg string) (string, error) {
	cmd := exec.Command("git", "commit", "-m", msg)
	cmd.Dir = dir
	var outBytes bytes.Buffer
	cmd.Stdout = &outBytes
	cmd.Stderr = &outBytes
	err := cmd.Run()
	return outBytes.String(), err
}

func gitGetBranches(dir string) ([]string, error) {
	cmd := exec.Command("git", "branch")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var branches []string
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "* ") {
			line = strings.TrimPrefix(line, "* ")
		}
		branches = append(branches, line)
	}
	return branches, nil
}

func gitCheckout(dir, branch string) (string, error) {
	cmd := exec.Command("git", "checkout", branch)
	cmd.Dir = dir
	var outBytes bytes.Buffer
	cmd.Stdout = &outBytes
	cmd.Stderr = &outBytes
	err := cmd.Run()
	return outBytes.String(), err
}

func gitPull(dir string) (string, error) {
	cmd := exec.Command("git", "pull")
	cmd.Dir = dir
	var outBytes bytes.Buffer
	cmd.Stdout = &outBytes
	cmd.Stderr = &outBytes
	err := cmd.Run()
	return outBytes.String(), err
}

func gitPush(dir string) (string, error) {
	cmd := exec.Command("git", "push")
	cmd.Dir = dir
	var outBytes bytes.Buffer
	cmd.Stdout = &outBytes
	cmd.Stderr = &outBytes
	err := cmd.Run()
	return outBytes.String(), err
}

func gitStageAll(dir string) error {
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = dir
	return cmd.Run()
}

func gitDiscardFile(dir string, f GitFileStatus) (string, error) {
	if f.Status == "untracked" {
		if err := os.Remove(filepath.Join(dir, f.Path)); err != nil {
			return "", err
		}
		return "Deleted untracked file: " + f.Path, nil
	}

	unstage := exec.Command("git", "restore", "--staged", f.Path)
	unstage.Dir = dir
	_ = unstage.Run()

	cmd := exec.Command("git", "restore", f.Path)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		cmd2 := exec.Command("git", "checkout", "--", f.Path)
		cmd2.Dir = dir
		var out2 bytes.Buffer
		cmd2.Stdout = &out2
		cmd2.Stderr = &out2
		if err2 := cmd2.Run(); err2 != nil {
			return out.String() + out2.String(), err2
		}
	}
	return "Discarded changes in: " + f.Path, nil
}

func gitFileAtHead(absPath string) string {
	dir := filepath.Dir(absPath)
	rootCmd := exec.Command("git", "rev-parse", "--show-toplevel")
	rootCmd.Dir = dir
	rootOut, err := rootCmd.Output()
	if err != nil {
		return ""
	}
	root := strings.TrimSpace(string(rootOut))
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return ""
	}
	showCmd := exec.Command("git", "show", "HEAD:"+filepath.ToSlash(rel))
	showCmd.Dir = dir
	out, err := showCmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func gitGetStagedDiff(dir string) string {
	cmd := exec.Command("git", "diff", "--staged")
	cmd.Dir = dir
	out, _ := cmd.Output()
	return string(out)
}

func gitGetWorkingDiff(dir string) string {
	cmd := exec.Command("git", "diff", "HEAD")
	cmd.Dir = dir
	out, _ := cmd.Output()
	return string(out)
}

func gitGetLog(dir string, n int) string {
	cmd := exec.Command("git", "log", fmt.Sprintf("-%d", n), "--pretty=format:%h  %ad  %s", "--date=short")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "Could not read git log: " + err.Error()
	}
	if strings.TrimSpace(string(out)) == "" {
		return "No commits yet."
	}
	return string(out)
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
