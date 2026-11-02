//go:build unix

// Faz 12 broker katmanı testleri (db/fs/mail ailelerindeki üç-katman
// kanıt disiplininin vm karşılığı — ADR-0016):
//   - Kapı VARSAYILAN KAPALI: Config.VM nil → tipli unsupported.
//   - Strict şema negatifleri: traversal/enjeksiyon adları, limit ihlalleri
//     ve bilinmeyen alanlar broker'a varmadan reddedilir (runner'a gitmez).
//   - Yürütme: runner bağlıyken happy yolu + runner hata kategorisi
//     eşlemesi (ErrDiskFormat/ErrForeignVolume → executor_error;
//     ErrInvalidName → invalid_params; ErrUnavailable → unsupported).

package broker

import (
	"context"
	"os"
	"testing"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/vmops"
)

type fakeVMRunner struct {
	err error
}

func (f *fakeVMRunner) Preflight(context.Context) (vmops.PreflightResult, error) {
	return vmops.PreflightResult{URI: "qemu:///test", KVMAvailable: true}, f.err
}
func (f *fakeVMRunner) Status(context.Context) (vmops.StatusResult, error) {
	return vmops.StatusResult{DomainsTotal: 1, Running: 1}, f.err
}
func (f *fakeVMRunner) List(context.Context, int) ([]vmops.VMRow, error) {
	return []vmops.VMRow{{Name: "vm1", State: "shut off"}}, f.err
}
func (f *fakeVMRunner) Info(context.Context, string) (vmops.VMInfo, error) {
	return vmops.VMInfo{Name: "vm1", VCPU: 2, MemMiB: 512, GuestAgent: true}, f.err
}
func (f *fakeVMRunner) Create(context.Context, string, int, int64, int64, string, string) (vmops.CreateResult, error) {
	return vmops.CreateResult{Name: "vm1", Volume: "sc-vm1-root", Accelerator: "kvm"}, f.err
}
func (f *fakeVMRunner) Clone(context.Context, string, string) (vmops.CloneResult, error) {
	return vmops.CloneResult{Source: "src", Name: "clone"}, f.err
}
func (f *fakeVMRunner) Start(context.Context, string) (vmops.MutateResult, error) {
	return vmops.MutateResult{Name: "vm1", Action: "start"}, f.err
}
func (f *fakeVMRunner) Stop(context.Context, string, bool) (vmops.StopResult, error) {
	return vmops.StopResult{Name: "vm1", Forced: true}, f.err
}
func (f *fakeVMRunner) Reboot(context.Context, string) (vmops.MutateResult, error) {
	return vmops.MutateResult{Name: "vm1", Action: "reboot"}, f.err
}
func (f *fakeVMRunner) Delete(context.Context, string) (vmops.DeleteResult, error) {
	return vmops.DeleteResult{Name: "vm1", VolumesRemoved: []string{"default/sc-vm1-root"}}, f.err
}
func (f *fakeVMRunner) DiskList(context.Context, string) ([]vmops.DiskInfo, error) {
	return []vmops.DiskInfo{{TargetDev: "vda", ActualFormat: "qcow2", FormatVerified: true}}, f.err
}
func (f *fakeVMRunner) DiskAdd(context.Context, string, string, int64) (vmops.DiskAddResult, error) {
	return vmops.DiskAddResult{Name: "vm1", Volume: "sc-vm1-vdb", TargetDev: "vdb"}, f.err
}
func (f *fakeVMRunner) SnapshotList(context.Context, string) ([]vmops.SnapshotRow, error) {
	return []vmops.SnapshotRow{{Name: "snap1", Current: true}}, f.err
}
func (f *fakeVMRunner) SnapshotCreate(context.Context, string, string, string) (vmops.SnapshotCreateResult, error) {
	return vmops.SnapshotCreateResult{Name: "snap1", VerifiedFormats: []string{"vda=qcow2"}}, f.err
}
func (f *fakeVMRunner) SnapshotRevert(context.Context, string, string) (vmops.MutateResult, error) {
	return vmops.MutateResult{Name: "vm1", Action: "snapshot-revert"}, f.err
}
func (f *fakeVMRunner) SnapshotDelete(context.Context, string, string) (vmops.MutateResult, error) {
	return vmops.MutateResult{Name: "vm1", Action: "snapshot-delete"}, f.err
}

var _ VMRunner = (*fakeVMRunner)(nil)

// Kapı VARSAYILAN KAPALI: Config.VM nil → her vm op'u tipli unsupported.
func TestVMFamilyDisabledByDefault(t *testing.T) {
	srv := NewServer(Config{AllowedUIDs: []uint32{uint32(os.Getuid())}})
	gate := []struct {
		op  string
		req string
	}{
		{OpVMPreflight, `{}`},
		{OpVMStatus, `{}`},
		{OpVMList, `{}`},
		{OpVMInfo, `{"name":"vm1"}`},
		{OpVMCreate, `{"name":"vm1","vcpu":2,"mem_mib":512,"disk_gib":20,"pool":"default","bridge":"br0"}`},
		{OpVMClone, `{"source":"src","name":"clone1"}`},
		{OpVMStart, `{"name":"vm1"}`},
		{OpVMStop, `{"name":"vm1"}`},
		{OpVMReboot, `{"name":"vm1"}`},
		{OpVMDelete, `{"name":"vm1"}`},
		{OpVMDiskList, `{"name":"vm1"}`},
		{OpVMDiskAdd, `{"name":"vm1","pool":"default","size_gib":10}`},
		{OpVMSnapshotList, `{"name":"vm1"}`},
		{OpVMSnapshotCreate, `{"name":"vm1","snapshot":"snap1"}`},
		{OpVMSnapshotRevert, `{"name":"vm1","snapshot":"snap1"}`},
		{OpVMSnapshotDelete, `{"name":"vm1","snapshot":"snap1"}`},
	}
	for _, g := range gate {
		req := &Request{V: ProtocolV1, Operation: g.op, RequestID: "t", Params: []byte(g.req)}
		call, verr := validateRequest(req)
		if verr != nil {
			t.Fatalf("%s şema geçmeli (kapı yürütme katmanında): %v", g.op, verr)
		}
		res := srv.execute(req, call)
		if res.Status != StatusRejected || res.ErrorCategory != CatUnsupportedOp {
			t.Fatalf("%s kapalıyken unsupported beklenir: %+v", g.op, res)
		}
	}
}

// Strict şema negatifleri (validateRequest katmanı): traversal/enjeksiyon
// adları ve limit ihlalleri runner'a ASLA ulaşmaz (kriter 3'ün broker
// savunma katmanı).
func TestVMFamilyValidation(t *testing.T) {
	negatives := []struct {
		op  string
		req string
	}{
		{OpVMInfo, `{"name":"../../etc"}`},
		{OpVMInfo, `{"name":"VM1"}`},
		{OpVMInfo, `{"name":"vm1;rm -rf"}`},
		{OpVMInfo, `{"name":"vm1","x":1}`},
		{OpVMCreate, `{"name":"vm1","vcpu":0,"mem_mib":512,"disk_gib":20,"pool":"default","bridge":"br0"}`},
		{OpVMCreate, `{"name":"vm1","vcpu":65,"mem_mib":512,"disk_gib":20,"pool":"default","bridge":"br0"}`},
		{OpVMCreate, `{"name":"vm1","vcpu":2,"mem_mib":127,"disk_gib":20,"pool":"default","bridge":"br0"}`},
		{OpVMCreate, `{"name":"vm1","vcpu":2,"mem_mib":512,"disk_gib":4097,"pool":"default","bridge":"br0"}`},
		{OpVMCreate, `{"name":"vm1","vcpu":2,"mem_mib":512,"disk_gib":20,"pool":"/etc/evil","bridge":"br0"}`},
		{OpVMCreate, `{"name":"vm1","vcpu":2,"mem_mib":512,"disk_gib":20,"pool":"default","bridge":"a b"}`},
		{OpVMCreate, `{"name":"vm1","vcpu":2,"mem_mib":512,"disk_gib":20,"pool":"default"}`},
		{OpVMClone, `{"source":"src","name":"src"}`},
		{OpVMClone, `{"source":"../src","name":"clone1"}`},
		{OpVMStop, `{"name":"vm1","force":"yes"}`},
		{OpVMDiskAdd, `{"name":"vm1","pool":"default","size_gib":0}`},
		{OpVMDiskAdd, `{"name":"vm1","pool":"a/b","size_gib":10}`},
		{OpVMSnapshotCreate, `{"name":"vm1","snapshot":"snap 1"}`},
		{OpVMSnapshotCreate, `{"name":"vm1","snapshot":"snap1","description":"` + string(make([]byte, 300)) + `"}`},
		{OpVMSnapshotRevert, `{"name":"vm1"}`},
		{OpVMSnapshotDelete, `{"name":"vm1","snapshot":"<script>"}`},
	}
	for i, n := range negatives {
		req := &Request{V: ProtocolV1, Operation: n.op, RequestID: "neg", Params: []byte(n.req)}
		if _, err := validateRequest(req); err == nil {
			t.Errorf("neg[%d] %s %s reddedilmeli", i, n.op, n.req)
		}
	}
	positives := []struct {
		op  string
		req string
	}{
		{OpVMPreflight, `{}`},
		{OpVMPreflight, ``},
		{OpVMStatus, `{}`},
		{OpVMList, `{"limit":10}`},
		{OpVMInfo, `{"name":"vm1"}`},
		{OpVMCreate, `{"name":"vm1","vcpu":64,"mem_mib":262144,"disk_gib":4096,"pool":"pool-1","bridge":"virbr0"}`},
		{OpVMCreate, `{"name":"vm1","vcpu":2,"mem_mib":512,"disk_gib":20,"pool":"default","bridge":"br0"}`},
		{OpVMClone, `{"source":"src","name":"clone1"}`},
		{OpVMStart, `{"name":"vm1"}`},
		{OpVMStop, `{"name":"vm1","force":true}`},
		{OpVMStop, `{"name":"vm1"}`},
		{OpVMReboot, `{"name":"vm1"}`},
		{OpVMDelete, `{"name":"vm1"}`},
		{OpVMDiskList, `{"name":"vm1"}`},
		{OpVMDiskAdd, `{"name":"vm1","pool":"default","size_gib":10}`},
		{OpVMSnapshotList, `{"name":"vm1"}`},
		{OpVMSnapshotCreate, `{"name":"vm1","snapshot":"snap1","description":"temiz kurulum"}`},
		{OpVMSnapshotRevert, `{"name":"vm1","snapshot":"snap1"}`},
		{OpVMSnapshotDelete, `{"name":"vm1","snapshot":"snap1"}`},
	}
	for i, p := range positives {
		req := &Request{V: ProtocolV1, Operation: p.op, RequestID: "pos", Params: []byte(p.req)}
		if _, err := validateRequest(req); err != nil {
			t.Errorf("pos[%d] %s %s reddedildi: %v", i, p.op, p.req, err)
		}
	}
}

// Runner bağlıyken yürütme (happy) + runner hata kategori eşlemesi.
func TestVMFamilyExecuteAndErrorMapping(t *testing.T) {
	srv := NewServer(Config{
		AllowedUIDs: []uint32{uint32(os.Getuid())},
		VM:          &fakeVMRunner{},
	})
	happy := []struct {
		op   string
		req  string
		want string
	}{
		{OpVMPreflight, `{}`, StatusSucceeded},
		{OpVMStatus, `{}`, StatusSucceeded},
		{OpVMList, `{}`, StatusSucceeded},
		{OpVMInfo, `{"name":"vm1"}`, StatusSucceeded},
		{OpVMCreate, `{"name":"vm1","vcpu":2,"mem_mib":512,"disk_gib":20,"pool":"default","bridge":"br0"}`, StatusSucceeded},
		{OpVMClone, `{"source":"src","name":"clone1"}`, StatusSucceeded},
		{OpVMStart, `{"name":"vm1"}`, StatusSucceeded},
		{OpVMStop, `{"name":"vm1","force":true}`, StatusSucceeded},
		{OpVMReboot, `{"name":"vm1"}`, StatusSucceeded},
		{OpVMDelete, `{"name":"vm1"}`, StatusSucceeded},
		{OpVMDiskList, `{"name":"vm1"}`, StatusSucceeded},
		{OpVMDiskAdd, `{"name":"vm1","pool":"default","size_gib":10}`, StatusSucceeded},
		{OpVMSnapshotList, `{"name":"vm1"}`, StatusSucceeded},
		{OpVMSnapshotCreate, `{"name":"vm1","snapshot":"snap1"}`, StatusSucceeded},
		{OpVMSnapshotRevert, `{"name":"vm1","snapshot":"snap1"}`, StatusSucceeded},
		{OpVMSnapshotDelete, `{"name":"vm1","snapshot":"snap1"}`, StatusSucceeded},
	}
	for i, h := range happy {
		req := &Request{V: ProtocolV1, Operation: h.op, RequestID: "h", Params: []byte(h.req)}
		call, verr := validateRequest(req)
		if verr != nil {
			t.Fatalf("pos[%d] %s şema reddedildi: %v", i, h.op, verr)
		}
		res := srv.execute(req, call)
		if res.Status != h.want {
			t.Fatalf("pos[%d] %s status = %s (%s)", i, h.op, res.Status, res.Output)
		}
	}

	createReq := &Request{V: ProtocolV1, Operation: OpVMCreate, RequestID: "f",
		Params: []byte(`{"name":"vm1","vcpu":2,"mem_mib":512,"disk_gib":20,"pool":"default","bridge":"br0"}`)}
	call, _ := validateRequest(createReq)

	// Durum redleri: istek geçerli, durum uygunsuz → executor_error.
	for _, terr := range []error{vmops.ErrDiskFormat, vmops.ErrForeignVolume, vmops.ErrConflict} {
		srvFail := NewServer(Config{AllowedUIDs: []uint32{uint32(os.Getuid())},
			VM: &fakeVMRunner{err: terr}})
		res := srvFail.execute(createReq, call)
		if res.Status != StatusFailed || res.ErrorCategory != CatExecutorFailed {
			t.Fatalf("%v kategorisi = %s/%s, want failed/executor_error",
				terr, res.Status, res.ErrorCategory)
		}
	}

	// İstem kaynaklı redler → invalid_params.
	srvBad := NewServer(Config{AllowedUIDs: []uint32{uint32(os.Getuid())},
		VM: &fakeVMRunner{err: vmops.ErrInvalidName}})
	res := srvBad.execute(createReq, call)
	if res.Status != StatusRejected || res.ErrorCategory != CatInvalidParams {
		t.Fatalf("ErrInvalidName kategorisi = %s/%s, want rejected/invalid_params",
			res.Status, res.ErrorCategory)
	}

	// Ortam yok → unsupported_operation.
	srvUn := NewServer(Config{AllowedUIDs: []uint32{uint32(os.Getuid())},
		VM: &fakeVMRunner{err: vmops.ErrUnavailable}})
	res = srvUn.execute(createReq, call)
	if res.Status != StatusRejected || res.ErrorCategory != CatUnsupportedOp {
		t.Fatalf("ErrUnavailable kategorisi = %s/%s, want rejected/unsupported_operation",
			res.Status, res.ErrorCategory)
	}
}
