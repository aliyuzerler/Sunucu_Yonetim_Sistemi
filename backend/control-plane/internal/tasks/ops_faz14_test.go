package tasks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeFaz14Backup records calls for executor tests.
type fakeFaz14Backup struct {
	dumpCalled   bool
	verifyCalled bool
	drillCalled  bool
	verifyID     string
	drillID      string
	drillActor   string
}

func (f *fakeFaz14Backup) DumpCP(ctx context.Context, pool *pgxpool.Pool) (map[string]any, error) {
	f.dumpCalled = true
	return map[string]any{"backup_id": "b1", "verify_state": "verified"}, nil
}
func (f *fakeFaz14Backup) VerifyCP(ctx context.Context, id string) (map[string]any, error) {
	f.verifyCalled = true
	f.verifyID = id
	return map[string]any{"backup_id": id, "verify_state": "verified"}, nil
}
func (f *fakeFaz14Backup) DrillCP(ctx context.Context, pool *pgxpool.Pool, id, actor string) (map[string]any, error) {
	f.drillCalled = true
	f.drillID = id
	f.drillActor = actor
	return map[string]any{"drill_id": "d1", "status": "success"}, nil
}

func TestFaz14RegistryOps(t *testing.T) {
	fake := &fakeFaz14Backup{}
	reg, err := BuildDefaultRegistry(&fakeTagSetter{}, Faz09Services{}, Faz14Services{Backup: fake}, Faz15Services{})
	if err != nil {
		t.Fatalf("kayıt defteri: %v", err)
	}
	// CP yedeği executor'lu kurulumda 111 operasyon: 105 + 3 backup.cp.* + 3 update.* (Faz 15).
	if len(reg.Names()) != 111 {
		t.Fatalf("111 operasyon bekleniyordu: %d", len(reg.Names()))
	}
	perm := map[string]string{
		"backup.repo.init":        "backup:control",
		"backup.run":              "backup:control",
		"backup.snapshot.list":    "backup:read",
		"backup.verify":           "backup:control",
		"backup.restore":          "backup:control",
		"backup.cp.run":           "backup:control",
		"backup.cp.verify":        "backup:control",
		"backup.cp.restore_drill": "backup:control",
	}
	for name, want := range perm {
		spec, ok := reg.Spec(name)
		if !ok {
			t.Fatalf("%s kayıtlı olmalı", name)
		}
		if spec.RequiredPerm != want {
			t.Fatalf("%s RequiredPerm=%q, beklenen %q", name, spec.RequiredPerm, want)
		}
	}
	// Node operasyonları executor'suz; CP operasyonları executor'lu olmalı.
	if spec, _ := reg.Spec("backup.run"); spec.Exec != nil {
		t.Fatal("backup.run node hedefli: control-plane executor OLMAMALI")
	}
	if spec, _ := reg.Spec("backup.cp.run"); spec.Exec == nil {
		t.Fatal("backup.cp.run executor'lu olmalı")
	}
}

func TestFaz14CPExecutor(t *testing.T) {
	fake := &fakeFaz14Backup{}
	ex := CPBackupExecutor{Svc: fake, Kind: "verify"}
	res, _, err := ex.Execute(context.Background(), &Task{
		Operation: "backup.cp.verify",
		Payload:   json.RawMessage(`{"backup_id":"0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111"}`),
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res["backup_id"] == "" || !fake.verifyCalled {
		t.Fatal("verify çağrısı bekleniyordu")
	}
	// Bozuk payload reddedilmeli.
	if _, _, err := ex.Execute(context.Background(), &Task{
		Operation: "backup.cp.verify",
		Payload:   json.RawMessage(`{"backup_id":"geçersiz"}`),
	}); err == nil {
		t.Fatal("geçersiz backup_id reddedilmeli")
	}
}

func TestFaz14Validators(t *testing.T) {
	uid := "0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111"
	cases := []struct {
		name    string
		op      string
		payload string
		wantErr bool
	}{
		{"init boş", "backup.repo.init", "", false},
		{"init bilinmeyen alan", "backup.repo.init", `{"x":1}`, true},
		{"run boş", "backup.run", "", false},
		{"run yollar", "backup.run", `{"paths":["/etc/server-control"]}`, false},
		{"run göreceli", "backup.run", `{"paths":["etc/x"]}`, true},
		{"run traversal", "backup.run", `{"paths":["/etc/../root"]}`, true},
		{"run çok yol", "backup.run", `{"paths":["/a","/b","/c","/d","/e","/f","/g","/h","/i"]}`, true},
		{"list limit", "backup.snapshot.list", `{"limit":50}`, false},
		{"list limit aşımı", "backup.snapshot.list", `{"limit":101}`, true},
		{"verify uuid", "backup.verify", `{"backup_id":"` + uid + `"}`, false},
		{"verify bozuk uuid", "backup.verify", `{"backup_id":"xyz"}`, true},
		{"restore snapshot", "backup.restore", `{"backup_id":"` + uid + `","snapshot":"abcd1234"}`, false},
		{"restore bozuk snapshot", "backup.restore", `{"backup_id":"` + uid + `","snapshot":"ZZZZ"}`, true},
		{"restore bilinmeyen alan", "backup.restore", `{"backup_id":"` + uid + `","snapshot":"abcd1234","target":"/etc"}`, true},
	}
	for _, tc := range cases {
		var err error
		switch tc.op {
		case "backup.repo.init":
			err = ValidateBackupRepoInit(json.RawMessage(tc.payload))
		case "backup.run":
			err = ValidateBackupRun(json.RawMessage(tc.payload))
		case "backup.snapshot.list":
			err = ValidateBackupSnapshotList(json.RawMessage(tc.payload))
		case "backup.verify":
			err = ValidateBackupVerify(json.RawMessage(tc.payload))
		case "backup.restore":
			err = ValidateBackupRestore(json.RawMessage(tc.payload))
		}
		if tc.wantErr && err == nil {
			t.Fatalf("%s: hata bekleniyordu", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("%s: beklenmeyen hata: %v", tc.name, err)
		}
	}
}

func TestFaz14RestorePayloadRejectsPathInjection(t *testing.T) {
	uid := "0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111"
	// restore payload'da HEDEF YOL ALANI YOKTUR — inject denemesi şema
	// hatası olarak reddedilir.
	err := ValidateBackupRestore(json.RawMessage(`{"backup_id":"` + uid + `","snapshot":"abcd1234","restore_to":"/etc/nginx"}`))
	if err == nil {
		t.Fatalf("bilinmeyen alan reddedilmeli")
	}
}
