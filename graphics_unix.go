//go:build !windows

package main

import (
	"golang.org/x/sys/unix"
	"os"
)

func terminalCellSize(fallbackWidth, fallbackHeight int) (int, int) {
	ws, err := unix.IoctlGetWinsize(int(os.Stderr.Fd()), unix.TIOCGWINSZ)
	if err == nil && ws.Col > 0 && ws.Row > 0 && ws.Xpixel >= ws.Col && ws.Ypixel >= ws.Row {
		return int(ws.Xpixel) / int(ws.Col), int(ws.Ypixel) / int(ws.Row)
	}
	return fallbackWidth, fallbackHeight
}
