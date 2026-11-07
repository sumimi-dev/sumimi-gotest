//go:build !linux && !darwin

package server

// maxrssToBytes: the unit of `ru_maxrss` is not the same everywhere, and
// this tool does not claim a number it cannot interpret.
func maxrssToBytes(value int64) uint64 {
	return 0
}
