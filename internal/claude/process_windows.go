//go:build windows

package claude

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	memImage         = 0x01000000
	pageExecuteFlags = 0xf0
)

type Process struct {
	ProcessID uint32
	ThreadID  uint32
	ImagePath string

	process  windows.Handle
	thread   windows.Handle
	job      windows.Handle
	resumed  bool
	closed   bool
	tainted  bool
	patching bool
	mu       sync.Mutex
}

func CreateSuspended(executable string, args []string, workingDirectory string, overrides map[string]string) (*Process, error) {
	if workingDirectory == "" {
		var err error
		workingDirectory, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	application, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return nil, err
	}
	command, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{executable}, args...)))
	if err != nil {
		return nil, err
	}
	cwd, err := windows.UTF16PtrFromString(workingDirectory)
	if err != nil {
		return nil, err
	}
	environment, err := environmentBlock(overrides)
	if err != nil {
		return nil, err
	}

	stdin, _ := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	stdout, _ := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	stderr, _ := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	startup := windows.StartupInfo{
		Cb: uint32(unsafe.Sizeof(windows.StartupInfo{})), Flags: windows.STARTF_USESTDHANDLES,
		StdInput: stdin, StdOutput: stdout, StdErr: stderr,
	}
	var information windows.ProcessInformation
	if err := windows.CreateProcess(
		application, command, nil, nil, true,
		windows.CREATE_SUSPENDED|windows.CREATE_UNICODE_ENVIRONMENT,
		&environment[0], cwd, &startup, &information,
	); err != nil {
		return nil, fmt.Errorf("CreateProcessW: %w", err)
	}
	process := &Process{
		ProcessID: information.ProcessId, ThreadID: information.ThreadId,
		process: information.Process, thread: information.Thread,
	}
	fail := func(cause error) (*Process, error) {
		_ = process.Terminate(1)
		_ = process.Close()
		return nil, cause
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fail(fmt.Errorf("CreateJobObjectW: %w", err))
	}
	process.job = job
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return fail(fmt.Errorf("SetInformationJobObject: %w", err))
	}
	if err := windows.AssignProcessToJobObject(job, information.Process); err != nil {
		return fail(fmt.Errorf("AssignProcessToJobObject: %w", err))
	}
	process.ImagePath, err = queryImagePath(information.Process)
	if err != nil {
		return fail(err)
	}
	return process, nil
}

func environmentBlock(overrides map[string]string) ([]uint16, error) {
	type entry struct{ name, value string }
	values := make(map[string]entry)
	for _, raw := range os.Environ() {
		index := strings.IndexByte(raw, '=')
		if index <= 0 {
			continue
		}
		name, value := raw[:index], raw[index+1:]
		values[strings.ToUpper(name)] = entry{name, value}
	}
	for name, value := range overrides {
		if name == "" || strings.ContainsAny(name, "=\x00") || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("Windows 环境变量无效：%q", name)
		}
		values[strings.ToUpper(name)] = entry{name, value}
	}
	entries := make([]entry, 0, len(values))
	for _, value := range values {
		entries = append(entries, value)
	}
	sort.Slice(entries, func(left, right int) bool {
		return strings.ToUpper(entries[left].name) < strings.ToUpper(entries[right].name)
	})
	var block []uint16
	for _, value := range entries {
		block = append(block, utf16.Encode([]rune(value.name+"="+value.value))...)
		block = append(block, 0)
	}
	return append(block, 0), nil
}

func queryImagePath(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return "", fmt.Errorf("QueryFullProcessImageNameW: %w", err)
	}
	return windows.UTF16ToString(buffer[:size]), nil
}

func (process *Process) Resume() error {
	process.mu.Lock()
	defer process.mu.Unlock()
	if process.closed || process.resumed || process.tainted || process.patching {
		return errors.New("Claude child 无法 resume")
	}
	previous, err := windows.ResumeThread(process.thread)
	if err != nil {
		process.tainted = true
		return fmt.Errorf("ResumeThread: %w", err)
	}
	if previous != 1 {
		process.tainted = true
		return fmt.Errorf("主线程 suspend count 异常：%d", previous)
	}
	process.resumed = true
	return nil
}

func (process *Process) Resumed() bool {
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.resumed
}

func (process *Process) Wait(milliseconds uint32) (uint32, bool, error) {
	process.mu.Lock()
	closed := process.closed
	handle := process.process
	process.mu.Unlock()
	if closed || handle == 0 {
		return 0, false, errors.New("Claude child 句柄已关闭")
	}
	result, err := windows.WaitForSingleObject(handle, milliseconds)
	if err != nil {
		return 0, false, err
	}
	if result == uint32(windows.WAIT_TIMEOUT) {
		return 0, false, nil
	}
	if result != windows.WAIT_OBJECT_0 {
		return 0, false, fmt.Errorf("WaitForSingleObject 返回 0x%x", result)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return 0, false, err
	}
	return code, true, nil
}

func (process *Process) Terminate(code uint32) error {
	process.mu.Lock()
	if process.patching {
		process.mu.Unlock()
		return errors.New("内存 patch 进行中，无法终止 child")
	}
	closed := process.closed
	handle := process.process
	process.mu.Unlock()
	if closed || handle == 0 {
		return errors.New("Claude child 句柄已关闭")
	}
	if _, exited, _ := process.Wait(0); exited {
		return nil
	}
	if err := windows.TerminateProcess(handle, code); err != nil {
		if _, exited, _ := process.Wait(0); exited {
			return nil
		}
		return err
	}
	_, exited, err := process.Wait(10_000)
	if err != nil {
		return err
	}
	if !exited {
		return errors.New("TerminateProcess 后等待超时")
	}
	return nil
}

func (process *Process) Close() error {
	process.mu.Lock()
	if process.patching {
		process.mu.Unlock()
		return errors.New("内存 patch 进行中，无法关闭 child")
	}
	defer process.mu.Unlock()
	if process.closed {
		return nil
	}
	process.closed = true
	var errs []error
	for _, handle := range []windows.Handle{process.thread, process.process, process.job} {
		if handle != 0 {
			if err := windows.CloseHandle(handle); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
func (process *Process) ReadMemory(address uintptr, size int) ([]byte, error) {
	if size < 0 {
		return nil, errors.New("远程读取长度无效")
	}
	if size == 0 {
		return []byte{}, nil
	}
	output := make([]byte, size)
	var read uintptr
	if err := windows.ReadProcessMemory(process.process, address, &output[0], uintptr(size), &read); err != nil {
		return nil, fmt.Errorf("ReadProcessMemory: %w", err)
	}
	if read != uintptr(size) {
		return nil, fmt.Errorf("ReadProcessMemory 长度不完整：%d/%d", read, size)
	}
	return output, nil
}

func (process *Process) regions() ([]windows.MemoryBasicInformation, error) {
	var regions []windows.MemoryBasicInformation
	for address := uintptr(0x10000); ; {
		var information windows.MemoryBasicInformation
		err := windows.VirtualQueryEx(process.process, address, &information, unsafe.Sizeof(information))
		if err != nil {
			if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
				break
			}
			return nil, fmt.Errorf("VirtualQueryEx: %w", err)
		}
		if information.RegionSize == 0 {
			break
		}
		regions = append(regions, information)
		next := information.BaseAddress + information.RegionSize
		if next <= address {
			break
		}
		address = next
	}
	return regions, nil
}

func (process *Process) FindImageBase(headers []byte, imageBaseOffset uint32) (uintptr, error) {
	regions, err := process.regions()
	if err != nil {
		return 0, err
	}
	var candidates []uintptr
	for _, region := range regions {
		if region.State != windows.MEM_COMMIT || region.Type != memImage || region.BaseAddress != region.AllocationBase || region.RegionSize < uintptr(len(headers)) {
			continue
		}
		remote, err := process.ReadMemory(region.BaseAddress, len(headers))
		if err != nil || int(imageBaseOffset)+8 > len(remote) {
			continue
		}
		mapped := uintptr(*(*uint64)(unsafe.Pointer(&remote[imageBaseOffset])))
		if mapped != region.BaseAddress {
			continue
		}
		left := append([]byte(nil), headers...)
		right := append([]byte(nil), remote...)
		for index := int(imageBaseOffset); index < int(imageBaseOffset)+8; index++ {
			left[index], right[index] = 0, 0
		}
		if bytes.Equal(left, right) {
			candidates = append(candidates, region.BaseAddress)
		}
	}
	if len(candidates) != 1 {
		return 0, fmt.Errorf("主映像 headers 应唯一匹配，实际 %d", len(candidates))
	}
	return candidates[0], nil
}

func (process *Process) markTainted() {
	process.mu.Lock()
	process.tainted = true
	process.mu.Unlock()
}

func (process *Process) beginPatch() error {
	process.mu.Lock()
	defer process.mu.Unlock()
	if process.closed || process.resumed || process.tainted || process.patching {
		return errors.New("Claude child 无法开始内存 patch")
	}
	process.patching = true
	return nil
}

func (process *Process) endPatch() {
	process.mu.Lock()
	process.patching = false
	process.mu.Unlock()
}

func (process *Process) PatchData(address uintptr, expected, replacement []byte) error {
	if len(expected) == 0 || len(expected) != len(replacement) {
		process.markTainted()
		return errors.New("内存 patch 必须为非空同长度替换")
	}
	if err := process.beginPatch(); err != nil {
		return err
	}
	defer process.endPatch()
	writeAttempted, err := process.writeDataLocked(address, expected, replacement, true)
	if err == nil {
		return nil
	}
	var restoreErr error
	if writeAttempted {
		_, restoreErr = process.writeDataLocked(address, nil, expected, false)
	}
	process.markTainted()
	return errors.Join(err, restoreErr)
}

func (process *Process) writeDataLocked(address uintptr, expected, replacement []byte, checkExpected bool) (bool, error) {
	if len(replacement) == 0 || (checkExpected && len(expected) != len(replacement)) {
		return false, errors.New("内存 patch 数据长度无效")
	}
	regions, err := process.regions()
	if err != nil {
		return false, err
	}
	var region *windows.MemoryBasicInformation
	end := address + uintptr(len(replacement))
	if end < address {
		return false, errors.New("内存 patch 地址溢出")
	}
	for index := range regions {
		candidate := &regions[index]
		if address >= candidate.BaseAddress && end <= candidate.BaseAddress+candidate.RegionSize {
			if region != nil {
				return false, errors.New("内存 patch 命中多个 region")
			}
			region = candidate
		}
	}
	if region == nil || region.State != windows.MEM_COMMIT || region.Type != memImage || region.Protect&(windows.PAGE_NOACCESS|windows.PAGE_GUARD|pageExecuteFlags) != 0 {
		return false, errors.New("内存 patch region 不可安全写入")
	}
	if checkExpected {
		before, err := process.ReadMemory(address, len(expected))
		if err != nil {
			return false, err
		}
		if !bytes.Equal(before, expected) {
			return false, errors.New("内存 patch 原字节不匹配")
		}
	}
	var oldProtect uint32
	if err := windows.VirtualProtectEx(process.process, address, uintptr(len(replacement)), windows.PAGE_READWRITE, &oldProtect); err != nil {
		return false, fmt.Errorf("VirtualProtectEx(write): %w", err)
	}
	if oldProtect != region.Protect {
		var ignored uint32
		_ = windows.VirtualProtectEx(process.process, address, uintptr(len(replacement)), oldProtect, &ignored)
		return false, fmt.Errorf("VirtualProtectEx 原保护不一致：0x%x/0x%x", oldProtect, region.Protect)
	}
	var written uintptr
	writeErr := windows.WriteProcessMemory(process.process, address, &replacement[0], uintptr(len(replacement)), &written)
	writeAttempted := true
	var ignored uint32
	restoreErr := windows.VirtualProtectEx(process.process, address, uintptr(len(replacement)), oldProtect, &ignored)
	if writeErr != nil || restoreErr != nil || written != uintptr(len(replacement)) {
		var lengthErr error
		if written != uintptr(len(replacement)) {
			lengthErr = fmt.Errorf("WriteProcessMemory 长度：%d/%d", written, len(replacement))
		}
		return writeAttempted, errors.Join(writeErr, restoreErr, lengthErr)
	}
	after, err := process.ReadMemory(address, len(replacement))
	if err != nil {
		return writeAttempted, err
	}
	if !bytes.Equal(after, replacement) {
		return writeAttempted, errors.New("内存 patch 写后回读不匹配")
	}
	var restored windows.MemoryBasicInformation
	if err := windows.VirtualQueryEx(process.process, address, &restored, unsafe.Sizeof(restored)); err != nil {
		return writeAttempted, err
	}
	if restored.Protect != region.Protect {
		return writeAttempted, errors.New("内存 patch 页保护未恢复")
	}
	return writeAttempted, nil
}

func samePath(left, right string) bool {
	leftAbs, _ := filepath.Abs(left)
	rightAbs, _ := filepath.Abs(right)
	return strings.EqualFold(filepath.Clean(leftAbs), filepath.Clean(rightAbs))
}
