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

// langForExt maps a file extension to a Monaco language id.
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

// serveGitOverview opens a browser "source control" view of the working tree:
// a sidebar of changed files and a Monaco diff editor (committed vs working).
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
		t, err := template.New("gitview").Parse(gitOverviewTemplate)
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
	t, err := template.New("gitview").Parse(gitOverviewTemplate)
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

const gitOverviewTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Invoke Git - {{.Branch}}</title>
    <link rel="icon" type="image/png" href="/web/favicon.png" />
    <style>
        :root {
            --bg: #000000; --panel: #000000; --border: #1a1a1a; --text: #e2e2e2;
            --muted: #666; --accent: #a855f7; --add: #a855f7; --del: #bf616a;
            --mod: #d6b36a; --new: #a855f7; --untracked: #888;
        }
        * { box-sizing: border-box; }
        body { margin: 0; height: 100vh; overflow: hidden; background: var(--bg); color: var(--text);
               font-family: 'Segoe UI', system-ui, sans-serif; display: flex; flex-direction: column; }
        #topbar { height: 44px; flex: 0 0 44px; background: var(--panel); border-bottom: 1px solid var(--border);
                  display: flex; align-items: center; padding: 0 16px; gap: 12px; }
        #topbar .logo { font-weight: 700; color: var(--accent); }
        #topbar .branch { color: var(--mod); font-weight: 600; }
        #topbar .dir { color: var(--muted); font-size: 0.85em; margin-left: auto; }
        #main { flex: 1; display: flex; min-height: 0; }
        #sidebar { width: 300px; flex: 0 0 300px; background: var(--panel); border-right: 1px solid var(--border);
                   overflow-y: auto; padding: 8px 0; }
        #sidebar .hint { color: var(--muted); font-size: 0.78em; padding: 4px 16px 10px; }
        .file { display: flex; align-items: center; gap: 8px; padding: 7px 16px; cursor: pointer;
                font-size: 0.88em; border-left: 3px solid transparent; }
        .file:hover { background: #121212; }
        .file.active { background: #1a1a1a; border-left-color: var(--accent); }
        .file .name { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; direction: rtl; text-align: left; }
        .badge { flex: 0 0 18px; text-align: center; font-weight: 700; font-size: 0.8em; border-radius: 3px; }
        .s-modified, .s-staged { color: var(--mod); }
        .s-added { color: var(--add); }
        .s-deleted { color: var(--del); }
        .s-untracked { color: var(--untracked); }
        #container { flex: 1; min-width: 0; background-color: #000000; }
    </style>
    <script src="/web/vs/loader.js"></script>
</head>
<body>
    <div id="topbar">
        <span class="logo">Invoke</span>
        <span>git delta</span>
        <span class="branch">{{.Branch}}</span>
        <span style="color:var(--muted)">{{.FileCount}} changed</span>
        <span class="dir">{{.Dir}}</span>
    </div>
    <div id="main">
        <div id="sidebar">
            <div class="hint">Click a file (or Up/Down) to view its diff</div>
            <div id="files"></div>
        </div>
        <div id="container"></div>
    </div>
    <script>
        const files = {{.FilesJSON}};
        function badge(s) {
            if (s === 'added' || s === 'staged') return 'A';
            if (s === 'deleted') return 'D';
            if (s === 'untracked') return '?';
            return 'M';
        }
        require.config({ paths: { 'vs': '/web/vs' }});
        require(['vs/editor/editor.main'], function() {
            monaco.editor.defineTheme('badass-black', {
                base: 'vs-dark',
                inherit: true,
                rules: [],
                colors: {
                    'editor.background': '#000000',
                    'editorGutter.background': '#000000',
                    'minimap.background': '#000000',
                    'diffEditor.inserted.background': '#a855f722',
                    'diffEditor.removed.background': '#bf616a22'
                }
            });

            const diffEditor = monaco.editor.createDiffEditor(document.getElementById('container'), {
                theme: 'badass-black', automaticLayout: true, readOnly: true, originalEditable: false,
                renderSideBySide: true, minimap: { enabled: true }, ignoreTrimWhitespace: false
            });
            let current = -1;
            function show(i) {
                if (i < 0 || i >= files.length) return;
                current = i;
                const f = files[i];
                diffEditor.setModel({
                    original: monaco.editor.createModel(f.original, f.lang),
                    modified: monaco.editor.createModel(f.modified, f.lang)
                });
                document.querySelectorAll('.file').forEach((el, idx) => el.classList.toggle('active', idx === i));
                const active = document.querySelectorAll('.file')[i];
                if (active) active.scrollIntoView({ block: 'nearest' });
            }
            const list = document.getElementById('files');
            files.forEach((f, i) => {
                const d = document.createElement('div');
                d.className = 'file';
                const b = document.createElement('span');
                b.className = 'badge s-' + f.status; b.textContent = badge(f.status);
                const n = document.createElement('span');
                n.className = 'name'; n.textContent = f.path; n.title = f.path;
                d.appendChild(b); d.appendChild(n);
                d.onclick = () => show(i);
                list.appendChild(d);
            });
            document.addEventListener('keydown', (e) => {
                if (e.key === 'ArrowDown' || e.key === 'j') { show(Math.min(current + 1, files.length - 1)); e.preventDefault(); }
                if (e.key === 'ArrowUp'   || e.key === 'k') { show(Math.max(current - 1, 0)); e.preventDefault(); }
            });
            if (files.length) show(0);

            registerAIHoverProvider();
        });

        function registerAIHoverProvider() {
            monaco.languages.getLanguages().forEach(function(lang) {
                monaco.languages.registerHoverProvider(lang.id, {
                    provideHover: function(model, position, token) {
                        return new Promise((resolve) => {
                            const timer = setTimeout(() => {
                                if (token.isCancellationRequested) {
                                    resolve(null);
                                    return;
                                }
                                const wordInfo = model.getWordAtPosition(position);
                                if (!wordInfo) {
                                    resolve(null);
                                    return;
                                }
                                const word = wordInfo.word;
                                const line = model.getLineContent(position.lineNumber);
                                const startLine = Math.max(1, position.lineNumber - 5);
                                const endLine = Math.min(model.getLineCount(), position.lineNumber + 5);
                                let context = "";
                                for (let i = startLine; i <= endLine; i++) {
                                    context += model.getLineContent(i) + "\n";
                                }
                                fetch('/ai-explain-hover', {
                                    method: 'POST',
                                    headers: { 'Content-Type': 'application/json' },
                                    body: JSON.stringify({
                                        symbol: word,
                                        line: line,
                                        context: context,
                                        lang: model.getLanguageId()
                                    })
                                })
                                .then(r => r.json())
                                .then(data => {
                                    if (token.isCancellationRequested) {
                                        resolve(null);
                                        return;
                                    }
                                    if (data && data.explanation) {
                                        resolve({
                                            range: new monaco.Range(position.lineNumber, wordInfo.startColumn, position.lineNumber, wordInfo.endColumn),
                                            contents: [
                                                { value: '**🤖 AI Explains **' + word },
                                                { value: data.explanation }
                                            ]
                                        });
                                    } else {
                                        resolve(null);
                                    }
                                })
                                .catch(() => resolve(null));
                            }, 300);
                            token.onCancellationRequested(() => {
                                clearTimeout(timer);
                                resolve(null);
                            });
                        });
                    }
                });
            });
        }

        window.addEventListener("beforeunload", function () { fetch('/close', { method: 'POST' }); });
    </script>
</body>
</html>
`

// handleGitStage stages a file relative to the given dir.
// POST /git/stage?dir=...&file=...
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

// handleGitUnstage un-stages a file.
// POST /git/unstage?dir=...&file=...
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

// handleGitCommit commits staged changes.
// POST /git/commit  body: {"dir":"...","message":"..."}
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
