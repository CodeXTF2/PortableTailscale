//go:build !windows

package main

import "os"

// enableColor reports whether stdout is a terminal that can show ANSI colours.
func enableColor() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0 && os.Getenv("TERM") != "dumb"
}
