//go:build !windows

package app

import (
	"errors"
	"os"
)

func expandEnvironment(value string) string { return os.ExpandEnv(value) }

func CommandState(string) (CommandStatus, error) {
	return CommandStatus{}, errors.New("仅支持 Windows")
}
func InstallCommand(string) (CommandStatus, error) {
	return CommandStatus{}, errors.New("仅支持 Windows")
}
func UninstallCommand(string) (CommandStatus, error) {
	return CommandStatus{}, errors.New("仅支持 Windows")
}
