//go:build windows

package app

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ownedProcessJobName = `Local\ClaudePatch.Workers.v1`
	jobObjectAssign     = 0x0001
	jobObjectTerminate  = 0x0008
)

type OwnedProcessGroup struct {
	handle windows.Handle
}

var procOpenJobObjectW = windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW")

func JoinOwnedProcessGroup() (*OwnedProcessGroup, error) {
	name, err := windows.UTF16PtrFromString(ownedProcessJobName)
	if err != nil {
		return nil, fmt.Errorf("本工具进程组名称无效：%w", err)
	}
	job, err := openJobObject(jobObjectAssign, name)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		job, err = windows.CreateJobObject(nil, name)
	}
	if err != nil {
		return nil, fmt.Errorf("打开本工具进程组失败：%w", err)
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("加入本工具进程组失败：%w", err)
	}
	return &OwnedProcessGroup{handle: job}, nil
}

func OpenOrCreateOwnedProcessGroup() (*OwnedProcessGroup, error) {
	name, err := windows.UTF16PtrFromString(ownedProcessJobName)
	if err != nil {
		return nil, fmt.Errorf("本工具进程组名称无效：%w", err)
	}
	job, err := openJobObject(jobObjectAssign|jobObjectTerminate, name)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		job, err = windows.CreateJobObject(nil, name)
	}
	if err != nil {
		return nil, fmt.Errorf("创建本工具进程组失败：%w", err)
	}
	return &OwnedProcessGroup{handle: job}, nil
}

func (group *OwnedProcessGroup) Terminate() error {
	if group == nil || group.handle == 0 {
		return nil
	}
	if err := windows.TerminateJobObject(group.handle, 1); err != nil {
		return fmt.Errorf("TerminateJobObject: %w", err)
	}
	return nil
}

func (group *OwnedProcessGroup) Close() error {
	if group == nil || group.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(group.handle)
	group.handle = 0
	return err
}

func openJobObject(access uint32, name *uint16) (windows.Handle, error) {
	result, _, callErr := procOpenJobObjectW.Call(uintptr(access), 0, uintptr(unsafe.Pointer(name)))
	if result == 0 {
		if callErr != nil {
			return 0, callErr
		}
		return 0, windows.ERROR_FILE_NOT_FOUND
	}
	return windows.Handle(result), nil
}
