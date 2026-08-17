//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsGUIBinaryAttachesParentConsole(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "console-helper.exe")
	build := exec.Command("go", "test", "-c", "-o", executable, "-ldflags=-H=windowsgui", ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("构建 GUI helper：%v\n%s", err, output)
	}
	command := exec.Command(executable, "-test.run=^TestConsoleHelperProcess$")
	command.Env = append(os.Environ(), "CLAUDE_PATCH_CONSOLE_HELPER=1")
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	if err := command.Run(); err != nil {
		t.Fatalf("GUI 子命令未复用当前控制台：%v", err)
	}
}
