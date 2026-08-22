package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigLoadAndSave(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "invoke_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldConfigPath := configPath
	configPath = filepath.Join(tempDir, ".invoke_test.json")
	defer func() { configPath = oldConfigPath }()

	cfg := loadConfig()
	if cfg.OllamaHost == "" {
		t.Errorf("expected default OllamaHost, got empty string")
	}
	if len(cfg.Snippets) == 0 {
		t.Errorf("expected default snippets, got empty list")
	}

	cfg.OllamaHost = "http://test-host:11434"
	cfg.OllamaModel = "test-model"
	saveConfig(cfg)

	loaded := loadConfig()
	if loaded.OllamaHost != "http://test-host:11434" {
		t.Errorf("expected OllamaHost 'http://test-host:11434', got '%s'", loaded.OllamaHost)
	}
	if loaded.OllamaModel != "test-model" {
		t.Errorf("expected OllamaModel 'test-model', got '%s'", loaded.OllamaModel)
	}
}

func TestPromptManagement(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "invoke_prompt_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldConfigPath := configPath
	configPath = filepath.Join(tempDir, ".invoke_test.json")
	defer func() { configPath = oldConfigPath }()

	addPrompt("Test Prompt", "Template for {test}")

	cfg := loadConfig()
	found := false
	for _, p := range cfg.Prompts {
		if p.Name == "Test Prompt" && p.Template == "Template for {test}" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'Test Prompt' to be added")
	}

	deletePrompt(len(cfg.Prompts) - 1)
	cfgAfter := loadConfig()
	if len(cfgAfter.Prompts) != len(cfg.Prompts)-1 {
		t.Errorf("expected prompt count %d, got %d", len(cfg.Prompts)-1, len(cfgAfter.Prompts))
	}
}

func TestLayoutLoadAndSave(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "invoke_layout_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldLayoutPath := layoutPath
	layoutPath = filepath.Join(tempDir, ".invoke_layouts_test.json")
	defer func() { layoutPath = oldLayoutPath }()

	layouts := []Layout{
		{
			Name: "Dev Workspace",
			Tabs: []LayoutTab{
				{
					Name:  "Main Tab",
					Panes: []LayoutPane{{CWD: "C:\\code"}},
				},
			},
		},
	}
	saveLayouts(layouts)

	loaded := loadLayouts()
	if len(loaded) != 1 || loaded[0].Name != "Dev Workspace" {
		t.Errorf("failed to load saved layouts correctly")
	}
}
