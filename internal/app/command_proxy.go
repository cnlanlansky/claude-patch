package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ownershipMarker = "ClaudePatch command proxy"

type CommandStatus struct {
	Directory string
	Command   string
	Installed bool
	Healthy   bool
	InPath    bool
}

func CommandDirectory(executable string) string {
	return filepath.Dir(executable)
}

func commandContent() string {
	return strings.Join([]string{
		"@echo off",
		"rem " + ownershipMarker,
		"\"%~dp0claude-patch.exe\" claude %*",
		"exit /b %ERRORLEVEL%",
		"",
	}, "\r\n")
}

func splitPath(value string) []string {
	var output []string
	for _, part := range strings.Split(value, ";") {
		if part = strings.TrimSpace(part); part != "" {
			output = append(output, part)
		}
	}
	return output
}

func sameExpandedPath(left, right string) bool {
	left = strings.TrimRight(expandEnvironment(strings.Trim(left, ` "`)), `\/`)
	right = strings.TrimRight(expandEnvironment(strings.Trim(right, ` "`)), `\/`)
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func pathFirst(value, directory string) string {
	parts := []string{directory}
	for _, part := range splitPath(value) {
		if !sameExpandedPath(part, directory) {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ";")
}

func pathWithout(value, directory string) string {
	var parts []string
	for _, part := range splitPath(value) {
		if !sameExpandedPath(part, directory) {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ";")
}

func ownedFile(path string) (bool, error) {
	bytes, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.Contains(string(bytes), ownershipMarker), nil
}

func writeOwnedFile(path, content string, mode os.FileMode) error {
	if err := requireOwnedOrMissing(path); err != nil {
		return fmt.Errorf("拒绝覆盖目标文件：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	defer os.Remove(temporary)
	if err := os.WriteFile(temporary, []byte(content), mode); err != nil {
		return err
	}
	return replaceFile(temporary, path)
}

func requireOwnedOrMissing(path string) error {
	owned, err := ownedFile(path)
	if err != nil {
		return err
	}
	if owned {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("拒绝删除非 Claude Patch 文件：%s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func removeOwnedFile(path string) error {
	if err := requireOwnedOrMissing(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func inspectCommand(executable, userPath string) (CommandStatus, error) {
	directory := CommandDirectory(executable)
	status := CommandStatus{Directory: directory, Command: filepath.Join(directory, "claude.cmd")}
	commandBytes, commandErr := os.ReadFile(status.Command)
	if commandErr != nil && !errors.Is(commandErr, os.ErrNotExist) {
		return status, commandErr
	}
	status.Installed = commandErr == nil
	status.Healthy = commandErr == nil && string(commandBytes) == commandContent()
	parts := splitPath(userPath)
	status.InPath = len(parts) > 0 && sameExpandedPath(parts[0], directory)
	return status, nil
}
