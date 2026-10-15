//go:build !unix

package runner

import "errors"

func statfsFree(string) (uint64, error) {
	return 0, errors.New("free space check is not implemented on this platform")
}
