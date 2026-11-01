//go:build windows

package metrics

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGetVolumeInfo        = kernel32.NewProc("GetVolumeInformationW")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetTickCount64       = kernel32.NewProc("GetTickCount64")
)

// filetime is a raw FILETIME (64-bit 100ns units).
type filetime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

func (ft filetime) value() uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}

// memoryStatusEx mirrors MEMORYSTATUSEX (GlobalMemoryStatusEx).
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// Collect reads host metrics via the Win32 API (kernel32/iphlpapi through
// x/sys/windows LazyProc; NO third-party dependency, NO PowerShell —
// ADR-0009). CPU: GetSystemTimes, Memory: GlobalMemoryStatusEx, Disks: fixed
// logical volumes via GetDriveType+GetDiskFreeSpaceEx, Networks: GetIfTable,
// Uptime: GetTickCount64.
func (c *Collector) Collect() (*Snapshot, error) {
	now := c.nowFunc().UTC()
	snap := &Snapshot{CollectedAt: now, Source: SourceWinapi}

	// CPU: GetSystemTimes; Windows kernel time INCLUDES idle, so the
	// (idle, kernel+user) pair has the same semantics as /proc/stat.
	var idle, kernel, user filetime
	if err := procGetSystemTimes.Find(); err == nil {
		r1, _, _ := procGetSystemTimes.Call(
			uintptr(unsafe.Pointer(&idle)),
			uintptr(unsafe.Pointer(&kernel)),
			uintptr(unsafe.Pointer(&user)))
		if r1 != 0 {
			idleN := idle.value()
			totalN := kernel.value() + user.value()
			snap.CPU.Percent = c.applyCPU(cpuTimes{idle: idleN, total: totalN}, now)
		}
	}
	snap.CPU.LogicalCores = logicalCores()

	// Memory: GlobalMemoryStatusEx.
	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	if err := procGlobalMemoryStatusEx.Find(); err == nil {
		r1, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
		if r1 != 0 {
			m := MemoryMetrics{TotalBytes: ms.TotalPhys, AvailableBytes: ms.AvailPhys}
			m.UsedBytes = m.TotalBytes - m.AvailableBytes
			if m.TotalBytes > 0 {
				m.UsedPercent = clampPercent(100 * float64(m.UsedBytes) / float64(m.TotalBytes))
			}
			snap.Memory = m
		}
	}

	// Disks: FIXED logical volumes only (no CD-ROM, no network shares —
	// basic host metrics; remote drives would double-count NAS from many
	// nodes and can stall on dead links).
	for _, vol := range fixedVolumes() {
		d := DiskMetrics{Device: vol, MountPoint: vol}
		d.FSType = volumeFS(vol)
		var free, total, totalFree uint64
		if err := windows.GetDiskFreeSpaceEx(
			windows.StringToUTF16Ptr(vol), &free, &total, &totalFree); err == nil {
			d.TotalBytes = total
			d.AvailableBytes = totalFree
			d.UsedBytes = total - totalFree
		}
		snap.Disks = append(snap.Disks, d)
	}
	snap.Disks = boundSlice(snap.Disks, 64)

	// Network: GetIfTable (MIB_IFROW — fixed layout since NT4; parsed with
	// explicit offsets, fixture-tested in netstat_windows_test.go).
	if nets, err := collectIfTable(); err == nil && len(nets) > 0 {
		snap.Networks = nets
		snap.Source = SourceWinapi + "+" + SourceIPHLP
	}

	// Uptime: GetTickCount64 (ms since boot).
	if err := procGetTickCount64.Find(); err == nil {
		r1, _, _ := procGetTickCount64.Call()
		snap.UptimeSeconds = float64(r1) / 1000.0
	}
	return snap, nil
}

func logicalCores() uint32 {
	n := runtime.NumCPU()
	if n < 1 {
		return 1
	}
	return uint32(n)
}

// fixedVolumes returns local fixed-drive roots (C:\, D:\, …) sorted.
func fixedVolumes() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		if windows.GetDriveType(windows.StringToUTF16Ptr(root)) == windows.DRIVE_FIXED {
			out = append(out, root)
		}
	}
	sort.Strings(out)
	return out
}

// volumeFS returns the filesystem name of a volume root ("NTFS", "ReFS")
// or "" when the query fails (never fatal).
func volumeFS(root string) string {
	if err := procGetVolumeInfo.Find(); err != nil {
		return ""
	}
	name := make([]uint16, 64)
	r1, _, _ := procGetVolumeInfo.Call(
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(root))),
		uintptr(unsafe.Pointer(&name[0])), 0,
		0, 0, 0, 0, 0)
	if r1 == 0 {
		return ""
	}
	return strings.TrimRight(windows.UTF16ToString(name), "\x00")
}

// MIB_IFROW layout (fixed since NT4; offsets in bytes). We only read the
// fields we need; the stride is the full C size (856).
const (
	mibIfRowSize      = 856
	offIndex          = 512 // DWORD dwIndex
	offOperStatus     = 544 // DWORD dwOperStatus
	offInOctets       = 552 // DWORD dwInOctets
	offOutOctets      = 556 // DWORD dwOutOctets
	offName           = 0   // WCHAR wszName[256]
	mibNameWchars     = 256
	offType           = 516 // DWORD dwType
	ifTypeLoopback    = 24
	maxNetworkEntries = 64
)

var (
	iphlpapi       = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetIfTable = iphlpapi.NewProc("GetIfTable")
)

// collectIfTable fetches interface counters via GetIfTable. The table is
// parsed with explicit MIB_IFROW offsets; non-operational and loopback
// interfaces are skipped.
func collectIfTable() ([]NetworkMetrics, error) {
	if err := procGetIfTable.Find(); err != nil {
		return nil, err
	}
	var size uint32
	// 1. çağrı: gereken boyutu sor (ERROR_INSUFFICIENT_BUFFER beklenir).
	r1, _, _ := procGetIfTable.Call(0, uintptr(unsafe.Pointer(&size)), 0)
	if r1 != 0 && size == 0 {
		return nil, fmt.Errorf("GetIfTable boyut sorgusu başarısız: %d", r1)
	}
	if size < mibIfRowSize || size > 8<<20 {
		return nil, fmt.Errorf("GetIfTable boyutu şüpheli: %d", size)
	}
	buf := make([]byte, size)
	r1, _, _ = procGetIfTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)), 0)
	if r1 != 0 {
		return nil, fmt.Errorf("GetIfTable başarısız: %d", r1)
	}
	return parseIfTable(buf), nil
}

// parseIfTable walks a MIB_IFTABLE buffer (pure; fixture-tested everywhere).
func parseIfTable(buf []byte) []NetworkMetrics {
	if len(buf) < 4 {
		return nil
	}
	n := uint32At(buf, 0)
	if n > 4096 || 4+int(n)*mibIfRowSize > len(buf) {
		return nil
	}
	out := make([]NetworkMetrics, 0, n)
	for i := uint32(0); i < n && len(out) < maxNetworkEntries; i++ {
		row := buf[4+int(i)*mibIfRowSize:]
		if uint32At(row, offType) == ifTypeLoopback {
			continue
		}
		if uint32At(row, offOperStatus) == 0 { // NON_OPERATIONAL
			continue
		}
		rx := uint64(uint32At(row, offInOctets))
		tx := uint64(uint32At(row, offOutOctets))
		out = append(out, NetworkMetrics{Interface: ifRowName(row), RxBytes: rx, TxBytes: tx})
	}
	return out
}

func uint32At(b []byte, off int) uint32 {
	if len(b) < off+4 {
		return 0
	}
	return *(*uint32)(unsafe.Pointer(&b[off]))
}

// ifRowName extracts the printable prefix of wszName[256]; falls back to
// the interface index shape used by many tools ("if<N>") when empty or
// non-printable.
func ifRowName(row []byte) string {
	end := 0
	for i := 0; i < mibNameWchars && 2*i+1 < len(row); i++ {
		ch := *(*uint16)(unsafe.Pointer(&row[2*i]))
		if ch == 0 {
			break
		}
		if ch < 32 || ch > 126 {
			return fmt.Sprintf("if%d", uint32At(row, offIndex))
		}
		end++
	}
	name := strings.TrimSpace(windows.UTF16ToString(utf16Runes(row[:2*end])))
	if name == "" {
		name = fmt.Sprintf("if%d", uint32At(row, offIndex))
	}
	return name
}

func utf16Runes(b []byte) []uint16 {
	out := make([]uint16, len(b)/2)
	for i := range out {
		out[i] = *(*uint16)(unsafe.Pointer(&b[2*i]))
	}
	return out
}
