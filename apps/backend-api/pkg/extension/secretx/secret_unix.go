//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package secretx

import (
	"os"

	"golang.org/x/sys/unix"
)

func openFile(path string) (secretFile, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil //nolint:gosec // fd is a non-negative descriptor returned by unix.Open.
}
