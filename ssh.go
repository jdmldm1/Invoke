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
)

type SSHEndpoint struct {
	Name        string `json:"name"`
	Host        string `json:"host"`
	User        string `json:"user"`
	Port        int    `json:"port"`
	UseKey      bool   `json:"use_key"`
	EncPassword string `json:"enc_password,omitempty"`
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

	http.Error(w, "method not allowed", 405)
}

func handleSSHConnect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
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
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "endpoint not found"})
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

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"cmd":      cmd,
		"password": decryptSSHPassword(ep.EncPassword),
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
