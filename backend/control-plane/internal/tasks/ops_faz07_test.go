package tasks

import (
	"encoding/json"
	"strings"
	"testing"
)

// Faz 07 validator pozitif + negatif setleri (ops_faz07.go aynaları).
func TestValidateDockerList(t *testing.T) {
	if err := ValidateDockerList(nil); err != nil {
		t.Errorf("boş payload geçerli: %v", err)
	}
	if err := ValidateDockerList(json.RawMessage(`{"limit":100}`)); err != nil {
		t.Errorf("limit 100 geçerli: %v", err)
	}
	if err := ValidateDockerList(json.RawMessage(`{"limit":501}`)); err == nil {
		t.Error("limit 501 reddedilmeli")
	}
	if err := ValidateDockerList(json.RawMessage(`{"limit":-1}`)); err == nil {
		t.Error("negatif limit reddedilmeli")
	}
	if err := ValidateDockerList(json.RawMessage(`{"name":"x"}`)); err == nil {
		t.Error("bilinmeyen alan reddedilmeli")
	}
}

func TestValidateDockerRef(t *testing.T) {
	valid := []string{"abc123def4567890", "web-01", "cache_2.prod"}
	for _, v := range valid {
		if err := ValidateDockerRef(mustJSON(t, dockerRefPayload{Ref: v})); err != nil {
			t.Errorf("ref %q reddedildi: %v", v, err)
		}
	}
	invalid := []dockerRefPayload{
		{Ref: ""}, {Ref: "-flag"}, {Ref: "../etc/passwd"}, {Ref: "a/b"},
		{Ref: strings.Repeat("a", 200)}, {Ref: "boşluk lu"},
	}
	for _, p := range invalid {
		if err := ValidateDockerRef(mustJSON(t, p)); err == nil {
			t.Errorf("ref %q KABUL EDİLDİ", p.Ref)
		}
	}
	// Bilinmeyen alan reddi.
	if err := ValidateDockerRef(json.RawMessage(`{"ref":"ok01","force":true}`)); err == nil {
		t.Error("bilinmeyen alan reddedilmeli")
	}
}

func TestValidateDockerLogs(t *testing.T) {
	if err := ValidateDockerLogs(json.RawMessage(`{"ref":"web-01","tail":50}`)); err != nil {
		t.Errorf("tail 50 reddedildi: %v", err)
	}
	// tail yok → varsayılan 100 (sınır içinde).
	if err := ValidateDockerLogs(json.RawMessage(`{"ref":"web-01"}`)); err != nil {
		t.Errorf("tail varsayılan: %v", err)
	}
	for _, bad := range []string{
		`{"ref":"web-01","tail":501}`,
		`{"ref":"web-01","tail":-5}`,
		`{"ref":"","tail":10}`,
	} {
		if err := ValidateDockerLogs(json.RawMessage(bad)); err == nil {
			t.Errorf("%s kabul edildi", bad)
		}
	}
}

func TestValidateDockerAction(t *testing.T) {
	if err := ValidateDockerAction(json.RawMessage(`{"ref":"web-01","timeout":30}`)); err != nil {
		t.Errorf("timeout 30 reddedildi: %v", err)
	}
	// timeout yok → varsayılan 10.
	if err := ValidateDockerAction(json.RawMessage(`{"ref":"web-01"}`)); err != nil {
		t.Errorf("timeout varsayılan: %v", err)
	}
	for _, bad := range []string{
		`{"ref":"web-01","timeout":121}`,
		`{"ref":"web-01","timeout":-2}`,
		`{"ref":"","timeout":10}`,
		`{"ref":"web-01","ref2":"x"}`,
	} {
		if err := ValidateDockerAction(json.RawMessage(bad)); err == nil {
			t.Errorf("%s kabul edildi", bad)
		}
	}
}

func TestValidateDockerRemoveByKind(t *testing.T) {
	// image ailesi tag/digest/registry yolu kabul eder.
	if err := ValidateDockerRemoveByKind("image")(json.RawMessage(`{"ref":"nginx:1.25","force":true}`)); err != nil {
		t.Errorf("image ref reddedildi: %v", err)
	}
	if err := ValidateDockerRemoveByKind("image")(json.RawMessage(`{"ref":"ghcr.io/o/app@sha256:abc"}`)); err != nil {
		t.Errorf("digest reddedildi: %v", err)
	}
	// container/volume/network ailesi '/' ve ':' kabul etmez.
	if err := ValidateDockerRemoveByKind("container")(json.RawMessage(`{"ref":"nginx:1.25"}`)); err == nil {
		t.Error("container ref ':' kabul etmemeli")
	}
	// '..' traversal her ailede reddedilir.
	for _, kind := range []string{"container", "image", "volume", "network"} {
		if err := ValidateDockerRemoveByKind(kind)(json.RawMessage(`{"ref":"a..b"}`)); err == nil {
			t.Errorf("%s '..' kabul etti", kind)
		}
	}
	// Bilinmeyen alan.
	if err := ValidateDockerRemoveByKind("volume")(json.RawMessage(`{"ref":"v1","extra":true}`)); err == nil {
		t.Error("bilinmeyen alan reddedilmeli")
	}
}

func TestValidateDockerCompose(t *testing.T) {
	if err := ValidateDockerCompose(mustJSON(t, dockerComposePayload{Compose: "services:\n  web:\n    image: nginx\n"})); err != nil {
		t.Errorf("küçük compose reddedildi: %v", err)
	}
	if err := ValidateDockerCompose(mustJSON(t, dockerComposePayload{Compose: ""})); err == nil {
		t.Error("boş compose reddedilmeli")
	}
	big := strings.Repeat("a", MaxDockerComposeBytes+1)
	if err := ValidateDockerCompose(mustJSON(t, dockerComposePayload{Compose: big})); err == nil {
		t.Error("boyut sınırı çalışmalı")
	}
	// Bilinmeyen alan.
	if err := ValidateDockerCompose(json.RawMessage(`{"compose":"x","extra":1}`)); err == nil {
		t.Error("bilinmeyen alan reddedilmeli")
	}
}

func TestFaz07RegistryPerms(t *testing.T) {
	specs := buildFaz07Registry()
	if len(specs) != 15 {
		t.Fatalf("15 spec beklenir: %d", len(specs))
	}
	control := map[string]bool{
		"docker.container.start": true, "docker.container.stop": true, "docker.container.restart": true,
	}
	preview := map[string]bool{
		"docker.container.remove": true, "docker.image.remove": true,
		"docker.volume.remove": true, "docker.network.remove": true,
		"docker.compose.deploy": true,
	}
	for _, s := range specs {
		if control[s.Name] {
			if s.RequiredPerm != "docker:control" {
				t.Errorf("%s → %s, docker:control beklenir", s.Name, s.RequiredPerm)
			}
			continue
		}
		if s.RequiredPerm != "docker:read" {
			t.Errorf("%s → %s, docker:read beklenir", s.Name, s.RequiredPerm)
		}
	}
	// restart non_idempotent + retry-güvensiz; preview ailesi read_only + retry-safe.
	for _, s := range specs {
		if s.Name == "docker.container.restart" {
			if s.Class != ClassNonIdem || s.RetrySafe {
				t.Errorf("restart sınıfı: %v retrySafe=%v", s.Class, s.RetrySafe)
			}
		}
		if preview[s.Name] && (s.Class != ClassReadOnly || !s.RetrySafe) {
			t.Errorf("%s preview olmalı (read_only/retry-safe, yan etkisiz)", s.Name)
		}
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
