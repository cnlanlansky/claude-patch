//go:build windows

package app

import (
	"errors"
	"path/filepath"
	"strings"
)

const (
	startupValueName = "ClaudePatch"
	closeToTrayValue = "CloseToTray"
)

type DesktopSettings struct {
	StartupEnabled  bool
	StartupHealthy  bool
	StartupConflict bool
	CloseToTray     bool
}

func startupCommand(executable string) string {
	return composeStartupCommand([]string{executable, "--background"})
}

func startupOwned(value string) bool {
	args, err := splitStartupCommand(value)
	if err != nil || len(args) != 2 || args[1] != "--background" {
		return false
	}
	return strings.EqualFold(filepath.Base(args[0]), "claude-patch.exe") && filepath.IsAbs(args[0])
}

func startupHealthy(value, executable string) bool {
	args, err := splitStartupCommand(value)
	if err != nil || len(args) != 2 || args[1] != "--background" {
		return false
	}
	return sameExpandedPath(args[0], executable)
}

func requireOwnedStartup(value string, exists bool) error {
	if !exists || startupOwned(value) {
		return nil
	}
	return errors.New("拒绝覆盖非 Claude Patch 的同名开机启动项")
}
