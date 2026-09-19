package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName = "invoke_session"
	sessionTTL        = 24 * time.Hour

	pbkdf2Iterations = 200000
	pbkdf2KeyLen     = 32

	maxLoginAttempts   = 5
	loginAttemptWindow = 15 * time.Minute
)

var (
	listenerMu           sync.Mutex
	httpListener         net.Listener
	httpHandler          http.Handler
	networkAccessEnabled bool

	sessionMu sync.Mutex
	sessions  = map[string]time.Time{}

	loginAttemptsMu sync.Mutex
	loginAttempts   = map[string][]time.Time{}
)

func pbkdf2HMACSHA256(password, salt []byte, iterations, keyLen int) []byte {
	prf := func(key, data []byte) []byte {
		h := hmac.New(sha256.New, key)
		h.Write(data)
		return h.Sum(nil)
	}

	hashLen := sha256.Size
	numBlocks := (keyLen + hashLen - 1) / hashLen
	derived := make([]byte, 0, numBlocks*hashLen)

	block := make([]byte, len(salt)+4)
	copy(block, salt)

	for i := 1; i <= numBlocks; i++ {
		binary.BigEndian.PutUint32(block[len(salt):], uint32(i))
		u := prf(password, block)
		t := append([]byte(nil), u...)
		for iter := 1; iter < iterations; iter++ {
			u = prf(password, u)
			for k := range t {
				t[k] ^= u[k]
			}
		}
		derived = append(derived, t...)
	}

	return derived[:keyLen]
}

func hashPassword(password, salt string) string {
	derived := pbkdf2HMACSHA256([]byte(password), []byte(salt), pbkdf2Iterations, pbkdf2KeyLen)
	return hex.EncodeToString(derived)
}

func generateSalt() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func checkPassword(password, salt, wantHash string) bool {
	if wantHash == "" {
		return false
	}
	got := hashPassword(password, salt)
	return subtle.ConstantTimeCompare([]byte(got), []byte(wantHash)) == 1
}

func newSessionToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func createSession() string {
	tok := newSessionToken()
	now := time.Now()

	sessionMu.Lock()
	defer sessionMu.Unlock()
	for existing, exp := range sessions {
		if now.After(exp) {
			delete(sessions, existing)
		}
	}
	sessions[tok] = now.Add(sessionTTL)
	return tok
}

func validSession(token string) bool {
	if token == "" {
		return false
	}
	sessionMu.Lock()
	defer sessionMu.Unlock()
	exp, ok := sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(sessions, token)
		return false
	}
	return true
}

func loginAttemptKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func tooManyLoginAttempts(remoteAddr string) bool {
	key := loginAttemptKey(remoteAddr)
	cutoff := time.Now().Add(-loginAttemptWindow)

	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()

	kept := loginAttempts[key][:0]
	for _, t := range loginAttempts[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	loginAttempts[key] = kept
	return len(kept) >= maxLoginAttempts
}

func recordFailedLogin(remoteAddr string) {
	key := loginAttemptKey(remoteAddr)
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()
	loginAttempts[key] = append(loginAttempts[key], time.Now())
}

func clearLoginAttempts(remoteAddr string) {
	key := loginAttemptKey(remoteAddr)
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()
	delete(loginAttempts, key)
}

func isLoopbackAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func localLANAddresses() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil {
			continue
		}
		out = append(out, ip4.String())
	}
	return out
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func serveRemoteLoginPage(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	errHTML := ""
	if errMsg != "" {
		errHTML = `<div style="color:#c4756e;margin-bottom:12px;font-size:13px">` + html.EscapeString(errMsg) + `</div>`
	}
	fmt.Fprintf(w, `<!doctype html><html><head><title>Invoke - Remote Access</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
body{background:#141414;color:#e2e2e2;font-family:Segoe UI,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
form{background:#1c1c1c;padding:32px;border-radius:8px;min-width:280px;border:1px solid #2a2a2a}
h1{font-size:16px;margin:0 0 8px;font-weight:600}
p.sub{font-size:12px;color:#888;margin:0 0 16px}
input{width:100%%;padding:9px;margin-bottom:12px;background:#141414;border:1px solid #333;color:#e2e2e2;border-radius:4px;box-sizing:border-box;font-size:14px}
button{width:100%%;padding:9px;background:#0ea5e9;border:none;color:#fff;border-radius:4px;cursor:pointer;font-size:14px}
</style></head><body>
<form method="POST" action="/remote-login">
<h1>Invoke &mdash; Remote Access</h1>
<p class="sub">Enter the network access password</p>
%s
<input type="password" name="password" placeholder="Password" autofocus>
<button type="submit">Unlock</button>
</form></body></html>`, errHTML)
}

func handleRemoteLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		serveRemoteLoginPage(w, "")
		return
	}
	if tooManyLoginAttempts(r.RemoteAddr) {
		serveRemoteLoginPage(w, "Too many attempts. Try again later.")
		return
	}
	if err := r.ParseForm(); err != nil {
		serveRemoteLoginPage(w, "Bad request")
		return
	}
	password := r.FormValue("password")

	cfg := loadConfig()
	if !checkPassword(password, cfg.NetworkPasswordSalt, cfg.NetworkPasswordHash) {
		recordFailedLogin(r.RemoteAddr)
		serveRemoteLoginPage(w, "Incorrect password")
		return
	}
	clearLoginAttempts(r.RemoteAddr)

	token := createSession()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func localAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !networkAccessEnabled {
			origin := r.Header.Get("Origin")
			if origin != "" {
				u, err := url.Parse(origin)
				if err != nil || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") {
					http.Error(w, "Forbidden Origin", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func networkAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !networkAccessEnabled || isLoopbackAddr(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/web/xterm.min.js" || r.URL.Path == "/web/xterm.min.css" || r.URL.Path == "/web/xterm-addon-fit.min.js" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/cast" || r.URL.Path == "/ws/view" || strings.HasPrefix(r.URL.Path, "/tunnel/route/") {
			sessionID := r.URL.Query().Get("session")
			if sessionID != "" {
				activeSessionsMu.Lock()
				_, exists := activeSessions[sessionID]
				activeSessionsMu.Unlock()
				if exists {
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		if r.URL.Path == "/remote-login" {
			handleRemoteLogin(w, r)
			return
		}
		if cookie, err := r.Cookie(sessionCookieName); err == nil && validSession(cookie.Value) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			serveRemoteLoginPage(w, "")
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func handleNetworkAccess(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config := loadConfig()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"enabled":     config.NetworkAccess,
			"hasPassword": config.NetworkPasswordHash != "",
			"port":        serverPort,
			"addresses":   localLANAddresses(),
		})

	case http.MethodPost:
		var req struct {
			Enabled  bool   `json:"enabled"`
			Password string `json:"password"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeJSONError(w, 400, "bad request")
			return
		}
		config := loadConfig()
		if req.Enabled {
			if req.Password != "" {
				config.NetworkPasswordSalt = generateSalt()
				config.NetworkPasswordHash = hashPassword(req.Password, config.NetworkPasswordSalt)
			}
			if config.NetworkPasswordHash == "" {
				writeJSONError(w, 400, "a password is required to enable network access")
				return
			}
		}
		config.NetworkAccess = req.Enabled
		saveConfig(config)
		if err := rebindNetworkAccess(req.Enabled); err != nil {
			writeJSONError(w, 500, "failed to rebind server: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"enabled":   config.NetworkAccess,
			"port":      serverPort,
			"addresses": localLANAddresses(),
		})

	default:
		writeJSONError(w, 405, "method not allowed")
	}
}

func setNetworkPasswordCLI(password string) {
	if strings.TrimSpace(password) == "" {
		fmt.Println("Password must not be empty.")
		return
	}

	config := loadConfig()
	config.NetworkPasswordSalt = generateSalt()
	config.NetworkPasswordHash = hashPassword(password, config.NetworkPasswordSalt)
	config.NetworkAccess = true
	saveConfig(config)

	fmt.Println("Network access password set. Remote logins now require this password.")
}

func rebindNetworkAccess(enabled bool) error {
	listenerMu.Lock()
	defer listenerMu.Unlock()

	host := "127.0.0.1"
	if enabled {
		host = "0.0.0.0"
	}
	addr := fmt.Sprintf("%s:%d", host, serverPort)

	if httpListener != nil {
		httpListener.Close()
		httpListener = nil
	}
	newListener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	httpListener = newListener
	networkAccessEnabled = enabled
	go serveHTTP(newListener, httpHandler)
	return nil
}
