//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/windows/registry"
)

func applySystemConfig(cfg *ConfigData) {
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `Software\Invoke`, registry.READ); err == nil {
		if portStr, _, err := k.GetStringValue("ServerPort"); err == nil {
			if port, err := strconv.Atoi(portStr); err == nil && port > 0 {
				cfg.ServerPort = port
				cfg.NetworkAccess = true
			}
		}
		if hash, _, err := k.GetStringValue("NetworkPasswordHash"); err == nil && hash != "" {
			cfg.NetworkPasswordHash = hash
		}
		if salt, _, err := k.GetStringValue("NetworkPasswordSalt"); err == nil && salt != "" {
			cfg.NetworkPasswordSalt = salt
		}
		k.Close()
	}
}

func saveSystemPassword(hash, salt string) {
	if k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, `Software\Invoke`, registry.SET_VALUE); err == nil {
		k.SetStringValue("NetworkPasswordHash", hash)
		k.SetStringValue("NetworkPasswordSalt", salt)
		k.Close()
	} else {
		if f, fErr := os.OpenFile(filepath.Join(os.TempDir(), `invoke-reg.log`), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666); fErr == nil {
			f.WriteString(fmt.Sprintf("Failed to open/create registry key: %v\n", err))
			f.Close()
		}
	}
}
