//go:build windows

package main

import (
	"os"
	"testing"

	"github.com/cnlanlansky/claude-patch/internal/app"
	"golang.org/x/sys/windows"
)

func TestConsoleHelperProcess(t *testing.T) {
	if os.Getenv("CLAUDE_PATCH_CONSOLE_HELPER") != "1" {
		return
	}
	if err := app.AttachParentConsole(); err != nil {
		os.Exit(3)
	}
	input, inputErr := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	output, outputErr := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if inputErr != nil || outputErr != nil || input == 0 || input == windows.InvalidHandle || output == 0 || output == windows.InvalidHandle {
		os.Exit(2)
	}
	os.Exit(0)
}
