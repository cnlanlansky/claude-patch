//go:build !windows

package app

import "errors"

func RunGUI(*Runtime, bool) error { return errors.New("仅支持 Windows") }
func OpenURL(string) error        { return errors.New("仅支持 Windows") }
func ShowError(error)             {}
