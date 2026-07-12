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
