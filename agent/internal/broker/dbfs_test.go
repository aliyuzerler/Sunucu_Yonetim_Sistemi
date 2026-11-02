//go:build unix

package broker

// Faz 10: db/fs aileleri broker testleri — opt-in kapıları (VARSAYILAN
// KAPALI — ADR-0014), strict şema doğrulaması ve sahte runner'larla
// yürütme yolları. Hermetik: gerçek DB/dosya yok.

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/dbops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fsops"
)

type fakeDBRunner struct {
	err error
}

func (f *fakeDBRunner) InstancesList(context.Context) ([]dbops.Instance, error) {
	return []dbops.Instance{{Kind: dbops.KindPostgres, Socket: "/run/postgresql"}}, f.err
}
func (f *fakeDBRunner) DatabasesList(context.Context, string, int) ([]dbops.DatabaseInfo, error) {
	return []dbops.DatabaseInfo{{Name: "app"}}, f.err
}
func (f *fakeDBRunner) DatabaseCreate(context.Context, string, string, string) (map[string]any, error) {
	return map[string]any{"created": true}, f.err
}
func (f *fakeDBRunner) DatabaseDelete(context.Context, string, string) (map[string]any, error) {
	return map[string]any{"deleted": true}, f.err
}
func (f *fakeDBRunner) UsersList(context.Context, string, int) ([]dbops.UserInfo, error) {
	return []dbops.UserInfo{{Name: "app_ro"}}, f.err
}
func (f *fakeDBRunner) UserCreate(context.Context, string, string, string) (dbops.UserCreateResult, error) {
	return dbops.UserCreateResult{Kind: dbops.KindMySQL,
		Credentials: &dbops.UserCredentials{Password: "pw", Fingerprint: "fp"}}, f.err
}
func (f *fakeDBRunner) UserDelete(context.Context, string, string, string) (map[string]any, error) {
	return map[string]any{"deleted": true}, f.err
}
func (f *fakeDBRunner) GrantSet(context.Context, string, string, string, string, string) (map[string]any, error) {
	return map[string]any{"set": true}, f.err
}
func (f *fakeDBRunner) Backup(context.Context, string, string) (map[string]any, error) {
	return map[string]any{"file": "x.sql", "bytes": int64(10), "sha256": "ab"}, f.err
}
func (f *fakeDBRunner) BackupsList(context.Context, string, int) ([]dbops.BackupInfo, error) {
	return nil, f.err
}
func (f *fakeDBRunner) Restore(context.Context, string, string, string) (map[string]any, error) {
	return map[string]any{"restored": true}, f.err
}

type fakeFsRunner struct{ err error }

func (f *fakeFsRunner) List(context.Context, string, string, int) (fsops.ListResult, error) {
	return fsops.ListResult{Root: "blog"}, f.err
}
func (f *fakeFsRunner) Download(context.Context, string, string) (fsops.DownloadResult, error) {
	return fsops.DownloadResult{Content: "YQ=="}, f.err
}
func (f *fakeFsRunner) Upload(context.Context, string, string, string, uint32, bool) (fsops.UploadResult, error) {
	return fsops.UploadResult{Created: true}, f.err
}
func (f *fakeFsRunner) Chmod(context.Context, string, string, uint32) (fsops.ChmodResult, error) {
	return fsops.ChmodResult{NewMode: "0600"}, f.err
}
func (f *fakeFsRunner) Chown(context.Context, string, string) (fsops.ChownResult, error) {
	return fsops.ChownResult{User: "sc-web-blog"}, f.err
}

// Kapılar VARSAYILAN KAPALI: Config.DB/Config.Fs nil → tipli unsupported.
func TestDBFSFamiliesDisabledByDefault(t *testing.T) {
	srv := NewServer(Config{AllowedUIDs: []uint32{uint32(os.Getuid())}})
	gate := []struct {
		op  string
		req string
	}{
		{OpDBInstanceList, `{}`},
		{OpDBBackup, `{"kind":"mysql","database":"app"}`},
		{OpDBRestore, `{"kind":"mysql","database":"app","backup":"app-20261102T090507Z.sql"}`},
		{OpDBUserCreate, `{"kind":"mysql","name":"app_ro"}`},
		{OpFSList, `{"root":"blog"}`},
		{OpFSDownload, `{"root":"blog","path":"a.txt"}`},
		{OpFSUpload, `{"root":"blog","path":"a.txt","content_b64":"YQ=="}`},
		{OpFSChmod, `{"root":"blog","path":"a.txt","mode":"0600"}`},
		{OpFSChown, `{"root":"blog","path":"a.txt"}`},
	}
	for _, g := range gate {
		req := &Request{V: ProtocolV1, Operation: g.op, RequestID: "t", Params: []byte(g.req)}
		res := srv.execute(req, mustValidate(t, req))
		if res.Status != StatusRejected || res.ErrorCategory != CatUnsupportedOp {
			t.Fatalf("%s kapalıyken unsupported beklenir: %+v", g.op, res)
		}
	}
}

// Strict şema negatifleri (broker'a varan hiçbir kötü payload runner'a gitmez).
func TestDBFSFamilyValidation(t *testing.T) {
	negatives := []struct {
		op  string
		req string
	}{
		{OpDBDatabaseList, `{"kind":"oracle"}`},
		{OpDBDatabaseList, `{"kind":"mysql","limit":99999}`},
		{OpDBDatabaseCreate, `{"kind":"mysql","name":"App"}`},
		{OpDBDatabaseCreate, `{"kind":"mysql","name":"x;drop"}`},
		{OpDBDatabaseCreate, `{"kind":"mysql","name":"srvctl_panel"}`},
		{OpDBDatabaseDelete, `{"kind":"mysql","database":"mysql"}`},
		{OpDBDatabaseDelete, `{"kind":"postgres","database":"server_control"}`},
		{OpDBUserCreate, `{"kind":"mysql","name":"root","host":"localhost"}`},
		{OpDBUserCreate, `{"kind":"mysql","name":"u1","host":"h;drop"}`},
		{OpDBUserCreate, `{"kind":"postgres","name":"u1","host":"localhost"}`},
		{OpDBUserDelete, `{"kind":"mysql","name":"u1","host":"a b"}`},
		{OpDBGrantSet, `{"kind":"mysql","user":"u1","database":"d","level":"superuser"}`},
		{OpDBGrantSet, `{"kind":"postgres","user":"root","database":"d","level":"all"}`},
		{OpDBBackup, `{"kind":"mysql","database":"nope;"}`},
		{OpDBRestore, `{"kind":"mysql","database":"app","backup":"../../etc/passwd"}`},
		{OpDBRestore, `{"kind":"mysql","database":"app","backup":"a/b.sql"}`},
		{OpDBInstanceList, `{"x":1}`},
		{OpFSList, `{"root":"../etc"}`},
		{OpFSList, `{"root":"blog","path":"/abs"}`},
		{OpFSList, `{"root":"blog","path":"a/../b"}`},
		{OpFSDownload, `{"root":"blog","path":""}`},
		{OpFSDownload, `{"root":"blog","path":"a.link","mode":"x"}`},
		{OpFSUpload, `{"root":"blog","path":"f.txt","content_b64":"YQ==","mode":"9999"}`},
		{OpFSUpload, `{"root":"blog","path":"f.txt","content_b64":"YQ==","unknown":1}`},
		{OpFSChmod, `{"root":"blog","path":"f.txt","mode":"1777"}`},
		{OpFSChmod, `{"root":"blog","path":"f.txt"}`},
		{OpFSChown, `{"root":"blog"}`},
	}
	for i, n := range negatives {
		req := &Request{V: ProtocolV1, Operation: n.op, RequestID: "neg", Params: []byte(n.req)}
		if _, err := validateRequest(req); err == nil {
			t.Errorf("neg[%d] %s %s reddedilmeli", i, n.op, n.req)
		}
	}
	// Pozitifler.
	positives := []struct {
		op  string
		req string
	}{
		{OpDBInstanceList, `{}`},
		{OpDBDatabaseList, `{"kind":"mysql"}`},
		{OpDBDatabaseList, `{"kind":"postgres","limit":50}`},
		{OpDBDatabaseCreate, `{"kind":"mysql","name":"app","owner":"app_u"}`},
		{OpDBDatabaseDelete, `{"kind":"postgres","database":"app"}`},
		{OpDBUserList, `{"kind":"mysql"}`},
		{OpDBUserCreate, `{"kind":"mysql","name":"app_ro","host":"10.0.0.5"}`},
		{OpDBUserCreate, `{"kind":"postgres","name":"app_ro"}`},
		{OpDBUserDelete, `{"kind":"mysql","name":"app_ro"}`},
		{OpDBGrantSet, `{"kind":"mysql","user":"u1","database":"d","level":"readonly"}`},
		{OpDBGrantSet, `{"kind":"mysql","user":"u1","host":"%","database":"d","level":"all"}`},
		{OpDBBackup, `{"kind":"postgres","database":"app"}`},
		{OpDBBackupList, `{"kind":"mysql"}`},
		{OpDBRestore, `{"kind":"mysql","database":"app","backup":"app-20261102T090507Z.sql"}`},
		{OpFSList, `{"root":"blog"}`},
		{OpFSList, `{"root":"blog","path":"sub","limit":10}`},
		{OpFSDownload, `{"root":"blog","path":"a.txt"}`},
		{OpFSUpload, `{"root":"blog","path":"a.txt","content_b64":"YQ==","mode":"0644","overwrite":true}`},
		{OpFSChmod, `{"root":"blog","path":"a.txt","mode":"0600"}`},
		{OpFSChown, `{"root":"blog","path":"a.txt"}`},
	}
	for i, p := range positives {
		req := &Request{V: ProtocolV1, Operation: p.op, RequestID: "pos", Params: []byte(p.req)}
		if _, err := validateRequest(req); err != nil {
			t.Errorf("pos[%d] %s %s reddedildi: %v", i, p.op, p.req, err)
		}
	}
}

// Runner bağlıyken yürütme yolları (happy) + runner hatası kategori eşlemesi.
func TestDBFSFamilyExecute(t *testing.T) {
	srv := NewServer(Config{
		AllowedUIDs: []uint32{uint32(os.Getuid())},
		DB:          &fakeDBRunner{},
		Fs:          &fakeFsRunner{},
	})
	happy := []struct {
		op   string
		req  string
		want string
	}{
		{OpDBInstanceList, `{}`, StatusSucceeded},
		{OpDBDatabaseList, `{"kind":"mysql"}`, StatusSucceeded},
		{OpDBUserCreate, `{"kind":"mysql","name":"app_ro"}`, StatusSucceeded},
		{OpFSList, `{"root":"blog"}`, StatusSucceeded},
		{OpFSUpload, `{"root":"blog","path":"a.txt","content_b64":"YQ=="}`, StatusSucceeded},
		{OpFSChmod, `{"root":"blog","path":"a.txt","mode":"0600"}`, StatusSucceeded},
	}
	for i, h := range happy {
		req := &Request{V: ProtocolV1, Operation: h.op, RequestID: "h", Params: []byte(h.req)}
		res := srv.execute(req, mustValidate(t, req))
		if res.Status != h.want {
			t.Errorf("happy[%d] %s: %s (output: %s)", i, h.op, res.Status, res.Output)
		}
	}

	// Runner hatası → failed/executor_error.
	srvErr := NewServer(Config{
		AllowedUIDs: []uint32{uint32(os.Getuid())},
		DB:          &fakeDBRunner{err: errors.New("dump failed")},
		Fs:          &fakeFsRunner{err: errors.New("io error")},
	})
	reqErr := &Request{V: ProtocolV1, Operation: OpDBBackup, RequestID: "e", Params: []byte(`{"kind":"mysql","database":"app"}`)}
	res := srvErr.execute(reqErr, mustValidate(t, reqErr))
	if res.Status != StatusFailed || res.ErrorCategory != CatExecutorFailed {
		t.Fatalf("runner hatası executor_error olmalı: %+v", res)
	}
}
