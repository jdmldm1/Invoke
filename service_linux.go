//go:build !windows

package main

import (
	"fmt"
)

func checkServiceAndRun() bool {
	return false
}

func installService(name, desc string) error {
	return fmt.Errorf("service installation not supported on linux")
}

func removeService(name string) error {
	return fmt.Errorf("service removal not supported on linux")
}

func runService(name string, isDebug bool) {
	fmt.Println("service mode not supported on linux")
}
