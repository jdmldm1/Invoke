package main

import "strings"

// cleanHost ensures an Ollama host string carries an http(s):// scheme.
func cleanHost(host string) string {
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		return "http://" + host
	}
	return host
}
