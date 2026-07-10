package main

import (
	"encoding/json"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// Control-channel connections receive "open tab" pushes from the server.
var (
	ctrlMu    sync.Mutex
	ctrlConns = map[*websocket.Conn]bool{}
)

func broadcastControl(v interface{}) {
	b, _ := json.Marshal(v)
	ctrlMu.Lock()
	defer ctrlMu.Unlock()
	for c := range ctrlConns {
		_ = c.WriteMessage(websocket.TextMessage, b)
	}
}

func handleControlWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ctrlMu.Lock()
	ctrlConns[conn] = true
	ctrlMu.Unlock()
	for {
		if _, _, e := conn.ReadMessage(); e != nil {
			break
		}
	}
	ctrlMu.Lock()
	delete(ctrlConns, conn)
	ctrlMu.Unlock()
	conn.Close()
}

// handleOpenRequest is POSTed by `pt diff`/`pt edit` running inside a pane.
func handleOpenRequest(w http.ResponseWriter, r *http.Request) {
	var req struct{ Type, File string }
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.File == "" {
		http.Error(w, "bad request", 400)
		return
	}
	abs, err := filepath.Abs(req.File)
	if err != nil {
		abs = req.File
	}
	typ := "diff"
	if req.Type == "edit" {
		typ = "edit"
	} else if req.Type == "git" {
		typ = "git"
	}
	broadcastControl(map[string]string{
		"action": "openTab", "type": typ, "file": abs, "name": filepath.Base(abs),
	})
	w.WriteHeader(200)
}

func readFileOrEmpty(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func handleDiffRoute(w http.ResponseWriter, r *http.Request) {
	abs, _ := filepath.Abs(r.URL.Query().Get("file"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(monacoDiffHTML(filepath.Base(abs), filepath.Ext(abs), gitFileAtHead(abs), readFileOrEmpty(abs))))
}

func handleEditRoute(w http.ResponseWriter, r *http.Request) {
	abs, _ := filepath.Abs(r.URL.Query().Get("file"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(monacoEditHTML(filepath.Base(abs), filepath.Ext(abs), abs, readFileOrEmpty(abs))))
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

// monacoDiffHTML renders a read-only side-by-side diff page (HEAD vs working).
func monacoDiffHTML(name, ext, original, modified string) string {
	o, _ := json.Marshal(original) // json.Marshal HTML-escapes <,>,& so embedding in <script> is safe
	m, _ := json.Marshal(modified)
	l, _ := json.Marshal(langForExt(ext))
	return fill(monacoDiffTmpl, map[string]string{
		"__NAME__":     template.HTMLEscapeString(name),
		"__ORIGINAL__": string(o),
		"__MODIFIED__": string(m),
		"__LANG__":     string(l),
	})
}

// monacoEditHTML renders an editable Monaco page; Ctrl+S saves back to absPath.
func monacoEditHTML(name, ext, absPath, content string) string {
	c, _ := json.Marshal(content)
	l, _ := json.Marshal(langForExt(ext))
	a, _ := json.Marshal(absPath)
	return fill(monacoEditTmpl, map[string]string{
		"__NAME__":    template.HTMLEscapeString(name),
		"__CONTENT__": string(c),
		"__LANG__":    string(l),
		"__ABS__":     string(a),
	})
}

const monacoDiffTmpl = `<!DOCTYPE html><html><head><meta charset="utf-8"><title>__NAME__</title>
<style>
 html,body{margin:0;height:100%;background:#000000;font-family:'Segoe UI',sans-serif}
 #h{height:30px;background:#000000;color:#e2e2e2;display:flex;align-items:center;gap:10px;padding:0 12px;font-size:13px;border-bottom:1px solid #1a1a1a}
 #h b{color:#a855f7}
 #h .leg{margin-left:auto;color:#888;font-size:12px}
 #c{height:calc(100vh - 30px);background-color:#000000}
</style>
<script src="/web/vs/loader.js"></script>
</head><body>
<div id="h"><b>__NAME__</b><span>diff vs HEAD</span><span class="leg">left = HEAD, right = working copy</span></div>
<div id="c"></div>
<script>
 const orig=__ORIGINAL__, mod=__MODIFIED__, lang=__LANG__;
 require.config({paths:{vs:'/web/vs'}});
 require(['vs/editor/editor.main'],function(){
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
   const de=monaco.editor.createDiffEditor(document.getElementById('c'),
     {theme:'badass-black',automaticLayout:true,readOnly:true,originalEditable:false,renderSideBySide:true,minimap:{enabled:true}});
   de.setModel({original:monaco.editor.createModel(orig,lang),modified:monaco.editor.createModel(mod,lang)});
   
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
</script></body></html>`

const monacoEditTmpl = `<!DOCTYPE html><html><head><meta charset="utf-8"><title>__NAME__</title>
<style>
 html,body{margin:0;height:100%;background:#000000;font-family:'Segoe UI',sans-serif;overflow:hidden}
 #h{height:30px;background:#000000;color:#e2e2e2;display:flex;align-items:center;gap:10px;padding:0 12px;font-size:13px;border-bottom:1px solid #1a1a1a}
 #h b{color:#a855f7}
 #h .leg{margin-left:auto;color:#888;font-size:12px}
 #st{color:#a855f7;opacity:0;transition:opacity .3s;font-weight:600;font-size:12px}
 #c{height:calc(100vh - 30px);background-color:#000000}
</style>
<script src="/web/vs/loader.js"></script>
</head><body>
<div id="h"><b>__NAME__</b><span id="st">Saved</span></div>
<div id="c"></div>
 
<div id="ai-widget" style="position:fixed;display:none;z-index:9999;background:#050505;border:1px solid #222;border-radius:6px;padding:8px;box-shadow:0 8px 24px rgba(0,0,0,0.8);width:300px;font-family:sans-serif">
  <input type="text" id="ai-prompt" placeholder="Instruct AI (e.g., 'add error handling')..." style="width:100%;box-sizing:border-box;background:#121212;color:#fff;border:1px solid #333;border-radius:4px;padding:6px;font-size:12px;outline:none;margin-bottom:6px">
  <div style="display:flex;justify-content:flex-end;gap:6px">
    <button id="ai-cancel" style="background:#222;color:#aaa;border:1px solid #333;border-radius:4px;padding:4px 8px;font-size:11px;cursor:pointer">Cancel</button>
    <button id="ai-ok" style="background:#a855f7;color:#fff;border:none;border-radius:4px;padding:4px 8px;font-size:11px;cursor:pointer;font-weight:bold">Submit</button>
  </div>
</div>
 
<script>
 const content=__CONTENT__, lang=__LANG__, abs=__ABS__;
 require.config({paths:{vs:'/web/vs'}});
 require(['vs/editor/editor.main'],function(){
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

   const ed=monaco.editor.create(document.getElementById('c'),
     {value:content,language:lang,theme:'badass-black',automaticLayout:true,minimap:{enabled:true}});
   
   ed.addCommand(monaco.KeyMod.CtrlCmd|monaco.KeyCode.KeyS,function(){
     fetch('/file-save',{method:'POST',headers:{'Content-Type':'application/json'},
       body:JSON.stringify({file:abs,content:ed.getValue()})})
       .then(r=>{if(r.ok){const s=document.getElementById('st');s.textContent='Saved';s.style.opacity='1';setTimeout(()=>s.style.opacity='0',1500);}});
   });
   
   let saveTimeout = null;
   ed.onDidChangeModelContent(function() {
     if (saveTimeout) clearTimeout(saveTimeout);
     saveTimeout = setTimeout(function() {
       fetch('/file-save',{method:'POST',headers:{'Content-Type':'application/json'},
         body:JSON.stringify({file:abs,content:ed.getValue()})})
         .then(r=>{if(r.ok){const s=document.getElementById('st');s.textContent='Saved';s.style.opacity='1';setTimeout(()=>s.style.opacity='0',1000);}});
     }, 1000);
   });
   
   const urlParams = new URLSearchParams(window.location.search);
   const line = parseInt(urlParams.get('line')) || 1;
   if (line > 1) {
     ed.revealLineInCenter(line);
     ed.setPosition({lineNumber: line, column: 1});
     ed.focus();
   }

   // 4. Comment-to-Code Generator (Ctrl+Space on a comment line)
   ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Space, function() {
     const position = ed.getPosition();
     const lineText = ed.getModel().getLineContent(position.lineNumber);
     const match = lineText.match(/^\s*(#|\/\/|--)\s*(.*)$/);
     if (match) {
       const query = match[2].trim();
       if (query) {
         const s = document.getElementById('st');
         s.textContent = 'AI generating...';
         s.style.opacity = '1';
         
         fetch('/ai-edit-code', {
           method: 'POST',
           headers: {'Content-Type': 'application/json'},
           body: JSON.stringify({
             code: lineText,
             instruction: "Write the code that implements this description: " + query + ". Return ONLY the clean code block, no markdown, no backticks, no explanation.",
             lang: lang
           })
         })
         .then(r => r.json())
         .then(d => {
           s.style.opacity = '0';
           if (d && d.code) {
             const range = new monaco.Range(position.lineNumber, 1, position.lineNumber, lineText.length + 1);
             ed.executeEdits("ai-line-gen", [{
               range: range,
               text: d.code,
               forceMoveMarkers: true
             }]);
             // Save the file automatically after AI generation
             setTimeout(() => {
               fetch('/file-save',{method:'POST',headers:{'Content-Type':'application/json'},
                 body:JSON.stringify({file:abs,content:ed.getValue()})})
                 .then(r=>{if(r.ok){s.textContent='Saved';s.style.opacity='1';setTimeout(()=>s.style.opacity='0',1000);}});
             }, 100);
           }
         })
         .catch(() => {
           s.textContent = 'Error';
           setTimeout(() => s.style.opacity = '0', 2000);
         });
         return;
       }
     }
     
     // Fallback to default suggestions trigger
     ed.trigger('keyboard', 'editor.action.triggerSuggest', {});
   });

   // 1. Monaco Editor Inline AI Copilot (Ctrl+Alt+A)
   ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyMod.Alt | monaco.KeyCode.KeyA, function() {
     const selection = ed.getSelection();
     if (selection.isEmpty()) {
       alert("Please select some code to edit first.");
       return;
     }
     
     const widget = document.getElementById('ai-widget');
     const input = document.getElementById('ai-prompt');
     input.value = '';
     
     const pos = ed.getScrolledVisiblePosition(selection.getStartPosition());
     if (pos) {
       widget.style.top = Math.min(window.innerHeight - 80, Math.max(10, pos.top + 34)) + 'px';
       widget.style.left = Math.min(window.innerWidth - 320, Math.max(10, pos.left)) + 'px';
       widget.style.display = 'block';
       setTimeout(() => input.focus(), 50);
     }
   });

   const submitAIEdit = () => {
     const widget = document.getElementById('ai-widget');
     const promptVal = document.getElementById('ai-prompt').value.trim();
     if (!promptVal) return;
     
     const selection = ed.getSelection();
     const selectedText = ed.getModel().getValueInRange(selection);
     
     widget.style.display = 'none';
     const s = document.getElementById('st');
     s.textContent = 'AI generating...';
     s.style.opacity = '1';
     
     fetch('/ai-edit-code', {
       method: 'POST',
       headers: {'Content-Type': 'application/json'},
       body: JSON.stringify({
         code: selectedText,
         instruction: promptVal,
         lang: lang
       })
     })
     .then(r => r.json())
     .then(d => {
       s.style.opacity = '0';
       if (d && d.code) {
         const range = new monaco.Range(
           selection.startLineNumber,
           selection.startColumn,
           selection.endLineNumber,
           selection.endColumn
         );
         ed.executeEdits("ai-copilot", [{
           range: range,
           text: d.code,
           forceMoveMarkers: true
         }]);
         // Save the file automatically after AI edit
         setTimeout(() => {
           fetch('/file-save',{method:'POST',headers:{'Content-Type':'application/json'},
             body:JSON.stringify({file:abs,content:ed.getValue()})})
             .then(r=>{if(r.ok){s.textContent='Saved';s.style.opacity='1';setTimeout(()=>s.style.opacity='0',1000);}});
         }, 100);
       }
     })
     .catch(() => {
       s.textContent = 'Error';
       setTimeout(() => s.style.opacity = '0', 2000);
     });
   };
   
   document.getElementById('ai-ok').onclick = submitAIEdit;
   document.getElementById('ai-cancel').onclick = () => { document.getElementById('ai-widget').style.display = 'none'; ed.focus(); };
   document.getElementById('ai-prompt').onkeydown = (e) => {
     if (e.key === 'Enter') submitAIEdit();
     if (e.key === 'Escape') { document.getElementById('ai-widget').style.display = 'none'; ed.focus(); }
   };

   // 3. Smart Code Snippets Autocomplete
   fetch('/config')
     .then(r => r.json())
     .then(cfg => {
       const snippets = cfg.snippets || [];
       if (snippets.length > 0) {
         monaco.languages.registerCompletionItemProvider(lang, {
           provideCompletionItems: () => {
             const suggestions = snippets.map(s => {
               let idx = 1;
               let monacoCmd = s.cmd;
               const re = /\{([^}]+)\}/g;
               let m;
               const seen = new Set();
               while ((m = re.exec(s.cmd)) !== null) {
                 if (!seen.has(m[1])) {
                   seen.add(m[1]);
                   monacoCmd = monacoCmd.split('{' + m[1] + '}').join('${' + idx + ':' + m[1] + '}');
                   idx++;
                 }
               }
               
               return {
                 label: s.name,
                 kind: monaco.languages.CompletionItemKind.Snippet,
                 documentation: s.cmd,
                 insertText: monacoCmd,
                 insertTextRules: monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet
               };
             });
             return { suggestions: suggestions };
           }
         });
       }
       registerAIHoverProvider();
     });

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
</script></body></html>`
