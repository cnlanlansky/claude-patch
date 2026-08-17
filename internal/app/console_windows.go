//go:build windows

package app

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

var procAttachConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")

func AttachParentConsole() error {
	const attachParentProcess = ^uint32(0)
	attached, _, err := procAttachConsole.Call(uintptr(attachParentProcess))
	if attached != 0 || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return nil
	}
	return fmt.Errorf("AttachConsole(ATTACH_PARENT_PROCESS): %w", err)
}
