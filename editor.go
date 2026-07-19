package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

type SidebarFile struct {
	Name  string `json:"name"`
	IsDir bool   `json:"dir"`
}

type SidebarTask struct {
	Name string `json:"name"`
	Cmd  string `json:"cmd"`
}

type SidebarResponse struct {
	Dir   string        `json:"dir"`
	Files []SidebarFile `json:"files"`
	Tasks []SidebarTask `json:"tasks"`
}

func serveMonacoDiff(filePath string) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		fmt.Printf("Error resolving path: %v\n", err)
		return
	}

	if host := os.Getenv("INVOKE_HOST"); host != "" {
		url := fmt.Sprintf("%s/open", host)
		body, _ := json.Marshal(map[string]string{"type": "diff", "file": absPath})
		resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			fmt.Printf("Opened diff for %s in a new tab.\n", filepath.Base(absPath))
			return
		}
	}

	modified, err := os.ReadFile(absPath)
	if err != nil {
		modified = []byte("")
	}
	original := gitFileAtHead(absPath)

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
		t, err := template.ParseFS(webFS, "web/diff.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		origJSON, _ := json.Marshal(string(original))
		modJSON, _ := json.Marshal(string(modified))
		data := struct {
			FileName string
			Ext      string
			Original template.JS
			Modified template.JS
		}{
			FileName: filepath.Base(absPath),
			Ext:      filepath.Ext(absPath),
			Original: template.JS(origJSON),
			Modified: template.JS(modJSON),
		}
		w.Header().Set("Content-Type", "text/html")
		t.Execute(w, data)
	})

	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := os.WriteFile(absPath, []byte(payload.Content), 0644); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(200)
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
		fmt.Printf("Monaco Diff running at %s\nComparing: %s (vs HEAD)\nClose the browser tab when done.\n", url, absPath)
		openBrowser(url)
		server.Serve(listener)
	}()

	<-shutdownCh
	server.Close()
	fmt.Println("Diff viewer closed.")
}

func serveMonacoEditor(filePath string) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		fmt.Printf("Error resolving path: %v\n", err)
		return
	}

	if host := os.Getenv("INVOKE_HOST"); host != "" {
		url := fmt.Sprintf("%s/open", host)
		body, _ := json.Marshal(map[string]string{"type": "edit", "file": absPath})
		resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			fmt.Printf("Opened edit for %s in a new tab.\n", filepath.Base(absPath))
			return
		}
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		content = []byte("")
	}

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
		t, err := template.ParseFS(webFS, "web/editor.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		jsonContent, _ := json.Marshal(string(content))
		data := struct {
			FileName string
			Ext      string
			Content  template.JS
		}{
			FileName: filepath.Base(absPath),
			Ext:      filepath.Ext(absPath),
			Content:  template.JS(jsonContent),
		}
		w.Header().Set("Content-Type", "text/html")
		t.Execute(w, data)
	})

	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := os.WriteFile(absPath, []byte(payload.Content), 0644); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(200)
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
		fmt.Printf("Monaco Editor running at %s\nEditing: %s\nPress Ctrl+C in terminal to stop, or simply close the browser tab.\n", url, absPath)
		openBrowser(url)
		server.Serve(listener)
	}()

	<-shutdownCh
	server.Close()
	fmt.Println("Editor closed.")
}

func openBrowser(url string) {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}

	exec.Command(cmd, args...).Start()
}

func handleConverterRoute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	htmlBytes, err := webFS.ReadFile("web/converter.html")
	if err != nil {
		http.Error(w, "Converter template not found", 404)
		return
	}
	w.Write(htmlBytes)
}

func handleConverterRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Code string `json:"code"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Code == "" {
		http.Error(w, "bad request", 400)
		return
	}

	prompt := fmt.Sprintf(
		"You are a specialized scripting language translator. Translate this script/command from %s to %s.\n"+
			"Provide ONLY the translated code, without any markdown backticks, without any prefix/suffix explanation, and without additional comments unless required for functional correctness. "+
			"Here is the code to translate:\n\n%s",
		req.From, req.To, req.Code,
	)

	out, err := aiGenerateOnce(prompt, 60*time.Second)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "code": out})
}

func handleSidebarInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}

	res := SidebarResponse{
		Dir:   absDir,
		Files: []SidebarFile{},
		Tasks: []SidebarTask{},
	}

	entries, err := os.ReadDir(absDir)
	if err == nil {
		for _, e := range entries {
			res.Files = append(res.Files, SidebarFile{
				Name:  e.Name(),
				IsDir: e.IsDir(),
			})
		}
	}

	packageJsonPath := filepath.Join(absDir, "package.json")
	if f, err := os.Open(packageJsonPath); err == nil {
		defer f.Close()
		var pData struct {
			Scripts map[string]string `json:"scripts"`
		}
		dec := json.NewDecoder(f)
		if err := dec.Decode(&pData); err == nil {
			for name := range pData.Scripts {
				res.Tasks = append(res.Tasks, SidebarTask{
					Name: "npm run " + name,
					Cmd:  "npm run " + name,
				})
			}
		}
	}

	goModPath := filepath.Join(absDir, "go.mod")
	if _, err := os.Stat(goModPath); err == nil {
		res.Tasks = append(res.Tasks, SidebarTask{Name: "go run .", Cmd: "go run ."})
		res.Tasks = append(res.Tasks, SidebarTask{Name: "go test", Cmd: "go test ./..."})
		res.Tasks = append(res.Tasks, SidebarTask{Name: "go build", Cmd: "go build"})
	}

	makefileJsonPath := filepath.Join(absDir, "Makefile")
	if f, err := os.Open(makefileJsonPath); err == nil {
		defer f.Close()
		b, _ := io.ReadAll(f)
		content := string(b)

		re := regexp.MustCompile(`^[a-zA-Z0-9_-]+:`)
		for l := range strings.SplitSeq(content, "\n") {
			if re.MatchString(l) {
				target := strings.TrimSpace(strings.Split(l, ":")[0])
				if target != ".PHONY" && target != "all" {
					res.Tasks = append(res.Tasks, SidebarTask{
						Name: "make " + target,
						Cmd:  "make " + target,
					})
				}
			}
		}
	}

	requirementsPath := filepath.Join(absDir, "requirements.txt")
	if _, err := os.Stat(requirementsPath); err == nil {
		res.Tasks = append(res.Tasks, SidebarTask{
			Name: "pip install requirements",
			Cmd:  "pip install -r requirements.txt",
		})
	}

	cargoPath := filepath.Join(absDir, "Cargo.toml")
	if _, err := os.Stat(cargoPath); err == nil {
		res.Tasks = append(res.Tasks, SidebarTask{Name: "cargo run", Cmd: "cargo run"})
		res.Tasks = append(res.Tasks, SidebarTask{Name: "cargo test", Cmd: "cargo test"})
		res.Tasks = append(res.Tasks, SidebarTask{Name: "cargo build", Cmd: "cargo build"})
	}

	json.NewEncoder(w).Encode(res)
}

type SearchResult struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

func handleFS(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}

	type ent struct {
		Name string `json:"name"`
		Dir  bool   `json:"dir"`
	}
	out := struct {
		Dir     string `json:"dir"`
		Parent  string `json:"parent"`
		Entries []ent  `json:"entries"`
	}{Dir: abs, Parent: filepath.Dir(abs), Entries: []ent{}}

	entries, err := os.ReadDir(abs)
	if err == nil {
		var dirs, files []ent
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, ent{e.Name(), true})
			} else {
				files = append(files, ent{e.Name(), false})
			}
		}
		sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
		sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
		out.Entries = append(out.Entries, dirs...)
		out.Entries = append(out.Entries, files...)
		if len(out.Entries) > 800 {
			out.Entries = out.Entries[:800]
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
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

func readFileOrEmpty(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func handleFileSave(w http.ResponseWriter, r *http.Request) {
	var req struct{ File, Content string }
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.File == "" {
		http.Error(w, "bad request", 400)
		return
	}
	if err := os.WriteFile(req.File, []byte(req.Content), 0644); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(200)
}

func fill(tmpl string, kv map[string]string) string {
	for k, v := range kv {
		tmpl = strings.ReplaceAll(tmpl, k, v)
	}
	return tmpl
}

func monacoDiffHTML(name, ext, original, modified string) string {
	tmplBytes, err := webFS.ReadFile("web/tab_diff.html")
	if err != nil {
		return "Template read error: " + err.Error()
	}
	o, _ := json.Marshal(original)
	m, _ := json.Marshal(modified)
	l, _ := json.Marshal(langForExt(ext))
	return fill(string(tmplBytes), map[string]string{
		"__NAME__":     template.HTMLEscapeString(name),
		"__ORIGINAL__": string(o),
		"__MODIFIED__": string(m),
		"__LANG__":     string(l),
	})
}

func monacoEditHTML(name, ext, absPath, content string) string {
	tmplBytes, err := webFS.ReadFile("web/tab_editor.html")
	if err != nil {
		return "Template read error: " + err.Error()
	}
	c, _ := json.Marshal(content)
	l, _ := json.Marshal(langForExt(ext))
	a, _ := json.Marshal(absPath)
	return fill(string(tmplBytes), map[string]string{
		"__NAME__":    template.HTMLEscapeString(name),
		"__CONTENT__": string(c),
		"__LANG__":    string(l),
		"__ABS__":     string(a),
	})
}
