//go:build linux

package metrics

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// realFSTypes are filesystems backed by persistent block devices; everything
// else in /proc/mounts (procfs, sysfs, tmpfs, squashfs, …) is skipped.
var realFSTypes = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true,
	"f2fs": true, "jfs": true, "reiserfs": true, "zfs": true,
	"vfat": true, "exfat": true, "ntfs": true, "ntfs3": true, "ufs": true,
}

// Collect reads procfs (+statfs for disks) and returns one snapshot.
func (c *Collector) Collect() (*Snapshot, error) {
	now := c.nowFunc().UTC()
	snap := &Snapshot{CollectedAt: now, Source: SourceProcfs}

	// CPU: /proc/stat first cpu line.
	if times, ok := parseCPUStat(filepath.Join(c.root, "proc", "stat")); ok {
		snap.CPU.Percent = c.applyCPU(times, now)
	}
	snap.CPU.LogicalCores = logicalCores()
	if la, ok := parseLoadAvg(filepath.Join(c.root, "proc", "loadavg")); ok {
		snap.CPU.Load1m, snap.CPU.Load5m, snap.CPU.Load15m = la[0], la[1], la[2]
	}

	// Memory: /proc/meminfo.
	if m, ok := parseMemInfo(filepath.Join(c.root, "proc", "meminfo")); ok {
		snap.Memory = m
	}

	// Disks: real mounts only; one entry per DEVICE (first mount wins) so a
	// bind-duplicated filesystem is not double counted; statfs adds usage.
	mounts := parseMounts(filepath.Join(c.root, "proc", "mounts"))
	seen := map[string]bool{}
	for _, m := range mounts {
		if !realFSTypes[m.fstype] || seen[m.device] {
			continue
		}
		seen[m.device] = true
		d := DiskMetrics{Device: m.device, MountPoint: m.mount, FSType: m.fstype}
		if st, err := statFS(m.mount); err == nil {
			d.TotalBytes = st.Blocks * uint64(st.Bsize)
			d.AvailableBytes = st.Bavail * uint64(st.Bsize)
			d.UsedBytes = d.TotalBytes - (st.Bfree * uint64(st.Bsize))
			snap.Source = SourceProcfs + "+" + SourceStatfs
		}
		snap.Disks = append(snap.Disks, d)
	}
	snap.Disks = boundSlice(snap.Disks, 64)

	// Network: /proc/net/dev (loopback excluded).
	snap.Networks = boundSlice(parseNetDev(filepath.Join(c.root, "proc", "net", "dev")), 64)

	// Uptime: /proc/uptime.
	if up, ok := parseUptime(filepath.Join(c.root, "proc", "uptime")); ok {
		snap.UptimeSeconds = up
	}
	return snap, nil
}

// ---- ayrıştırıcılar (saf; fixture testleri birim testlerde doğrudan çağırır) ----

func parseCPUStat(path string) (cpuTimes, bool) {
	b, err := os.ReadFile(path) //nolint:gosec // root-joined by Collector
	if err != nil {
		return cpuTimes{}, false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuTimes{}, false
	}
	var sum, idle uint64
	for i, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return cpuTimes{}, false
		}
		sum += v
		if i == 3 { // idle (usr,nice,sys,idle,iowait,...)
			idle = v
		}
	}
	if sum == 0 {
		return cpuTimes{}, false
	}
	return cpuTimes{idle: idle, total: sum}, true
}

func parseLoadAvg(path string) ([3]float64, bool) {
	b, err := os.ReadFile(path) //nolint:gosec // root-joined by Collector
	if err != nil {
		return [3]float64{}, false
	}
	fields := strings.Fields(string(b))
	if len(fields) < 3 {
		return [3]float64{}, false
	}
	var out [3]float64
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return out, false
		}
		out[i] = v
	}
	return out, true
}

func parseMemInfo(path string) (MemoryMetrics, bool) {
	fh, err := os.Open(path) //nolint:gosec // root-joined by Collector
	if err != nil {
		return MemoryMetrics{}, false
	}
	defer fh.Close()
	var total, avail uint64
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		k, v, ok := cutMemField(sc.Text())
		if !ok {
			continue
		}
		switch k {
		case "MemTotal":
			total = v
		case "MemAvailable":
			avail = v
		}
	}
	if total == 0 {
		return MemoryMetrics{}, false
	}
	m := MemoryMetrics{TotalBytes: total << 10, AvailableBytes: avail << 10}
	m.UsedBytes = m.TotalBytes - m.AvailableBytes
	if m.TotalBytes > 0 {
		m.UsedPercent = clampPercent(100 * float64(m.UsedBytes) / float64(m.TotalBytes))
	}
	return m, true
}

func cutMemField(line string) (string, uint64, bool) {
	line = strings.TrimSpace(line)
	k, rest, ok := strings.Cut(line, ":")
	if !ok {
		return "", 0, false
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", 0, false
	}
	v, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return strings.TrimSpace(k), v, true
}

type mountEntry struct{ device, mount, fstype string }

func parseMounts(path string) []mountEntry {
	fh, err := os.Open(path) //nolint:gosec // root-joined by Collector
	if err != nil {
		return nil
	}
	defer fh.Close()
	var out []mountEntry
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 4<<10), 64<<10)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		dev := unescapeMount(fields[0])
		mnt := unescapeMount(fields[1])
		out = append(out, mountEntry{device: dev, mount: mnt, fstype: fields[2]})
	}
	return out
}

// unescapeMount decodes the octal escapes used in /proc/mounts (space=\040…).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 32); err == nil && v < 128 {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func parseNetDev(path string) []NetworkMetrics {
	fh, err := os.Open(path) //nolint:gosec // root-joined by Collector
	if err != nil {
		return nil
	}
	defer fh.Close()
	var out []NetworkMetrics
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 4<<10), 64<<10)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, "|") && !strings.Contains(line, ":") {
			continue // başlık satırları
		}
		iface, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		iface = strings.TrimSpace(iface)
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(fields[0], 10, 64)
		tx, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, NetworkMetrics{Interface: iface, RxBytes: rx, TxBytes: tx})
	}
	return out
}

func parseUptime(path string) (float64, bool) {
	b, err := os.ReadFile(path) //nolint:gosec // root-joined by Collector
	if err != nil {
		return 0, false
	}
	first, _, _ := strings.Cut(string(b), " ")
	v, err := strconv.ParseFloat(strings.TrimSpace(first), 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

func logicalCores() uint32 {
	n := runtime.NumCPU()
	if n < 1 {
		return 1
	}
	return uint32(n)
}

func statFS(path string) (syscall.Statfs_t, error) {
	var st syscall.Statfs_t
	err := syscall.Statfs(path, &st)
	return st, err
}
