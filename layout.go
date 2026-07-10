package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LayoutPane describes one terminal pane in a layout.
type LayoutPane struct {
	CWD string `json:"cwd"`
}

// LayoutTab describes one tab in a saved layout.
type LayoutTab struct {
	Name  string       `json:"name"`
	Panes []LayoutPane `json:"panes"`
	Split string       `json:"split,omitempty"` // "row" | "col" | ""
}

// Layout is one complete named workspace snapshot.
type Layout struct {
	Name      string      `json:"name"`
	SavedAt   time.Time   `json:"saved_at"`
	Tabs      []LayoutTab `json:"tabs"`
	ActiveTab int         `json:"active_tab"`
}

var (
	layoutMu   sync.Mutex
	layoutPath string
)

func initLayouts() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	layoutPath = filepath.Join(home, ".powerterm_layouts.json")
}

func loadLayouts() []Layout {
	layoutMu.Lock()
	defer layoutMu.Unlock()
	data, err := os.ReadFile(layoutPath)
	if err != nil {
		return []Layout{}
	}
	var layouts []Layout
	if err := json.Unmarshal(data, &layouts); err != nil {
		return []Layout{}
	}
	return layouts
}

func saveLayouts(layouts []Layout) {
	layoutMu.Lock()
	defer layoutMu.Unlock()
	data, _ := json.MarshalIndent(layouts, "", "  ")
	if err := os.WriteFile(layoutPath, data, 0644); err != nil {
		log.Printf("Failed to save layouts: %v", err)
	}
}

// handleLayout handles GET/POST/DELETE for named layouts.
func handleLayout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		layouts := loadLayouts()
		json.NewEncoder(w).Encode(layouts)

	case http.MethodPost:
		var layout Layout
		if err := json.NewDecoder(r.Body).Decode(&layout); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		layout.SavedAt = time.Now()
		layouts := loadLayouts()
		replaced := false
		for i, l := range layouts {
			if l.Name == layout.Name {
				layouts[i] = layout
				replaced = true
				break
			}
		}
		if !replaced {
			layouts = append(layouts, layout)
		}
		saveLayouts(layouts)
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "name required", 400)
			return
		}
		layouts := loadLayouts()
		filtered := layouts[:0]
		for _, l := range layouts {
			if l.Name != name {
				filtered = append(filtered, l)
			}
		}
		saveLayouts(filtered)
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})

	default:
		http.Error(w, "method not allowed", 405)
	}
}
