//go:build windows || plan9 || js || wasip1

package secretx

import "os"

func openFile(path string) (secretFile, error) {
	return os.Open(path)
}
