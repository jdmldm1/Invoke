//go:build windows

package main

import (
	"golang.org/x/sys/windows/registry"
	"strconv"
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
