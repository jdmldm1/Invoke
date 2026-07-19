package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type SSHEndpoint struct {
	Name        string `json:"name"`
	Host        string `json:"host"`
	User        string `json:"user"`
	Port        int    `json:"port"`
	UseKey      bool   `json:"use_key"`
	EncPassword string `json:"enc_password,omitempty"`
	AutoSudo    bool   `json:"auto_sudo,omitempty"`
}

type FSNode struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	IsDir    bool     `json:"dir"`
	Children []FSNode `json:"children,omitempty"`
}

func sshMachineKey() []byte {
	hostname, _ := os.Hostname()
	salt := "powerterm-ssh-v1:" + hostname
	sum := sha256.Sum256([]byte(salt))
	return sum[:]
}

func encryptSSHPassword(plaintext string) string {
	if plaintext == "" {
		return ""
	}
	key := sshMachineKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return ""
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed)
}

func decryptSSHPassword(enc string) string {
	if enc == "" {
		return ""
	}
	data, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return ""
	}
	key := sshMachineKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	if len(data) < gcm.NonceSize() {
		return ""
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return ""
	}
	return string(plain)
}

func handleSSHEndpoints(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		cfg := loadConfig()
		safe := make([]SSHEndpoint, len(cfg.SSHEndpoints))
		copy(safe, cfg.SSHEndpoints)
		for i := range safe {
			safe[i].EncPassword = ""
		}
		_ = json.NewEncoder(w).Encode(safe)
		return
	}

	if r.Method == http.MethodPost {
		var ep SSHEndpoint
		if err := json.NewDecoder(r.Body).Decode(&ep); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if ep.Port == 0 {
			ep.Port = 22
		}
		if ep.EncPassword != "" {
			ep.EncPassword = encryptSSHPassword(ep.EncPassword)
		}
		cfg := loadConfig()
		cfg.SSHEndpoints = append(cfg.SSHEndpoints, ep)
		saveConfig(cfg)
		w.WriteHeader(200)
		return
	}

	if r.Method == http.MethodDelete {
		var req struct {
			Index int `json:"index"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		cfg := loadConfig()
		if req.Index < 0 || req.Index >= len(cfg.SSHEndpoints) {
			http.Error(w, "index out of range", 400)
			return
		}
		cfg.SSHEndpoints = append(cfg.SSHEndpoints[:req.Index], cfg.SSHEndpoints[req.Index+1:]...)
		saveConfig(cfg)
		w.WriteHeader(200)
		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func handleSSHConnect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Index int `json:"index"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}

	cfg := loadConfig()
	if req.Index < 0 || req.Index >= len(cfg.SSHEndpoints) {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "endpoint not found"})
		return
	}

	ep := cfg.SSHEndpoints[req.Index]
	port := ep.Port
	if port == 0 {
		port = 22
	}

	var cmd string
	if ep.UseKey {
		cmd = "ssh " + ep.User + "@" + ep.Host
		if port != 22 {
			cmd = "ssh -p " + itoa(port) + " " + ep.User + "@" + ep.Host
		}
	} else {
		password := decryptSSHPassword(ep.EncPassword)
		if password != "" {
			cmd = "ssh " + ep.User + "@" + ep.Host
			if port != 22 {
				cmd = "ssh -p " + itoa(port) + " " + ep.User + "@" + ep.Host
			}
		} else {
			cmd = "ssh " + ep.User + "@" + ep.Host
			if port != 22 {
				cmd = "ssh -p " + itoa(port) + " " + ep.User + "@" + ep.Host
			}
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"cmd":       cmd,
		"password":  decryptSSHPassword(ep.EncPassword),
		"auto_sudo": ep.AutoSudo,
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

func handleFSTree(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	root := r.URL.Query().Get("dir")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = home
	}

	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		_ = json.NewEncoder(w).Encode(FSNode{Name: filepath.Base(root), Path: root, IsDir: false})
		return
	}

	node := buildFSNode(root, 0)
	_ = json.NewEncoder(w).Encode(node)
}

func buildFSNode(path string, depth int) FSNode {
	name := filepath.Base(path)
	node := FSNode{Name: name, Path: path, IsDir: true}

	if depth >= 2 {
		return node
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return node
	}

	skipDirs := map[string]bool{
		".git": true, "node_modules": true, ".gemini": true,
		"dist": true, "build": true, "bin": true, "obj": true, "__pycache__": true,
	}

	var dirs, files []FSNode
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && e.Name() != ".." {
			continue
		}
		childPath := filepath.Join(path, e.Name())
		if e.IsDir() {
			if skipDirs[e.Name()] {
				continue
			}
			dirs = append(dirs, FSNode{Name: e.Name(), Path: childPath, IsDir: true})
		} else {
			files = append(files, FSNode{Name: e.Name(), Path: childPath, IsDir: false})
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	node.Children = append(dirs, files...)
	return node
}

func handleSCPPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	htmlBytes, err := webFS.ReadFile("web/scp.html")
	if err != nil {
		http.Error(w, "SCP template not found", 404)
		return
	}
	w.Write(htmlBytes)
}
