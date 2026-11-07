//go:build darwin

package server

// maxrssToBytes: on macOS `ru_maxrss` is already in bytes.
func maxrssToBytes(value int64) uint64 {
	if value < 0 {
		return 0
	}
	return uint64(value)
}
