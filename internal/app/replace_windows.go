//go:build windows

package app

import "golang.org/x/sys/windows"

func replaceFile(oldPath, newPath string) error { return windows.Rename(oldPath, newPath) }
