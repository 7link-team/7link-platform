//go:build windows

package executor

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os/exec"
	"slices"
	"strconv"
	"syscall"
	"unsafe"
)

var portKernel = syscall.NewLazyDLL("kernel32.dll")
var portThreadFirst = portKernel.NewProc("Thread32First")
var portThreadNext = portKernel.NewProc("Thread32Next")
var portSnapshot = portKernel.NewProc("CreateToolhelp32Snapshot")
var portOpenThread = portKernel.NewProc("OpenThread")
var portIsInJob = portKernel.NewProc("IsProcessInJob")
var portTCPTable = syscall.NewLazyDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

func startPortOwnedProcess(cmd *exec.Cmd) (authority *processPortAuthority, err error) {
	job, _, e := terminalCreateJob.Call(0, 0)
	if job == 0 {
		return nil, e
	}
	defer func() {
		if err != nil {
			syscall.CloseHandle(syscall.Handle(job))
		}
	}()
	limits := terminalJobLimits{Flags: 0x2000}
	if ok, _, e := terminalSetJob.Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits)); ok == 0 {
		return nil, e
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= 4 // CREATE_SUSPENDED: assign Job before descendants can launch.
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	process, e := syscall.OpenProcess(0x0100|0x0001|0x1000, false, uint32(cmd.Process.Pid))
	if e != nil {
		return nil, e
	}
	defer syscall.CloseHandle(process)
	if ok, _, e := terminalAssignJob.Call(job, uintptr(process)); ok == 0 {
		return nil, e
	}
	if err = resumePortProcess(uint32(cmd.Process.Pid)); err != nil {
		return nil, err
	}
	return &processPortAuthority{address: func(ctx context.Context, port uint16) (string, error) {
		return ownedJobLoopback(ctx, syscall.Handle(job), port)
	}, list: func(ctx context.Context) ([]int, error) { return ownedJobLoopbackPorts(ctx, syscall.Handle(job)) }, release: func() { syscall.CloseHandle(syscall.Handle(job)) }}, nil
}

func ownedJobLoopbackPorts(ctx context.Context, job syscall.Handle) ([]int, error) {
	candidates := make(map[uint16]bool)
	for _, family := range []uint32{2, 23} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var size uint32
		status, _, _ := portTCPTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), 3, 0)
		if status != 122 && status != 0 || size < 4 || size > 16<<20 {
			return nil, ErrUnsupported
		}
		data := make([]byte, size)
		status, _, _ = portTCPTable.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(family), 3, 0)
		if status != 0 {
			return nil, ErrUnsupported
		}
		count := binary.LittleEndian.Uint32(data[:4])
		rowSize, offset, pidOffset := 24, 8, 20
		if family == 23 {
			rowSize, offset, pidOffset = 56, 20, 52
		}
		if uint64(count)*uint64(rowSize)+4 > uint64(len(data)) {
			return nil, ErrUnsupported
		}
		for i := uint32(0); i < count; i++ {
			row := data[4+int(i)*rowSize : 4+int(i+1)*rowSize]
			pid := binary.LittleEndian.Uint32(row[pidOffset : pidOffset+4])
			process, err := syscall.OpenProcess(0x1000, false, pid)
			if err != nil {
				continue
			}
			var inJob uint32
			ok, _, _ := portIsInJob.Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&inJob)))
			syscall.CloseHandle(process)
			if ok != 0 && inJob != 0 {
				candidates[binary.BigEndian.Uint16(row[offset:offset+2])] = true
			}
		}
	}
	ports := make([]int, 0, len(candidates))
	for port := range candidates {
		if _, err := ownedJobLoopback(ctx, job, port); err == nil {
			ports = append(ports, int(port))
		}
	}
	slices.Sort(ports)
	return ports, nil
}

type portThreadEntry struct {
	Size, Usage, ThreadID, ProcessID uint32
	BasePriority, DeltaPriority      int32
	Flags                            uint32
}

func resumePortProcess(pid uint32) error {
	snapshot, _, e := portSnapshot.Call(4, 0)
	if snapshot == ^uintptr(0) {
		return e
	}
	defer syscall.CloseHandle(syscall.Handle(snapshot))
	entry := portThreadEntry{Size: uint32(unsafe.Sizeof(portThreadEntry{}))}
	ok, _, _ := portThreadFirst.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	for ok != 0 {
		if entry.ProcessID == pid {
			thread, _, e := portOpenThread.Call(2, 0, uintptr(entry.ThreadID))
			if thread == 0 {
				return e
			}
			result, _, e := terminalResume.Call(thread)
			syscall.CloseHandle(syscall.Handle(thread))
			if result == 0xffffffff {
				return e
			}
			return nil
		}
		ok, _, _ = portThreadNext.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	}
	return errors.New("runtime primary thread unavailable")
}

func ownedJobLoopback(ctx context.Context, job syscall.Handle, port uint16) (string, error) {
	address := ""
	for _, family := range []uint32{2, 23} {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var size uint32
		status, _, _ := portTCPTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), 3, 0)
		if status != 122 && status != 0 {
			return "", ErrUnsupported
		}
		if size < 4 || size > 16<<20 {
			return "", ErrUnsupported
		}
		data := make([]byte, size)
		status, _, _ = portTCPTable.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(family), 3, 0)
		if status != 0 {
			return "", ErrUnsupported
		}
		count := binary.LittleEndian.Uint32(data[:4])
		rowSize, portOffset, pidOffset := 24, 8, 20
		if family == 23 {
			rowSize, portOffset, pidOffset = 56, 20, 52
		}
		if uint64(count)*uint64(rowSize)+4 > uint64(len(data)) {
			return "", ErrUnsupported
		}
		for i := uint32(0); i < count; i++ {
			row := data[4+int(i)*rowSize : 4+int(i+1)*rowSize]
			if binary.BigEndian.Uint16(row[portOffset:portOffset+2]) != port {
				continue
			}
			loopback := false
			host := "127.0.0.1"
			if family == 2 {
				loopback = row[4] == 127 && row[5] == 0 && row[6] == 0 && row[7] == 1
			} else {
				host = "::1"
				loopback = row[15] == 1
				for _, b := range row[:15] {
					if b != 0 {
						loopback = false
					}
				}
			}
			if !loopback {
				return "", ErrUnsupported
			}
			pid := binary.LittleEndian.Uint32(row[pidOffset : pidOffset+4])
			process, err := syscall.OpenProcess(0x1000, false, pid)
			if err != nil {
				return "", ErrUnsupported
			}
			var inJob uint32
			ok, _, _ := portIsInJob.Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&inJob)))
			syscall.CloseHandle(process)
			if ok == 0 || inJob == 0 {
				return "", ErrUnsupported
			}
			if address == "" {
				address = host
			}
		}
	}
	if address == "" {
		return "", ErrUnsupported
	}
	return net.JoinHostPort(address, strconv.Itoa(int(port))), nil
}
