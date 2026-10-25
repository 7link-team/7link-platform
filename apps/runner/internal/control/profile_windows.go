//go:build windows

package control

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var crypt32 = syscall.NewLazyDLL("crypt32.dll")
var localFree = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")
var moveFile = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

type dataBlob struct {
	Size uint32
	Data *byte
}

func replaceFile(source, target string) error {
	s, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	t, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	// MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH.
	ok, _, err := moveFile.Call(uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(t)), 9)
	if ok == 0 {
		return err
	}
	return nil
}

func crypt(data []byte, encrypt bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty protected data")
	}
	in := dataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out dataBlob
	name := "CryptUnprotectData"
	if encrypt {
		name = "CryptProtectData"
	}
	// CRYPTPROTECT_UI_FORBIDDEN; current-user scope, no machine-wide flag.
	ok, _, err := crypt32.NewProc(name).Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	if ok == 0 {
		return nil, err
	}
	defer localFree.Call(uintptr(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
func protect(data []byte) ([]byte, error)   { return crypt(data, true) }
func unprotect(data []byte) ([]byte, error) { return crypt(data, false) }
func privateFile(os.FileInfo) error         { return nil } // DPAPI authenticates and protects the contents.
func lockProfile(path string) (func(), error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return func() { _ = syscall.CloseHandle(h) }, nil
}
