//go:build windows

package app

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	managementMutexName  = `Local\ClaudePatch.Management.v1`
	managementWindowName = "ClaudePatchWindow"
)

type ManagementInstance struct {
	handle windows.Handle
}

func AcquireManagementInstance(background bool) (*ManagementInstance, bool, error) {
	name, err := windows.UTF16PtrFromString(managementMutexName)
	if err != nil {
		return nil, false, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		if !background {
			showExistingManagementWindow()
		}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &ManagementInstance{handle: handle}, true, nil
}

func (instance *ManagementInstance) Close() error {
	if instance == nil || instance.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(instance.handle)
	instance.handle = 0
	return err
}

func showExistingManagementWindow() {
	class := windows.StringToUTF16Ptr(managementWindowName)
	window, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(class)), 0)
	if window != 0 {
		procPostMessageW.Call(window, wmShowManagement, 0, 0)
	}
}
