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
	"runtime"
)

const editorTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>PowerTool Monaco Editor - {{.FileName}}</title>
    <link rel="icon" type="image/png" href="/web/favicon.png" />
    <style>
        body { margin: 0; padding: 0; overflow: hidden; background-color: #000000; color: #d4d4d4; font-family: sans-serif; }
        #header { height: 35px; background: #000000; display: flex; align-items: center; padding: 0 15px; justify-content: space-between; border-bottom: 1px solid #1a1a1a; }
        #header .info { display: flex; align-items: center; gap: 10px; }
        #header .info span { font-weight: bold; }
        #header .status { color: #a855f7; font-size: 0.9em; opacity: 0; transition: opacity 0.3s; font-weight: bold; }
        #container { height: calc(100vh - 35px); width: 100%; background-color: #000000; }
    </style>
    <script src="/web/vs/loader.js"></script>
</head>
<body>
    <div id="header">
        <div class="info">
            <span>{{.FileName}}</span>
            <span style="color: #666; font-size: 0.9em; font-weight: normal;">(Press Ctrl+S to save)</span>
        </div>
        <span class="status" id="status">Saved!</span>
    </div>
    <div id="container"></div>
    <script>
        const initialContent = {{.Content}};
        require.config({ paths: { 'vs': '/web/vs' }});
        require(['vs/editor/editor.main'], function() {
            monaco.editor.defineTheme('badass-black', {
                base: 'vs-dark',
                inherit: true,
                rules: [],
                colors: {
                    'editor.background': '#000000',
                    'editorGutter.background': '#000000',
                    'minimap.background': '#000000'
                }
            });

            var editor = monaco.editor.create(document.getElementById('container'), {
                value: initialContent,
                language: getLanguage('{{.Ext}}'),
                theme: 'badass-black',
                automaticLayout: true,
                minimap: { enabled: true }
            });

            editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, function() {
                saveContent(editor.getValue());
            });

            registerAIHoverProvider();
            registerAIInlineCompletionsProvider();
            registerAICompletionItemProvider();
        });

        function registerAICompletionItemProvider() {
            const langId = getLanguage('{{.Ext}}');
            monaco.languages.registerCompletionItemProvider(langId, {
                triggerCharacters: ['.', ' ', '(', '{', '='],
                provideCompletionItems: function(model, position, context, token) {
                    return new Promise((resolve) => {
                        const timer = setTimeout(() => {
                            if (token.isCancellationRequested) {
                                resolve(null);
                                return;
                            }
                            const textBefore = model.getValueInRange({
                                startLineNumber: Math.max(1, position.lineNumber - 30),
                                startColumn: 1,
                                endLineNumber: position.lineNumber,
                                endColumn: position.column
                            });
                            if (textBefore.trim().length < 5) {
                                resolve(null);
                                return;
                            }
                            fetch('/ai-editor-complete', {
                                method: 'POST',
                                headers: { 'Content-Type': 'application/json' },
                                body: JSON.stringify({
                                    before: textBefore,
                                    lang: langId
                                })
                            })
                            .then(r => r.json())
                            .then(data => {
                                if (token.isCancellationRequested) {
                                    resolve(null);
                                    return;
                                }
                                if (data && data.completion && data.completion.trim()) {
                                    resolve({
                                        suggestions: [{
                                            label: '🤖 AI: ' + data.completion.split('\n')[0],
                                            kind: monaco.languages.CompletionItemKind.Snippet,
                                            insertText: data.completion,
                                            detail: 'AI Generated Completion',
                                            documentation: data.completion,
                                            range: new monaco.Range(position.lineNumber, position.column, position.lineNumber, position.column)
                                        }]
                                    });
                                } else {
                                    resolve(null);
                                }
                            })
                            .catch(() => resolve(null));
                        }, 500);
                        token.onCancellationRequested(() => {
                            clearTimeout(timer);
                            resolve(null);
                        });
                    });
                }
            });
        }

        function registerAIInlineCompletionsProvider() {
            const langId = getLanguage('{{.Ext}}');
            monaco.languages.registerInlineCompletionsProvider(langId, {
                provideInlineCompletions: function(model, position, context, token) {
                    return new Promise((resolve) => {
                        const timer = setTimeout(() => {
                            if (token.isCancellationRequested) {
                                resolve(null);
                                return;
                            }
                            const textBefore = model.getValueInRange({
                                startLineNumber: Math.max(1, position.lineNumber - 50),
                                startColumn: 1,
                                endLineNumber: position.lineNumber,
                                endColumn: position.column
                            });
                            if (textBefore.trim().length < 5) {
                                resolve(null);
                                return;
                            }
                            fetch('/ai-editor-complete', {
                                method: 'POST',
                                headers: { 'Content-Type': 'application/json' },
                                body: JSON.stringify({
                                    before: textBefore,
                                    lang: langId
                                })
                            })
                            .then(r => r.json())
                            .then(data => {
                                if (token.isCancellationRequested) {
                                    resolve(null);
                                    return;
                                }
                                if (data && data.completion) {
                                    resolve({
                                        items: [{
                                            insertText: data.completion,
                                            range: new monaco.Range(position.lineNumber, position.column, position.lineNumber, position.column)
                                        }]
                                    });
                                } else {
                                    resolve(null);
                                }
                            })
                            .catch(() => resolve(null));
                        }, 800);
                        token.onCancellationRequested(() => {
                            clearTimeout(timer);
                            resolve(null);
                        });
                    });
                },
                freeInlineCompletions: function() {}
            });
        }

        function getLanguage(ext) {
            switch(ext.toLowerCase()) {
                case '.go': return 'go';
                case '.js': return 'javascript';
                case '.ts': return 'typescript';
                case '.json': return 'json';
                case '.html': return 'html';
                case '.css': return 'css';
                case '.md': return 'markdown';
                case '.ps1': return 'powershell';
                case '.py': return 'python';
                case '.sh': return 'shell';
                case '.xml': return 'xml';
                case '.yaml': return 'yaml';
                case '.yml': return 'yaml';
                case '.sql': return 'sql';
                default: return 'plaintext';
            }
        }

        function saveContent(val) {
            fetch('/save', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ content: val })
            }).then(res => {
                if (res.ok) {
                    const status = document.getElementById('status');
                    status.style.opacity = '1';
                    setTimeout(() => status.style.opacity = '0', 2000);
                }
            });
        }

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

        window.addEventListener("beforeunload", function (e) {
             fetch('/close', {method: 'POST'});
        });
    </script>
</body>
</html>
`

const diffTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Invoke Diff - {{.FileName}}</title>
    <link rel="icon" type="image/png" href="/web/favicon.png" />
    <style>
        body { margin: 0; padding: 0; overflow: hidden; background-color: #000000; color: #d4d4d4; font-family: 'Segoe UI', sans-serif; }
        #header { height: 38px; background: #000000; display: flex; align-items: center; padding: 0 16px; justify-content: space-between; border-bottom: 1px solid #1a1a1a; }
        #header .info { display: flex; align-items: center; gap: 12px; }
        #header .info .name { font-weight: 600; color: #e2e2e2; }
        #header .info .sub { color: #666; font-size: 0.85em; }
        #header .legend { font-size: 0.8em; color: #888; }
        #header .legend b { color: #a855f7; } #header .legend i { color: #bf616a; font-style: normal; }
        #header .status { color: #a855f7; font-size: 0.9em; opacity: 0; transition: opacity 0.3s; font-weight: 600; }
        #container { height: calc(100vh - 38px); width: 100%; background-color: #000000; }
    </style>
    <script src="/web/vs/loader.js"></script>
</head>
<body>
    <div id="header">
        <div class="info">
            <span class="name">{{.FileName}}</span>
            <span class="sub">diff vs HEAD &middot; left = committed, right = working (editable)</span>
        </div>
        <span class="legend"><b>+ added</b>&nbsp;&nbsp;<i>- removed</i>&nbsp;&nbsp;Ctrl+S to save</span>
        <span class="status" id="status">Saved!</span>
    </div>
    <div id="container"></div>
    <script>
        const originalContent = {{.Original}};
        const modifiedContent = {{.Modified}};
        require.config({ paths: { 'vs': '/web/vs' }});
        require(['vs/editor/editor.main'], function() {
            function getLanguage(ext) {
                switch(ext.toLowerCase()) {
                    case '.go': return 'go'; case '.js': return 'javascript'; case '.ts': return 'typescript';
                    case '.json': return 'json'; case '.html': return 'html'; case '.css': return 'css';
                    case '.md': return 'markdown'; case '.ps1': return 'powershell'; case '.py': return 'python';
                    case '.sh': return 'shell'; case '.xml': return 'xml'; case '.yaml': case '.yml': return 'yaml';
                    case '.sql': return 'sql'; case '.c': case '.h': return 'c'; case '.cpp': return 'cpp';
                    case '.rs': return 'rust'; case '.java': return 'java'; default: return 'plaintext';
                }
            }
            
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

            const lang = getLanguage('{{.Ext}}');
            const original = monaco.editor.createModel(originalContent, lang);
            const modified = monaco.editor.createModel(modifiedContent, lang);
            const diffEditor = monaco.editor.createDiffEditor(document.getElementById('container'), {
                theme: 'badass-black', automaticLayout: true, originalEditable: false,
                renderSideBySide: true, minimap: { enabled: true }, ignoreTrimWhitespace: false
            });
            diffEditor.setModel({ original: original, modified: modified });

            diffEditor.getModifiedEditor().addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, function() {
                fetch('/save', {
                    method: 'POST', headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ content: diffEditor.getModifiedEditor().getValue() })
                }).then(res => {
                    if (res.ok) {
                        const s = document.getElementById('status');
                        s.style.opacity = '1'; setTimeout(() => s.style.opacity = '0', 2000);
                    }
                });
            });

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

        window.addEventListener("beforeunload", function () { fetch('/close', {method: 'POST'}); });
    </script>
</body>
</html>
`

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
		t, err := template.New("diff").Parse(diffTemplate)
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

func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = exec.Command("xdg-open", url).Start()
	}
	if err != nil {
		fmt.Println("Please open this URL in your browser:", url)
	}
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
		t, err := template.New("editor").Parse(editorTemplate)
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
