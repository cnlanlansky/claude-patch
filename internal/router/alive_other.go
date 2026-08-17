//go:build !windows

package router

import "syscall"

func processAlive(pid uint32) bool {
	process, err := syscall.OpenProcess(int(pid))
	if err != nil {
		return false
	}
	defer process.Release()
	return process.Signal(syscall.Signal(0)) == nil
}
