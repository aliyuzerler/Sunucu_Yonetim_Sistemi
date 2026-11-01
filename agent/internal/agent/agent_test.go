//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	agentv1 "github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/gen/proto/servercontrol/agent/v1"
)

// agentDefaultDockerForTest is the probe path used by the default registry
// tests (unix); the agent never dials this socket itself.
const agentDefaultDockerForTest = "/var/run/docker.sock"

func TestStoreKeyPermissionsAndPersistence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agentdata")
	store := &Store{Dir: dir}

	if store.HasIdentity() {
		t.Fatal("temiz dizinde kimlik olmamalı")
	}
	key, err := store.GenerateKey()
	if err != nil {
		t.Fatalf("anahtar üretilemedi: %v", err)
	}
	info, err := os.Stat(store.KeyPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("özel anahtar izni 0600 olmalı: %v", info.Mode().Perm())
	}
	reloaded, err := store.LoadKey()
	if err != nil {
		t.Fatalf("anahtar okunamadı: %v", err)
	}
	if key.PublicKey.X.Cmp(reloaded.PublicKey.X) != 0 {
		t.Fatal("anahtar kalıcı olmalı")
	}

	csr, err := BuildCSR(key)
	if err != nil {
		t.Fatalf("CSR üretilemedi: %v", err)
	}
	if len(csr) < 100 {
		t.Fatal("CSR PEM bekleniyordu")
	}

	if err := store.SaveCert("CERT-PEM", "CA-PEM"); err != nil {
		t.Fatalf("sertifika kaydı: %v", err)
	}
	if !store.HasIdentity() {
		t.Fatal("kimlik tam olmalı")
	}
	parts, err := store.LoadTLSConfigParts()
	if err != nil {
		t.Fatal(err)
	}
	if string(parts.certPEM) != "CERT-PEM" || string(parts.caPEM) != "CA-PEM" {
		t.Fatal("kayıtlı malzeme okunmalı")
	}
}

func TestEchoExecutor(t *testing.T) {
	exec := EchoExecutor{}
	info := exec.Info()
	if info.GetCapabilityId() != "agent.echo" || !info.GetAvailable() {
		t.Fatalf("capability hatalı: %+v", info)
	}

	body, output, err := exec.Execute(context.Background(), []byte(`{"message":"merhaba"}`))
	if err != nil {
		t.Fatalf("echo: %v", err)
	}
	if output != "echo tamamlandı" {
		t.Fatalf("output: %q", output)
	}
	var res map[string]any
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if res["echo"] != "merhaba" {
		t.Fatalf("echo alanı: %v", res)
	}

	// Bilinmeyen alan reddedilir.
	if _, _, err := exec.Execute(context.Background(), []byte(`{"message":"x","extra":1}`)); err == nil {
		t.Fatal("bilinmeyen alan reddedilmeli")
	}
	// Çok uzun mesaj reddedilir.
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	payload, _ := json.Marshal(map[string]string{"message": string(long)})
	if _, _, err := exec.Execute(context.Background(), payload); err == nil {
		t.Fatal("uzun mesaj reddedilmeli")
	}
	// Bozuk JSON reddedilir.
	if _, _, err := exec.Execute(context.Background(), []byte("not-json")); err == nil {
		t.Fatal("bozuk JSON reddedilmeli")
	}
}

func TestRegistryDefaultDeny(t *testing.T) {
	reg, err := DefaultRegistry("", agentDefaultDockerForTest)
	if err != nil {
		t.Fatal(err)
	}
	// Faz 04+06+07: echo + 5 systemd + pkg.list + 3 firewall + 13 docker + 2 compose = 25.
	if len(reg.Capabilities()) != 25 {
		t.Fatalf("25 capability bekleniyordu: %d", len(reg.Capabilities()))
	}
	for _, name := range []string{"agent.echo", "linux.systemd.service.list", "linux.systemd.service.status",
		"linux.systemd.service.start", "linux.systemd.service.stop", "linux.systemd.service.restart",
		"linux.pkg.list", "linux.firewall.status", "linux.firewall.rules.list", "linux.firewall.rule.preview",
		"docker.container.list", "docker.container.inspect", "docker.container.logs",
		"docker.container.start", "docker.container.stop", "docker.container.restart", "docker.container.remove",
		"docker.image.list", "docker.image.remove", "docker.network.list", "docker.network.remove",
		"docker.volume.list", "docker.volume.remove", "docker.compose.validate", "docker.compose.deploy"} {
		if _, ok := reg.Lookup(name); !ok {
			t.Fatalf("registry %s içermeli", name)
		}
	}

	// Bilinmeyen operasyon REJECTED döner, asla yürütülmez.
	res := reg.Execute(context.Background(), "node-1", &agentv1.OperationRequest{
		TaskId: "t1", Operation: "shell.exec", OperationVersion: "1.0.0",
		Payload: []byte(`{}`),
	})
	if res.GetStatus() != agentv1.OperationResult_STATUS_REJECTED || res.GetErrorCategory() != "unsupported_operation" {
		t.Fatalf("bilinmeyen operasyon reddedilmeli: %+v", res)
	}

	// Fazla payload reddedilir.
	res = reg.Execute(context.Background(), "node-1", &agentv1.OperationRequest{
		TaskId: "t2", Operation: "agent.echo", OperationVersion: "1.0.0",
		Payload: make([]byte, 200_000),
	})
	if res.GetStatus() != agentv1.OperationResult_STATUS_REJECTED || res.GetErrorCategory() != "payload_invalid" {
		t.Fatalf("büyük payload reddedilmeli: %+v", res)
	}

	// Geçersiz versiyon reddedilir.
	res = reg.Execute(context.Background(), "node-1", &agentv1.OperationRequest{
		TaskId: "t3", Operation: "agent.echo",
		Payload: []byte(`{"message":"x"}`),
	})
	if res.GetStatus() != agentv1.OperationResult_STATUS_REJECTED {
		t.Fatalf("versiyonsuz istek reddedilmeli: %+v", res)
	}

	// Başarılı akış SUCCEEDED döner.
	res = reg.Execute(context.Background(), "node-1", &agentv1.OperationRequest{
		TaskId: "t4", Operation: "agent.echo", OperationVersion: "1.0.0",
		Payload: []byte(`{"message":"selam"}`),
	})
	if res.GetStatus() != agentv1.OperationResult_STATUS_SUCCEEDED {
		t.Fatalf("başarılı echo bekleniyordu: %+v", res)
	}

	// Idempotency cache: aynı task_id saklanan sonucu döndürür.
	cached := reg.Execute(context.Background(), "node-1", &agentv1.OperationRequest{
		TaskId: "t4", Operation: "agent.echo", OperationVersion: "1.0.0",
		Payload: []byte(`{"message":"farklı"}`),
	})
	if string(cached.GetResult()) != string(res.GetResult()) {
		t.Fatal("idempotency cache saklanan sonucu döndürmeli")
	}
}

func TestRegistryRejectsDuplicateCapability(t *testing.T) {
	if _, err := NewRegistry(EchoExecutor{}, EchoExecutor{}); err == nil {
		t.Fatal("tekrarlı capability reddedilmeli")
	}
}
