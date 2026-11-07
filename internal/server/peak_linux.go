//go:build linux

package server

// maxrssToBytes: on Linux `ru_maxrss` is in kilobytes.
func maxrssToBytes(value int64) uint64 {
	if value < 0 {
		return 0
	}
	return uint64(value) * 1024
}
