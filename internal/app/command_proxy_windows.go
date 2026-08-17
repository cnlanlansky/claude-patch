//go:build windows

package app

import (
	"errors"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const environmentKey = `Environment`

func expandEnvironment(value string) string {
	expanded, err := registry.ExpandString(value)
	if err != nil {
		return value
	}
	return expanded
}

func CommandState(executable string) (CommandStatus, error) {
	path, _, err := userPath()
	if err != nil {
		return CommandStatus{}, err
	}
	return inspectCommand(executable, path)
}

func InstallCommand(executable string) (CommandStatus, error) {
	path, valueType, err := userPath()
	if err != nil {
		return CommandStatus{}, err
	}
	directory := CommandDirectory(executable)
	command := filepath.Join(directory, "claude.cmd")
	if err := requireOwnedOrMissing(command); err != nil {
		return CommandStatus{}, err
	}
	nextPath := pathFirst(path, directory)
	if err := setUserPath(nextPath, valueType); err != nil {
		return CommandStatus{}, err
	}
	if err := writeOwnedFile(command, commandContent(), 0o600); err != nil {
		_ = setUserPath(path, valueType)
		return CommandStatus{}, err
	}
	broadcastEnvironmentChange()
	return CommandState(executable)
}

func UninstallCommand(executable string) (CommandStatus, error) {
	path, valueType, err := userPath()
	if err != nil {
		return CommandStatus{}, err
	}
	directory := CommandDirectory(executable)
	command := filepath.Join(directory, "claude.cmd")
	if err := requireOwnedOrMissing(command); err != nil {
		return CommandStatus{}, err
	}
	if err := setUserPath(pathWithout(path, directory), valueType); err != nil {
		return CommandStatus{}, err
	}
	if err := removeOwnedFile(command); err != nil {
		_ = setUserPath(path, valueType)
		return CommandStatus{}, err
	}
	broadcastEnvironmentChange()
	return CommandState(executable)
}

func userPath() (string, uint32, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, environmentKey, registry.QUERY_VALUE)
	if err != nil {
		return "", registry.EXPAND_SZ, err
	}
	defer key.Close()
	value, valueType, err := key.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return "", registry.EXPAND_SZ, nil
	}
	return value, valueType, err
}

func setUserPath(value string, valueType uint32) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, environmentKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if valueType == registry.SZ {
		return key.SetStringValue("Path", value)
	}
	return key.SetExpandStringValue("Path", value)
}

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")
)

func broadcastEnvironmentChange() {
	name, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001a
		smtoAbortIfHung = 0x0002
	)
	var result uintptr
	_, _, _ = procSendMessageTimeoutW.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(name)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result)))
}
