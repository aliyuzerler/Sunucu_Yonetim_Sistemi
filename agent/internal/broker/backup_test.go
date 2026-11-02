//go:build unix

// Faz 14 broker katmanı testleri (vm/db/mail ailelerindeki üç-katman kanıt
// disiplininin backup karşılığı — ADR-0018):
//   - Kapı VARSAYILAN KAPALI: Config.Backup nil → tipli unsupported.
//   - Strict şema negatifleri: traversal yolları, bozuk UUID/snapshot,
//     restore payload'ında hedef alanı runner'a ASLA ulaşmaz.
//   - Yürütme: runner bağlıyken happy yolu + hata kategorisi eşlemesi
//     (ErrInvalidPaths → invalid_params; ErrSourceNotConfigured →
//     executor_error; ErrKeySeparation → invalid_params).

package broker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/backupops"
)

type fakeBackupRunner struct {
	err      error
	initHit  bool
	runHit   bool
	restHit  bool
	lastRun  backupops.RunPayload
	lastRest backupops.RestorePayload
}

func (f *fakeBackupRunner) RepoInit(context.Context) (map[string]any, error) {
	f.initHit = true
	return map[string]any{"initialized": true, "already": false}, f.err
}
func (f *fakeBackupRunner) Backup(ctx context.Context, p backupops.RunPayload) (backupops.BackupResult, error) {
	f.runHit = true
	f.lastRun = p
	return backupops.BackupResult{SnapshotID: "abcd1234ef567890", SizeBytes: 100, Files: 3,
		VerifyState: backupops.VerifyVerified, VerifyDetail: "check ok"}, f.err
}
func (f *fakeBackupRunner) SnapshotList(context.Context, backupops.SnapshotListPayload) ([]backupops.SnapshotRow, error) {
	return []backupops.SnapshotRow{{ShortID: "abcd1234"}}, f.err
}
func (f *fakeBackupRunner) Verify(context.Context, backupops.VerifyPayload) (backupops.VerifyResult, error) {
	return backupops.VerifyResult{VerifyState: backupops.VerifyVerified, VerifyDetail: "ok"}, f.err
}
func (f *fakeBackupRunner) Restore(ctx context.Context, p backupops.RestorePayload) (backupops.RestoreResult, error) {
	f.restHit = true
	f.lastRest = p
	return backupops.RestoreResult{BackupID: p.BackupID, Target: "/var/backups/server-control/restore/rest-abc123",
		Files: 7, Bytes: 4096, Detail: "ok"}, f.err
}

var _ BackupRunner = (*fakeBackupRunner)(nil)

// Kapı VARSAYILAN KAPALI: Config.Backup nil → her backup op'u tipli unsupported.
func TestBackupFamilyDisabledByDefault(t *testing.T) {
	srv := NewServer(Config{AllowedUIDs: []uint32{uint32(os.Getuid())}})
	gate := []struct {
		op  string
		req string
	}{
		{OpBackupRepoInit, `{}`},
		{OpBackupRun, `{}`},
		{OpBackupRun, `{"paths":["/etc/server-control"]}`},
		{OpBackupSnapshotList, `{}`},
		{OpBackupVerify, `{"backup_id":"0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111"}`},
		{OpBackupRestore, `{"backup_id":"0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111","snapshot":"abcd1234"}`},
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

// Strict şema negatifleri (validateRequest katmanı): traversal yolları,
// bozuk kimlikler ve hedef-yol enjeksiyonu runner'a ASLA ulaşmaz.
func TestBackupFamilyValidation(t *testing.T) {
	uid := "0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111"
	negatives := []struct {
		op  string
		req string
	}{
		{OpBackupRepoInit, `{"x":1}`},
		{OpBackupRun, `{"paths":["goreceli/yol"]}`},
		{OpBackupRun, `{"paths":["/etc/../shadow"]}`},
		{OpBackupRun, `{"paths":["/etc/server-control","/a","/b","/c","/d","/e","/f","/g","/h"]}`},
		{OpBackupRun, `{"paths":[123]}`},
		{OpBackupSnapshotList, `{"limit":101}`},
		{OpBackupSnapshotList, `{"limit":-1}`},
		{OpBackupVerify, `{"backup_id":"zort"}`},
		{OpBackupVerify, `{}`},
		{OpBackupRestore, `{"backup_id":"zort","snapshot":"abcd1234"}`},
		{OpBackupRestore, `{"backup_id":"` + uid + `","snapshot":"ZZZZ"}`},
		{OpBackupRestore, `{"backup_id":"` + uid + `","snapshot":"abcd1234","target":"/etc"}`},
		{OpBackupRestore, `{"backup_id":"` + uid + `"}`},
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
		{OpBackupRepoInit, ``},
		{OpBackupRun, `{}`},
		{OpBackupRun, `{"paths":["/etc/server-control","/var/vmail/example.com"]}`},
		{OpBackupSnapshotList, `{"limit":10}`},
		{OpBackupVerify, `{"backup_id":"` + uid + `"}`},
		{OpBackupRestore, `{"backup_id":"` + uid + `","snapshot":"abcd1234"}`},
	}
	for i, p := range positives {
		req := &Request{V: ProtocolV1, Operation: p.op, RequestID: "pos", Params: []byte(p.req)}
		if _, err := validateRequest(req); err != nil {
			t.Errorf("pos[%d] %s %s reddedilmemeli: %v", i, p.op, p.req, err)
		}
	}
}

// Yürütme: runner bağlıyken happy yolu + hata kategorisi eşlemesi.
func TestBackupFamilyExecution(t *testing.T) {
	uid := "0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111"
	fake := &fakeBackupRunner{}
	srv := NewServer(Config{AllowedUIDs: []uint32{uint32(os.Getuid())}, Backup: fake})

	run := func(op, params string) *Response {
		req := &Request{V: ProtocolV1, Operation: op, RequestID: "exec", Params: []byte(params)}
		call, verr := validateRequest(req)
		if verr != nil {
			t.Fatalf("%s şema: %v", op, verr)
		}
		return srv.execute(req, call)
	}

	if res := run(OpBackupRepoInit, `{}`); res.Status != StatusSucceeded || !fake.initHit {
		t.Fatalf("repo.init başarılı olmalı: %+v", res)
	}
	if res := run(OpBackupRun, `{"paths":["/etc/server-control"]}`); res.Status != StatusSucceeded {
		t.Fatalf("backup.run başarılı olmalı: %+v", res)
	} else {
		var out struct {
			SnapshotID  string `json:"snapshot_id"`
			VerifyState string `json:"verify_state"`
		}
		_ = json.Unmarshal(res.Result, &out)
		if out.SnapshotID != "abcd1234ef567890" || out.VerifyState != "verified" {
			t.Fatalf("backup sonucu: %s", res.Result)
		}
		if len(fake.lastRun.Paths) != 1 || fake.lastRun.Paths[0] != "/etc/server-control" {
			t.Fatalf("yollar runner'a taşınmalı: %+v", fake.lastRun.Paths)
		}
	}
	if res := run(OpBackupRestore, `{"backup_id":"`+uid+`","snapshot":"abcd1234"}`); res.Status != StatusSucceeded || !fake.restHit {
		t.Fatalf("restore başarılı olmalı: %+v", res)
	} else if !strings.Contains(string(res.Result), "rest-") {
		t.Fatalf("izole hedef sonuçta olmalı: %s", res.Result)
	}

	// Hata kategorisi eşlemesi.
	fake.err = backupops.ErrSourceNotConfigured
	if res := run(OpBackupRun, `{"paths":["/etc/server-control"]}`); res.Status != StatusFailed || res.ErrorCategory != CatExecutorFailed {
		t.Fatalf("ErrSourceNotConfigured → failed/executor_error: %+v", res)
	}
	fake.err = backupops.ErrInvalidPaths
	if res := run(OpBackupRun, `{}`); res.Status != StatusRejected || res.ErrorCategory != CatInvalidParams {
		t.Fatalf("ErrInvalidPaths → rejected/invalid_params: %+v", res)
	}
	fake.err = backupops.ErrKeySeparation
	if res := run(OpBackupRun, `{}`); res.Status != StatusRejected || res.ErrorCategory != CatInvalidParams {
		t.Fatalf("ErrKeySeparation → rejected/invalid_params: %+v", res)
	}
}
