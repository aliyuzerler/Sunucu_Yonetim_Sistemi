// Package metrics collects basic host metric snapshots WITHOUT third-party
// dependencies. Every snapshot carries its collection timestamp and data
// source. Per-GOOS collection lives in collect_linux.go (procfs/statfs) and
// collect_windows.go (Win32 API, ADR-0009); types and the CPU-window state
// machine are shared here.
package metrics

import (
	"sync"
	"time"
)

// Source identifiers (provenance, reported with every snapshot).
const (
	SourceProcfs = "procfs"
	SourceStatfs = "statfs"
	SourceWinapi = "winapi"
	SourceIPHLP  = "iphlpapi"
)

// CPUMetrics: Percent is busy-% over the window since the previous Collect
// (0.0 for the first sample of a Collector).
type CPUMetrics struct {
	Percent      float64
	LogicalCores uint32
	Load1m       float64
	Load5m       float64
	Load15m      float64
}

type MemoryMetrics struct {
	TotalBytes     uint64
	AvailableBytes uint64
	UsedBytes      uint64
	UsedPercent    float64
}

type DiskMetrics struct {
	Device         string
	MountPoint     string
	FSType         string
	TotalBytes     uint64
	UsedBytes      uint64
	AvailableBytes uint64
}

type NetworkMetrics struct {
	Interface string
	RxBytes   uint64
	TxBytes   uint64
}

// Snapshot is one point-in-time metric sample.
type Snapshot struct {
	CollectedAt   time.Time
	Source        string
	CPU           CPUMetrics
	Memory        MemoryMetrics
	Disks         []DiskMetrics
	Networks      []NetworkMetrics
	UptimeSeconds float64
}

// Collector keeps the previous CPU sample to compute busy-% over a window.
type Collector struct {
	root string

	mu      sync.Mutex
	lastCPU cpuTimes
	lastAt  time.Time
	hasLast bool
	nowFunc func() time.Time
}

type cpuTimes struct {
	idle  uint64
	total uint64
}

// NewCollector builds a collector reading from root ("" or "/" = real host;
// testdata fixtures on linux; ignored on windows).
func NewCollector(root string) *Collector {
	return &Collector{root: root, nowFunc: time.Now}
}

// applyCPU folds one (idle,total) sample into the window and returns the
// busy-% since the previous sample (0.0 for the first sample).
func (c *Collector) applyCPU(sample cpuTimes, now time.Time) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var pct float64
	if c.hasLast && sample.total > c.lastCPU.total {
		idleDelta := float64(sample.idle - c.lastCPU.idle)
		totalDelta := float64(sample.total - c.lastCPU.total)
		pct = clampPercent(100 * (1 - idleDelta/totalDelta))
	}
	c.lastCPU, c.lastAt, c.hasLast = sample, now, true
	return pct
}

// boundSlice caps a collected slice to the platform limit.
func boundSlice[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
