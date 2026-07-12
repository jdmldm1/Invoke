package main

import "strings"

func cleanHost(host string) string {
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		return "http://" + host
	}
	return host
}
