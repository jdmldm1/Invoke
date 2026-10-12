package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsLocalHostHeader(t *testing.T) {
	local := []string{"localhost", "localhost:8080", "127.0.0.1", "127.0.0.1:4321", "[::1]:9000", "[::1]"}
	remote := []string{"", "evil.com", "evil.com:8080", "localhost.evil.com", "127.0.0.1.evil.com", "192.168.1.5:80", "example.com"}
	for _, h := range local {
		if !isLocalHostHeader(h) {
			t.Errorf("%q should be local", h)
		}
	}
	for _, h := range remote {
		if isLocalHostHeader(h) {
			t.Errorf("%q should not be local", h)
		}
	}
}

func TestIsLocalOrigin(t *testing.T) {
	if !isLocalOrigin("http://localhost:3000") || !isLocalOrigin("http://127.0.0.1") {
		t.Error("loopback origins should be accepted")
	}
	for _, o := range []string{"https://evil.com", "http://localhost.evil.com", "null", "://bad"} {
		if isLocalOrigin(o) {
			t.Errorf("origin %q should be rejected", o)
		}
	}
}

func TestLocalAuthMiddleware(t *testing.T) {
	old := networkAccessEnabled
	defer func() { networkAccessEnabled = old }()

	handler := localAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	do := func(host, origin string) int {
		req := httptest.NewRequest(http.MethodGet, "/config", nil)
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	networkAccessEnabled = false
	cases := []struct {
		name         string
		host, origin string
		want         int
	}{
		{"local host, no origin", "localhost:8080", "", http.StatusOK},
		{"local host, local origin", "127.0.0.1:8080", "http://localhost:8080", http.StatusOK},
		{"dns rebinding: foreign host", "evil.com:8080", "", http.StatusForbidden},
		{"cross-site request", "localhost:8080", "https://evil.com", http.StatusForbidden},
	}
	for _, c := range cases {
		if got := do(c.host, c.origin); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}

	// With network access on, the password gate (networkAuthMiddleware) protects the server and
	// the host may be any LAN name.
	networkAccessEnabled = true
	if got := do("shiloh:8080", ""); got != http.StatusOK {
		t.Errorf("network mode: got %d, want 200", got)
	}
}
