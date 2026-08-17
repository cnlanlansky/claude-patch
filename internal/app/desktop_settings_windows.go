//go:build windows

package app

import (
	"errors"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	startupKeyPath  = `Software\Microsoft\Windows\CurrentVersion\Run`
	settingsKeyPath = `Software\ClaudePatch`
)

func composeStartupCommand(args []string) string {
	return windows.ComposeCommandLine(args)
}

func splitStartupCommand(value string) ([]string, error) {
	return windows.DecomposeCommandLine(value)
}

func ReadDesktopSettings(executable string) (DesktopSettings, error) {
	settings := DesktopSettings{CloseToTray: true}
	startup, exists, err := readRegistryString(startupKeyPath, startupValueName)
	if err != nil {
		return settings, err
	}
	settings.StartupEnabled = exists
	settings.StartupHealthy = exists && startupHealthy(startup, executable)
	settings.StartupConflict = exists && !startupOwned(startup)

	key, err := registry.OpenKey(registry.CURRENT_USER, settingsKeyPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue(closeToTrayValue)
	if errors.Is(err, registry.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	settings.CloseToTray = value != 0
	return settings, nil
}

func SetStartupEnabled(executable string, enabled bool) error {
	current, exists, err := readRegistryString(startupKeyPath, startupValueName)
	if err != nil {
		return err
	}
	if err := requireOwnedStartup(current, exists); err != nil {
		return err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, startupKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if enabled {
		return key.SetStringValue(startupValueName, startupCommand(executable))
	}
	if !exists {
		return nil
	}
	return key.DeleteValue(startupValueName)
}

func SetCloseToTray(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, settingsKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	var value uint32
	if enabled {
		value = 1
	}
	return key.SetDWordValue(closeToTrayValue, value)
}

func readRegistryString(path, name string) (string, bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}
