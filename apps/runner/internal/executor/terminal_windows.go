//go:build windows

package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var terminalKernel = syscall.NewLazyDLL("kernel32.dll")
var terminalCreate = terminalKernel.NewProc("CreatePseudoConsole")
var terminalResize = terminalKernel.NewProc("ResizePseudoConsole")
var terminalRelease = terminalKernel.NewProc("ClosePseudoConsole")
var terminalInitAttributes = terminalKernel.NewProc("InitializeProcThreadAttributeList")
var terminalUpdateAttributes = terminalKernel.NewProc("UpdateProcThreadAttribute")
var terminalDeleteAttributes = terminalKernel.NewProc("DeleteProcThreadAttributeList")
var terminalCreateJob = terminalKernel.NewProc("CreateJobObjectW")
var terminalSetJob = terminalKernel.NewProc("SetInformationJobObject")
var terminalAssignJob = terminalKernel.NewProc("AssignProcessToJobObject")
var terminalResume = terminalKernel.NewProc("ResumeThread")

type terminalStartup struct {
	syscall.StartupInfo
	attributes uintptr
}
type terminalJobLimits struct {
	ProcessTime, JobTime                                       int64
	Flags                                                      uint32
	Minimum, Maximum                                           uintptr
	Active                                                     uint32
	Affinity                                                   uintptr
	Priority, Scheduling                                       uint32
	IO                                                         [6]uint64
	ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory uintptr
}

func openTerminal(ctx context.Context, dir string, columns, rows uint16) (_ *Terminal, err error) {
	if terminalCreate.Find() != nil {
		return nil, ErrUnsupported
	}
	inRead, inWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		inRead.Close()
		inWrite.Close()
		return nil, err
	}
	var console uintptr
	hr, _, _ := terminalCreate.Call(uintptr(columns)|uintptr(rows)<<16, inRead.Fd(), outWrite.Fd(), 0, uintptr(unsafe.Pointer(&console)))
	defer inRead.Close()
	defer outWrite.Close()
	if int32(hr) < 0 {
		inWrite.Close()
		outRead.Close()
		return nil, fmt.Errorf("ConPTY create: %x", hr)
	}
	job, _, callErr := terminalCreateJob.Call(0, 0)
	if job == 0 {
		terminalRelease.Call(console)
		inWrite.Close()
		outRead.Close()
		return nil, callErr
	}
	cleanup := func() {
		syscall.CloseHandle(syscall.Handle(job))
		inWrite.Close()
		terminalRelease.Call(console)
		outRead.Close()
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	limits := terminalJobLimits{Flags: 0x2000} // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if ok, _, e := terminalSetJob.Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits)); ok == 0 {
		return nil, e
	}
	var size uintptr
	terminalInitAttributes.Call(0, 1, 0, uintptr(unsafe.Pointer(&size)))
	attrs := make([]byte, size)
	attrPtr := uintptr(unsafe.Pointer(&attrs[0]))
	if ok, _, e := terminalInitAttributes.Call(attrPtr, 1, 0, uintptr(unsafe.Pointer(&size))); ok == 0 {
		return nil, e
	}
	defer terminalDeleteAttributes.Call(attrPtr)
	// The attribute value IS the pseudoconsole handle, not its address.
	if ok, _, e := terminalUpdateAttributes.Call(attrPtr, 0, 0x20016, console, unsafe.Sizeof(console), 0, 0); ok == 0 {
		return nil, e
	}
	startup := terminalStartup{attributes: attrPtr}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	// Clear inherited parent std handles so the child resolves its console
	// handles through ConPTY, including when runner is a service or test host.
	startup.Flags = syscall.STARTF_USESHOWWINDOW | syscall.STARTF_USESTDHANDLES
	startup.ShowWindow = syscall.SW_HIDE
	// GetSystemDirectory is trusted OS state; do not honor COMSPEC or caller PATH.
	var system [32768]uint16
	getSystem := terminalKernel.NewProc("GetSystemDirectoryW")
	n, _, e := getSystem.Call(uintptr(unsafe.Pointer(&system[0])), uintptr(len(system)))
	if n == 0 || n >= uintptr(len(system)) {
		return nil, e
	}
	shell := filepath.Join(syscall.UTF16ToString(system[:n]), "cmd.exe")
	app, _ := syscall.UTF16PtrFromString(shell)
	command, _ := syscall.UTF16PtrFromString(`"` + shell + `" /D /Q`)
	cwd, _ := syscall.UTF16PtrFromString(dir)
	env := verificationEnvironment()
	// Build a double-NUL terminated Unicode environment block.
	var block []uint16
	for _, item := range env {
		encoded, e := syscall.UTF16FromString(item)
		if e != nil {
			return nil, e
		}
		block = append(block, encoded...)
	}
	block = append(block, 0)
	var process syscall.ProcessInformation
	flags := uint32(0x80000 | 0x400 | 0x4) // extended startup, Unicode, suspended; pseudoconsole has no window
	err = syscall.CreateProcess(app, command, nil, nil, false, flags, &block[0], cwd, &startup.StartupInfo, &process)
	runtime.KeepAlive(attrs)
	runtime.KeepAlive(block)
	if err != nil {
		return nil, err
	}
	if ok, _, e := terminalAssignJob.Call(job, uintptr(process.Process)); ok == 0 {
		syscall.TerminateProcess(process.Process, 1)
		syscall.CloseHandle(process.Thread)
		syscall.CloseHandle(process.Process)
		return nil, e
	}
	if result, _, e := terminalResume.Call(uintptr(process.Thread)); result == 0xffffffff {
		syscall.TerminateProcess(process.Process, 1)
		syscall.CloseHandle(process.Thread)
		syscall.CloseHandle(process.Process)
		return nil, e
	}
	syscall.CloseHandle(process.Thread)
	done := make(chan struct{})
	var waitErr error
	go func() {
		_, waitErr = syscall.WaitForSingleObject(process.Process, syscall.INFINITE)
		var code uint32
		if waitErr == nil {
			waitErr = syscall.GetExitCodeProcess(process.Process, &code)
			if waitErr == nil && code != 0 {
				waitErr = fmt.Errorf("terminal exit %d", code)
			}
		}
		syscall.CloseHandle(process.Process)
		close(done)
	}()
	var once sync.Once
	var stateMu sync.Mutex
	closed := false
	t := &Terminal{Input: inWrite, Output: outRead}
	t.close = func() error {
		once.Do(func() { stateMu.Lock(); closed = true; stateMu.Unlock(); cleanup() })
		return nil
	}
	t.resize = func(c, r uint16) error {
		stateMu.Lock()
		defer stateMu.Unlock()
		if closed {
			return os.ErrClosed
		}
		hr, _, _ := terminalResize.Call(console, uintptr(c)|uintptr(r)<<16)
		if int32(hr) < 0 {
			return errors.New("terminal resize failed")
		}
		return nil
	}
	t.wait = func(waitCtx context.Context) error {
		select {
		case <-done:
			return waitErr
		case <-waitCtx.Done():
			return waitCtx.Err()
		}
	}
	go func() {
		select {
		case <-ctx.Done():
			t.Close()
		case <-done:
		}
	}()
	// A bounded caller wait is mandatory; no command completion is inferred from
	// a successful pipe write or appearance of a prompt.
	return t, nil
}
