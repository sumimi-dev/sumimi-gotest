//go:build !darwin

package server

import "os"

// peakRusage: nothing to read here.
//
// On Linux the kernel does not reset `ru_maxrss` when a process is exec'd, so
// a child's high-water mark still carries the parent's resident set from the
// moment of the fork — the load generator's memory would be reported as the
// server's peak. The server reports its own peak (`/proc`), and that is the
// number to believe.
func peakRusage(state *os.ProcessState) uint64 {
	return 0
}
