package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type FSNode struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	IsDir    bool     `json:"dir"`
	Children []FSNode `json:"children,omitempty"`
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
