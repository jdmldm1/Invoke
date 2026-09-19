//go:build !windows

package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zserge/lorca"
)

//go:embed splash.html
var splashHTML string

func main() {
	self, err := os.Executable()
	if err != nil {
		self = "invoke-app"
	}
	dir := filepath.Dir(self)
	base := filepath.Base(self)

	serverExe := filepath.Join(dir, "invoke-server")
	if strings.Contains(base, "desktop") {
		serverExe = filepath.Join(dir, strings.Replace(base, "desktop", "server", 1))
	}

	if _, err := os.Stat(serverExe); err != nil {
		serverExe = filepath.Join(dir, "invoke-server")
		if _, err := os.Stat(serverExe); err != nil {
			serverExe = filepath.Join(dir, "invoke")
			if _, err := os.Stat(serverExe); err != nil {
				serverExe = filepath.Join(dir, "invoke-server-linux-x86")
				if _, err := os.Stat(serverExe); err != nil {
					fmt.Println("Cannot find invoke-server next to invoke-app.")
					os.Exit(1)
				}
			}
		}
	}

	port := 0
	if p := os.Getenv("INVOKE_TERM_PORT"); p != "" {
		if val, err := strconv.Atoi(p); err == nil && val > 0 {
			port = val
		}
	}
	if port == 0 {
		home, err := os.UserHomeDir()
		if err == nil {
			configBytes, err := os.ReadFile(filepath.Join(home, ".invoke.json"))
			if err == nil {
				var cfg struct {
					ServerPort int `json:"server_port"`
				}
				if json.Unmarshal(configBytes, &cfg) == nil && cfg.ServerPort > 0 {
					port = cfg.ServerPort
				}
			}
		}
	}
	if port == 0 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Printf("Could not bind a local port:\n\n%v\n", err)
			os.Exit(1)
		}
		port = l.Addr().(*net.TCPAddr).Port
		l.Close()
	}

	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	cmd := exec.Command(serverExe, "term")
	cmd.Env = append(os.Environ(), fmt.Sprintf("INVOKE_TERM_PORT=%d", port), "INVOKE_NO_WINDOW=1")
	if err := cmd.Start(); err != nil {
		fmt.Printf("Could not start invoke-server:\n\n%v\n", err)
		os.Exit(1)
	}
	defer cmd.Process.Kill()

	waitForServer(url)

	ui, err := lorca.New(url, "", 1280, 820, "--remote-allow-origins=*")
	if err != nil {
		fmt.Printf("Failed to launch UI via Lorca (Chrome/Chromium required): %v\n", err)
		exec.Command("xdg-open", url).Run()
		cmd.Wait()
		return
	}
	defer ui.Close()

	<-ui.Done()
}

func waitForServer(url string) bool {
	for i := 0; i < 100; i++ {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
