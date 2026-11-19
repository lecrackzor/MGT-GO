//go:build !windows

package main

import "fmt"

func syncAutoStartTask(enabled bool) error {
	if !enabled {
		return nil
	}
	return fmt.Errorf("start before the open is only available on Windows")
}
