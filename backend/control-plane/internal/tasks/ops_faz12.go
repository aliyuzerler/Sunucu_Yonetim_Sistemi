package tasks

// Faz 12: KVM/QEMU/libvirt sanallaştırma ve VPS yönetimi.
//
// Şemalar agent/internal/vmops ile AYNA şeklinde tutulur (payload'lar iki
// tarafta da strict doğrulanır — ADR-0016):
//   - Aile node'da --vm-enable ile OPT-IN'dir; modül kurulum YAPMAZ,
//     vm.preflight yalnız RAPOR verir (kriter 1).
//   - VM create/clone/start/stop/reboot/delete tipli işlemlerdir; kaynak
//     limitleri kapalı aralıklarla iki katmanda doğrulanır ve node'da host
//     gerçekleriyle (bellek, pool boş alanı) eklenir (kriter 2).
//   - Disk görüntü YOLU payload'da ASLA yoktur: çağıran yalnız pool/birim/
//     domain adı verir; yollar node'da libvirt çıktısından türetilir ve
//     vm.delete yalnız sc- önekli yönetilen birimleri kaldırır (kriter 3).
//   - Snapshot create/revert node'da GERÇEK disk formatını qemu-img ile
//     doğrular; raw disk tipli red yer (kriter 4).
//   - VM oluşturmak guest OS yönetimi SAĞLAMAZ: vm.info guest_agent
//     varlığını dürüst raporlar; in-guest modüller agent kurulana dek
//     unsupported kalır (kriter 5).

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
)

// Mirrors of the agent-side bounds (vmops).
const (
	MaxVMNameLen    = 32
	MaxVMPoolLen    = 48
	MaxVMBridgeLen  = 15
	MaxVMDescLen    = 200
	MinVMVCPU       = 1
	MaxVMVCPU       = 64
	MinVMMemMiB     = 128
	MaxVMMemMiB     = 256 << 10
	MinVMDiskGiB    = 1
	MaxVMDiskGiB    = 4096
	MaxVMList       = 200
	MaxVMPayload    = 8 << 10
	VMVolumePrefix  = "sc-"
	VMStopGraceSecs = 60
)

// vmValidName mirrors vmops.ValidVMName (lowercase DNS label).
func vmValidName(n string) bool {
	if len(n) == 0 || len(n) > MaxVMNameLen {
		return false
	}
	if n[0] < 'a' || n[0] > 'z' {
		return false
	}
	for i := 0; i < len(n); i++ {
		c := n[i]
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// vmValidPool mirrors vmops.ValidPoolName.
func vmValidPool(n string) bool {
	if len(n) == 0 || len(n) > MaxVMPoolLen {
		return false
	}
	c := n[0]
	if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
		return false
	}
	for i := 0; i < len(n); i++ {
		ch := n[i]
		ok := ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '.' || ch == '-'
		if !ok {
			return false
		}
	}
	return true
}

// vmValidBridge mirrors vmops.ValidBridge (Linux ifname rules).
func vmValidBridge(n string) bool {
	if len(n) == 0 || len(n) > MaxVMBridgeLen {
		return false
	}
	c := n[0]
	if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
		return false
	}
	for i := 0; i < len(n); i++ {
		ch := n[i]
		ok := ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '.' || ch == '-'
		if !ok {
			return false
		}
	}
	return true
}

// vmValidLimits mirrors the closed resource ranges (kriter 2).
func vmValidLimits(vcpu, memMiB, diskGiB int64) bool {
	return vcpu >= MinVMVCPU && vcpu <= MaxVMVCPU &&
		memMiB >= MinVMMemMiB && memMiB <= MaxVMMemMiB &&
		diskGiB >= MinVMDiskGiB && diskGiB <= MaxVMDiskGiB
}

// vmNamePayload is {name} (info/start/reboot/delete/disk.list/snapshot.list).
func validateVMNamePayload(payload json.RawMessage) error {
	var p struct {
		Name string `json:"name"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !vmValidName(p.Name) {
		return errors.New("VM adı allowlist dışı")
	}
	return nil
}

// vmListPayload is {limit?}.
func validateVMListPayload(payload json.RawMessage) error {
	var p struct {
		Limit int `json:"limit,omitempty"`
	}
	if len(payload) == 0 {
		return nil
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if p.Limit < 0 || p.Limit > MaxVMList {
		return errors.New("limit 0..200 olmalı")
	}
	return nil
}

// vmCreatePayload is {name, vcpu, mem_mib, disk_gib, pool, bridge}.
func validateVMCreatePayload(payload json.RawMessage) error {
	var p struct {
		Name    string `json:"name"`
		VCPU    int64  `json:"vcpu"`
		MemMiB  int64  `json:"mem_mib"`
		DiskGiB int64  `json:"disk_gib"`
		Pool    string `json:"pool"`
		Bridge  string `json:"bridge"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !vmValidName(p.Name) || !vmValidPool(p.Pool) || !vmValidBridge(p.Bridge) {
		return errors.New("vm adları allowlist dışı")
	}
	if !vmValidLimits(p.VCPU, p.MemMiB, p.DiskGiB) {
		return errors.New("kaynak limitleri aralık dışı (vCPU 1..64, RAM 128..262144 MiB, disk 1..4096 GiB)")
	}
	return nil
}

// vmClonePayload is {source, name} — adlar farklı olmalı.
func validateVMClonePayload(payload json.RawMessage) error {
	var p struct {
		Source string `json:"source"`
		Name   string `json:"name"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !vmValidName(p.Source) || !vmValidName(p.Name) || p.Source == p.Name {
		return errors.New("klon adları allowlist dışı")
	}
	return nil
}

// vmStopPayload is {name, force?} — force yokken graceful shutdown,
// zaman aşımında tipli çelişki (node asla kendi kendine destroy etmez).
func validateVMStopPayload(payload json.RawMessage) error {
	var p struct {
		Name  string `json:"name"`
		Force bool   `json:"force,omitempty"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !vmValidName(p.Name) {
		return errors.New("VM adı allowlist dışı")
	}
	return nil
}

// vmDiskAddPayload is {name, pool, size_gib} — yol yok (kriter 3).
func validateVMDiskAddPayload(payload json.RawMessage) error {
	var p struct {
		Name    string `json:"name"`
		Pool    string `json:"pool"`
		SizeGiB int64  `json:"size_gib"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !vmValidName(p.Name) || !vmValidPool(p.Pool) {
		return errors.New("adlar allowlist dışı")
	}
	if p.SizeGiB < MinVMDiskGiB || p.SizeGiB > MaxVMDiskGiB {
		return errors.New("disk boyutu 1..4096 GiB olmalı")
	}
	return nil
}

// vmSnapshotCreatePayload is {name, snapshot, description?}.
func validateVMSnapshotCreatePayload(payload json.RawMessage) error {
	var p struct {
		Name        string `json:"name"`
		Snapshot    string `json:"snapshot"`
		Description string `json:"description,omitempty"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !vmValidName(p.Name) || !vmValidName(p.Snapshot) {
		return errors.New("snapshot adı allowlist dışı")
	}
	if len(p.Description) > MaxVMDescLen {
		return errors.New("açıklama en fazla 200 karakter")
	}
	return nil
}

// vmSnapshotPayload is {name, snapshot} (revert/delete).
func validateVMSnapshotPayload(payload json.RawMessage) error {
	var p struct {
		Name     string `json:"name"`
		Snapshot string `json:"snapshot"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !vmValidName(p.Name) || !vmValidName(p.Snapshot) {
		return errors.New("snapshot adları allowlist dışı")
	}
	return nil
}

// buildFaz12Registry returns the Faz 12 operation specs (16 vm ops).
func buildFaz12Registry() []*OperationSpec {
	emptyOnly := func(p json.RawMessage) error {
		var v struct{}
		if len(p) == 0 {
			return nil
		}
		if err := decodeStrict(p, &v); err != nil {
			return fmt.Errorf("şema uyumsuz: %w", err)
		}
		return nil
	}
	node := func(name, version string, class Class, retry bool, timeout time.Duration, maxPayload int, validate func(json.RawMessage) error, perm string) *OperationSpec {
		return &OperationSpec{
			Name: name, Version: version, Class: class, Target: TargetNode,
			RetrySafe: retry, Timeout: timeout, MaxPayload: maxPayload,
			Validate: validate, RequiredPerm: perm,
		}
	}
	return []*OperationSpec{
		// preflight/status/list/info/disk.list/snapshot.list: salt okuma.
		// vm.info console METADATA döner; akış/proxy yok (kriter 5 notu).
		node("vm.preflight", "1.0.0", ClassReadOnly, true, 60*time.Second, 1<<10, emptyOnly, rbac.PermVMRead),
		node("vm.status", "1.0.0", ClassReadOnly, true, 60*time.Second, 1<<10, emptyOnly, rbac.PermVMRead),
		node("vm.list", "1.0.0", ClassReadOnly, true, 60*time.Second, 1<<10, validateVMListPayload, rbac.PermVMRead),
		node("vm.info", "1.0.0", ClassReadOnly, true, 60*time.Second, 1<<10, validateVMNamePayload, rbac.PermVMRead),
		node("vm.disk.list", "1.0.0", ClassReadOnly, true, 60*time.Second, 1<<10, validateVMNamePayload, rbac.PermVMRead),
		node("vm.snapshot.list", "1.0.0", ClassReadOnly, true, 60*time.Second, 1<<10, validateVMNamePayload, rbac.PermVMRead),

		// create: istenen son durum "VM tanımlı" → retry yakınsar; zaten
		// varsa tipli ErrExists dürüst çelişki bildirimidir (mail deseni).
		node("vm.create", "1.0.0", ClassIdempotent, true, 5*time.Minute, MaxVMPayload, validateVMCreatePayload, rbac.PermVMCtrl),
		// clone: duran (shut off) kaynaktan; retry-güvensiz (yarım klon
		// kaynak tüketir; ikinci koşu ErrExists verir).
		node("vm.clone", "1.0.0", ClassNonIdem, false, 5*time.Minute, 1<<10, validateVMClonePayload, rbac.PermVMCtrl),
		// start/stop: istenen son durum semantiği (running/shut off);
		// zaten hedefteyse dürüst no-op. stop force=true → destroy açık
		// çağıran kararıdır (node kendi kendine yükseltmez).
		node("vm.start", "1.0.0", ClassIdempotent, true, 5*time.Minute, 1<<10, validateVMNamePayload, rbac.PermVMCtrl),
		node("vm.stop", "1.0.0", ClassIdempotent, true, 5*time.Minute, 1<<10, validateVMStopPayload, rbac.PermVMCtrl),
		// reboot: yalnız çalışan VM'de; tekrarı istenen bir davranış değil.
		node("vm.reboot", "1.0.0", ClassNonIdem, false, 5*time.Minute, 1<<10, validateVMNamePayload, rbac.PermVMCtrl),
		// delete: destructive + retry-güvensiz; yalnız sc- birimleri
		// kaldırır, yabancı diskte TİPLİ red (kriter 3).
		node("vm.delete", "1.0.0", ClassDestructive, false, 5*time.Minute, 1<<10, validateVMNamePayload, rbac.PermVMCtrl),
		// disk.add: havuzda yönetilen qcow2 birimi yaratıp persistent
		// attach eder; attach başarısızsa birim geri alınır.
		node("vm.disk.add", "1.0.0", ClassIdempotent, true, 5*time.Minute, 1<<10, validateVMDiskAddPayload, rbac.PermVMCtrl),
		// snapshot.create: qcow2 kapısı node'da (kriter 4); ad çakışması
		// tipli ErrExists.
		node("vm.snapshot.create", "1.0.0", ClassIdempotent, true, 5*time.Minute, MaxVMPayload, validateVMSnapshotCreatePayload, rbac.PermVMCtrl),
		// snapshot.revert: ara durumu DISKARD eder — retry-güvensiz.
		node("vm.snapshot.revert", "1.0.0", ClassNonIdem, false, 5*time.Minute, 1<<10, validateVMSnapshotPayload, rbac.PermVMCtrl),
		node("vm.snapshot.delete", "1.0.0", ClassDestructive, false, 5*time.Minute, 1<<10, validateVMSnapshotPayload, rbac.PermVMCtrl),
	}
}
