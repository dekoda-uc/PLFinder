package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// openFolder opens `path` in the system's native file explorer.
func openFolder(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return err
	}

	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", abs).Start()
	case "darwin":
		return exec.Command("open", abs).Start()
	default:
		return exec.Command("xdg-open", abs).Start()
	}
}
