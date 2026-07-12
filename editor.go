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
