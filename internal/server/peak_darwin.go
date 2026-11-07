//go:build darwin

package server

import (
	"os"
	"syscall"
)

// peakRusage: the peak resident set of a process we started, from the
// kernel's own account.
//
// **Only on macOS.** On Linux the kernel does not reset `ru_maxrss` when a
// process is exec'd, so a child's high-water mark still carries the parent's
// resident set from the moment of the fork — the load generator's memory
// would be reported as the server's peak. macOS resets it, so there it is
// the server's own.
//
// On Linux the server reports its own peak (`/proc`), and that is the number
// to believe.
func peakRusage(state *os.ProcessState) uint64 {
	if state == nil {
		return 0
	}
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok || usage == nil {
		return 0
	}
	if usage.Maxrss < 0 {
		return 0
	}
	// On macOS `ru_maxrss` is already in bytes.
	return uint64(usage.Maxrss)
}
