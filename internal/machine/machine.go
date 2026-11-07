// Package machine reads what a machine is.
//
// A number without the machine it was measured on is not reproducible: the
// same rate costs a different amount of memory on a different box. So every
// report carries the machine's identity — **the machine that was measured**,
// not the machine that measured it.
package machine

import (
	"fmt"
	"runtime"
)

// Facts about a machine.
type Facts struct {
	// The CPU's model name.
	CPU string `json:"cpu,omitempty"`
	// How many CPUs the machine has.
	Cores int `json:"cores,omitempty"`
	// The machine's memory, in bytes.
	RAMBytes int64 `json:"ramBytes,omitempty"`
	// The operating system.
	OS string `json:"os,omitempty"`
	// The architecture.
	Arch string `json:"arch,omitempty"`
}

// Current: the facts about the machine this tool runs on.
func Current() Facts {
	return Facts{
		CPU:      cpuModel(),
		Cores:    runtime.NumCPU(),
		RAMBytes: ramBytes(),
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
	}
}

// A one-line description, for a report.
func (f Facts) String() string {
	cpu := f.CPU
	if cpu == "" {
		cpu = "an unnamed CPU"
	}
	out := fmt.Sprintf("%s", cpu)
	if f.Cores > 0 {
		out += fmt.Sprintf(" · %d cores", f.Cores)
	}
	if f.RAMBytes > 0 {
		out += fmt.Sprintf(" · %s", megabytes(f.RAMBytes))
	}
	if f.OS != "" {
		out += fmt.Sprintf(" · %s", f.OS)
	}
	return out
}

// megabytes: a number of bytes, in the biggest unit that divides it evenly.
func megabytes(bytes int64) string {
	const unit = 1024 * 1024
	if bytes < unit {
		return fmt.Sprintf("%d KB", bytes/1024)
	}
	megabytes := bytes / unit
	if megabytes < 1024 {
		return fmt.Sprintf("%d MB", megabytes)
	}
	return fmt.Sprintf("%.1f GB", float64(bytes)/(1024*1024*1024))
}
