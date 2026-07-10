package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type SidebarFile struct {
	Name  string `json:"name"`
	IsDir bool   `json:"dir"`
}

type SidebarTask struct {
	Name string `json:"name"`
	Cmd  string `json:"cmd"`
}

type SidebarResponse struct {
	Dir   string        `json:"dir"`
	Files []SidebarFile `json:"files"`
	Tasks []SidebarTask `json:"tasks"`
}

// handleSidebarInfo scans the CWD for files and developer tasks.
func handleSidebarInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}

	res := SidebarResponse{
		Dir:   absDir,
		Files: []SidebarFile{},
		Tasks: []SidebarTask{},
	}

	// 1. Scan files
	entries, err := os.ReadDir(absDir)
	if err == nil {
		for _, e := range entries {
			res.Files = append(res.Files, SidebarFile{
				Name:  e.Name(),
				IsDir: e.IsDir(),
			})
		}
	}

	// 2. Scan developer scripts / tasks
	// package.json
	packageJsonPath := filepath.Join(absDir, "package.json")
	if f, err := os.Open(packageJsonPath); err == nil {
		defer f.Close()
		var pData struct {
			Scripts map[string]string `json:"scripts"`
		}
		dec := json.NewDecoder(f)
		if err := dec.Decode(&pData); err == nil {
			for name := range pData.Scripts {
				res.Tasks = append(res.Tasks, SidebarTask{
					Name: "npm run " + name,
					Cmd:  "npm run " + name,
				})
			}
		}
	}

	// go.mod
	goModPath := filepath.Join(absDir, "go.mod")
	if _, err := os.Stat(goModPath); err == nil {
		res.Tasks = append(res.Tasks, SidebarTask{Name: "go run .", Cmd: "go run ."})
		res.Tasks = append(res.Tasks, SidebarTask{Name: "go test", Cmd: "go test ./..."})
		res.Tasks = append(res.Tasks, SidebarTask{Name: "go build", Cmd: "go build"})
	}

	// Makefile
	makefileJsonPath := filepath.Join(absDir, "Makefile")
	if f, err := os.Open(makefileJsonPath); err == nil {
		defer f.Close()
		b, _ := io.ReadAll(f)
		content := string(b)
		
		// Regex to find Makefile targets (e.g. build: clean)
		re := regexp.MustCompile(`^[a-zA-Z0-9_-]+:`)
		lines := strings.Split(content, "\n")
		for _, l := range lines {
			if re.MatchString(l) {
				target := strings.TrimSpace(strings.Split(l, ":")[0])
				if target != ".PHONY" && target != "all" {
					res.Tasks = append(res.Tasks, SidebarTask{
						Name: "make " + target,
						Cmd:  "make " + target,
					})
				}
			}
		}
	}

	// requirements.txt
	requirementsPath := filepath.Join(absDir, "requirements.txt")
	if _, err := os.Stat(requirementsPath); err == nil {
		res.Tasks = append(res.Tasks, SidebarTask{
			Name: "pip install requirements",
			Cmd:  "pip install -r requirements.txt",
		})
	}

	// Cargo.toml
	cargoPath := filepath.Join(absDir, "Cargo.toml")
	if _, err := os.Stat(cargoPath); err == nil {
		res.Tasks = append(res.Tasks, SidebarTask{Name: "cargo run", Cmd: "cargo run"})
		res.Tasks = append(res.Tasks, SidebarTask{Name: "cargo test", Cmd: "cargo test"})
		res.Tasks = append(res.Tasks, SidebarTask{Name: "cargo build", Cmd: "cargo build"})
	}

	json.NewEncoder(w).Encode(res)
}
