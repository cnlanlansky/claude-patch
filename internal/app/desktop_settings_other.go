//go:build !windows

package app

import "errors"

type DesktopSettings struct {
	StartupEnabled  bool
	StartupHealthy  bool
	StartupConflict bool
	CloseToTray     bool
}

func ReadDesktopSettings(string) (DesktopSettings, error) {
	return DesktopSettings{CloseToTray: true}, errors.New("仅支持 Windows")
}

func SetStartupEnabled(string, bool) error { return errors.New("仅支持 Windows") }
func SetCloseToTray(bool) error            { return errors.New("仅支持 Windows") }
