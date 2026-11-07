//go:build darwin

package machine

import (
	"os/exec"
	"strconv"
	"strings"
)

// cpuModel: the CPU's name, out of `sysctl`.
func cpuModel() string {
	return sysctl("machdep.cpu.brand_string")
}

// ramBytes: the machine's memory, out of `sysctl`.
func ramBytes() int64 {
	value, err := strconv.ParseInt(sysctl("hw.memsize"), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

// sysctl: one value out of `sysctl -n`.
func sysctl(key string) string {
	output, err := exec.Command("sysctl", "-n", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
