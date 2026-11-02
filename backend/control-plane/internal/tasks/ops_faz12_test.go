package tasks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVMValidNameMirror(t *testing.T) {
	good := []string{"vm1", "web-01", "a", "vm-0123456789012345678901234567"}
	for _, n := range good {
		if !vmValidName(n) {
			t.Errorf("vmValidName(%q) = false, want true", n)
		}
	}
	bad := []string{"", "VM1", "1vm", "-vm", "vm_1", "vm.1", "vm;rm", "vm<br>",
		"../../etc", "a/b", "vm space", "vm-01234567890123456789012345678901"}
	for _, n := range bad {
		if vmValidName(n) {
			t.Errorf("vmValidName(%q) = true, want false", n)
		}
	}
	if !vmValidPool("pool-1") || vmValidPool("") || vmValidPool("/etc") ||
		vmValidPool(".hidden") || vmValidPool("a b") {
		t.Error("pool mirror sınırı hatalı")
	}
	if !vmValidBridge("br0") || !vmValidBridge("virbr0") || vmValidBridge("") ||
		vmValidBridge("verylongbridgename16") || vmValidBridge("a/b") {
		t.Error("bridge mirror sınırı hatalı")
	}
	if !vmValidLimits(1, 128, 1) || !vmValidLimits(64, 256<<10, 4096) {
		t.Error("kapalı aralık uçları kabul edilmeli (kriter 2)")
	}
	if vmValidLimits(0, 128, 1) || vmValidLimits(65, 128, 1) ||
		vmValidLimits(1, 127, 1) || vmValidLimits(1, 256<<10+1, 1) ||
		vmValidLimits(1, 128, 0) || vmValidLimits(1, 128, 4097) {
		t.Error("aralık dışı kaynaklar reddedilmeli (kriter 2)")
	}
}

func TestVMValidatorsRejectBadPayloads(t *testing.T) {
	bodies := []string{
		// create: traversal/enjeksiyon adları, limit ihlalleri, eksik alan.
		`{"name":"../../etc","vcpu":2,"mem_mib":512,"disk_gib":20,"pool":"default","bridge":"br0"}`,
		`{"name":"VM1","vcpu":2,"mem_mib":512,"disk_gib":20,"pool":"default","bridge":"br0"}`,
		`{"name":"vm1","vcpu":0,"mem_mib":512,"disk_gib":20,"pool":"default","bridge":"br0"}`,
		`{"name":"vm1","vcpu":2,"mem_mib":127,"disk_gib":20,"pool":"default","bridge":"br0"}`,
		`{"name":"vm1","vcpu":2,"mem_mib":512,"disk_gib":4097,"pool":"default","bridge":"br0"}`,
		`{"name":"vm1","vcpu":2,"mem_mib":512,"disk_gib":20,"pool":"/etc/evil","bridge":"br0"}`,
		`{"name":"vm1","vcpu":2,"mem_mib":512,"disk_gib":20,"pool":"default","bridge":"a b"}`,
		`{"name":"vm1","vcpu":2,"mem_mib":512,"disk_gib":20,"pool":"default","bridge":"br0","x":1}`,
		`{"name":"vm1","vcpu":2,"mem_mib":512,"pool":"default","bridge":"br0"}`,
		// clone: aynı ad yasak.
		`{"source":"src","name":"src"}`,
		`{"source":"../src","name":"c1"}`,
		// stop: bozuk force türü.
		`{"name":"vm1","force":"yes"}`,
		// disk.add: sıfır boyut, traversal pool.
		`{"name":"vm1","pool":"default","size_gib":0}`,
		`{"name":"vm1","pool":"a/b","size_gib":10}`,
		// snapshot: boşluklu ad, aşırı açıklama.
		`{"name":"vm1","snapshot":"snap 1"}`,
		`{"name":"vm1","snapshot":"snap1","description":"` + strings.Repeat("x", 300) + `"}`,
		// info: ad yok.
		`{}`,
		`{"name":"vm1","extra":1}`,
	}
	for i, body := range bodies {
		var err error
		switch {
		case strings.Contains(body, `"snapshot"`):
			if strings.Contains(body, `"description"`) {
				err = validateVMSnapshotCreatePayload(json.RawMessage(body))
			} else {
				err = validateVMSnapshotPayload(json.RawMessage(body))
			}
		case strings.Contains(body, `"source"`):
			err = validateVMClonePayload(json.RawMessage(body))
		case strings.Contains(body, `"force"`):
			err = validateVMStopPayload(json.RawMessage(body))
		case strings.Contains(body, `"size_gib"`):
			err = validateVMDiskAddPayload(json.RawMessage(body))
		case strings.Contains(body, `"vcpu"`):
			err = validateVMCreatePayload(json.RawMessage(body))
		default:
			err = validateVMNamePayload(json.RawMessage(body))
		}
		if err == nil {
			t.Errorf("bozuk payload[%d] reddedilmeli: %s", i, body)
		}
	}
}

func TestVMValidatorsAcceptGoodPayloads(t *testing.T) {
	if err := validateVMCreatePayload(json.RawMessage(
		`{"name":"web-01","vcpu":2,"mem_mib":512,"disk_gib":20,"pool":"pool-1","bridge":"virbr0"}`)); err != nil {
		t.Fatalf("geçerli create reddedildi: %v", err)
	}
	if err := validateVMClonePayload(json.RawMessage(`{"source":"src","name":"clone1"}`)); err != nil {
		t.Fatalf("geçerli clone reddedildi: %v", err)
	}
	if err := validateVMStopPayload(json.RawMessage(`{"name":"vm1","force":true}`)); err != nil {
		t.Fatalf("geçerli stop reddedildi: %v", err)
	}
	if err := validateVMDiskAddPayload(json.RawMessage(`{"name":"vm1","pool":"default","size_gib":10}`)); err != nil {
		t.Fatalf("geçerli disk add reddedildi: %v", err)
	}
	if err := validateVMSnapshotCreatePayload(json.RawMessage(
		`{"name":"vm1","snapshot":"snap1","description":"temiz kurulum"}`)); err != nil {
		t.Fatalf("geçerli snapshot reddedildi: %v", err)
	}
	if err := validateVMListPayload(json.RawMessage(`{"limit":10}`)); err != nil {
		t.Fatalf("geçerli list reddedildi: %v", err)
	}
	if err := validateVMListPayload(json.RawMessage(`{"limit":201}`)); err == nil {
		t.Fatal("200 üstü limit reddedilmeli")
	}
}

func TestBuildFaz12RegistryShapes(t *testing.T) {
	specs := buildFaz12Registry()
	if len(specs) != 16 {
		t.Fatalf("16 spec bekleniyordu: %d", len(specs))
	}
	byName := map[string]*OperationSpec{}
	for _, s := range specs {
		byName[s.Name] = s
	}
	// Tümü node hedefli ve vm izinli olmalı.
	for name, s := range byName {
		if s.Target != TargetNode {
			t.Errorf("%s hedefi node olmalı", name)
		}
		if s.RequiredPerm != "vm:read" && s.RequiredPerm != "vm:control" {
			t.Errorf("%s izni vm:* olmalı: %q", name, s.RequiredPerm)
		}
		if strings.Contains(name, ".list") || name == "vm.preflight" || name == "vm.status" || name == "vm.info" {
			if s.RequiredPerm != "vm:read" {
				t.Errorf("%s salt-okuma olmalı (vm:read): %q", name, s.RequiredPerm)
			}
		}
	}
	// Mutasyon yüzeyi vm:control olmalı.
	for _, name := range []string{"vm.create", "vm.clone", "vm.start", "vm.stop",
		"vm.reboot", "vm.delete", "vm.disk.add", "vm.snapshot.create",
		"vm.snapshot.revert", "vm.snapshot.delete"} {
		if byName[name].RequiredPerm != "vm:control" {
			t.Errorf("%s vm:control olmalı", name)
		}
	}
	// delete/revert ailesi retry-güvensiz + destructive; clone/reboot
	// non-idempotent; start/stop/create/disk.add/snapshot.create
	// idempotent (istenen son duruma yakınsama) olmalı (db deseni).
	for _, name := range []string{"vm.delete", "vm.snapshot.delete"} {
		if byName[name].Class != ClassDestructive || byName[name].RetrySafe {
			t.Errorf("%s destructive + retry-güvensiz olmalı", name)
		}
	}
	for _, name := range []string{"vm.clone", "vm.reboot", "vm.snapshot.revert"} {
		if byName[name].Class != ClassNonIdem || byName[name].RetrySafe {
			t.Errorf("%s non-idempotent + retry-güvensiz olmalı", name)
		}
	}
	for _, name := range []string{"vm.create", "vm.start", "vm.stop", "vm.disk.add", "vm.snapshot.create"} {
		if byName[name].Class != ClassIdempotent || !byName[name].RetrySafe {
			t.Errorf("%s idempotent + retry-güvenli olmalı", name)
		}
	}
	// create payload tavanı vmops mirror (8 KiB).
	if byName["vm.create"].MaxPayload != MaxVMPayload {
		t.Errorf("vm.create payload tavanı hatalı: %d", byName["vm.create"].MaxPayload)
	}
}
