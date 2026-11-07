//go:build !linux && !darwin

package machine

// cpuModel: there is no portable way to ask, and this tool does not need one
// badly enough to add a dependency for it.
func cpuModel() string { return "" }

// ramBytes: the same.
func ramBytes() int64 { return 0 }
