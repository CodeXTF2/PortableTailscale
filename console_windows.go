package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableColor turns on ANSI escape processing for the console attached to
// stdout. It reports false when stdout is not a console or the console does
// not support escapes, so redirected output stays plain text.
func enableColor() bool {
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
