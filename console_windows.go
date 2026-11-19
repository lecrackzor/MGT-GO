//go:build windows

package main

import "syscall"

// hideConsoleWindow closes the console that Windows attaches to a console-subsystem
// process. The session log stays in the log file and opens from the Log button.
func hideConsoleWindow() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	hwnd, _, _ := kernel32.NewProc("GetConsoleWindow").Call()
	if hwnd != 0 {
		user32 := syscall.NewLazyDLL("user32.dll")
		user32.NewProc("ShowWindow").Call(hwnd, 0) // SW_HIDE
	}
	kernel32.NewProc("FreeConsole").Call()
}
