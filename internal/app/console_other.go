//go:build !windows

package app

func AttachParentConsole() error { return nil }
