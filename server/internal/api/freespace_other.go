//go:build !unix

package api

// freeBytes is not implemented on this platform.
func freeBytes(string) int64 { return 0 }
