package signpkg

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("anahtar üretimi: %v", err)
	}
	return pub, priv
}

func TestSignArtifactRoundtrip(t *testing.T) {
	pub, priv := testKey(t)
	data := []byte("artifact-bytes")
	sig, digest, err := SignArtifact(priv, "server-control-agent_0.16.0-rc1_amd64.deb", data)
	if err != nil {
		t.Fatalf("SignArtifact: %v", err)
	}
	if digest != SHA256Hex(data) {
		t.Fatalf("özet uyuşmadı")
	}
	if err := VerifyArtifact(pub, "server-control-agent_0.16.0-rc1_amd64.deb", int64(len(data)), digest, sig); err != nil {
		t.Fatalf("doğrulama başarısız: %v", err)
	}
}

func TestVerifyArtifactRejectsTampering(t *testing.T) {
	pub, priv := testKey(t)
	data := []byte("artifact-bytes")
	sig, digest, _ := SignArtifact(priv, "agent.deb", data)

	cases := []struct {
		name string
		call func() error
	}{
		{"içerik-değişti", func() error {
			return VerifyArtifact(pub, "agent.deb", int64(len(data))+1, digest, sig)
		}},
		{"ad-değişti", func() error {
			return VerifyArtifact(pub, "agent2.deb", int64(len(data)), digest, sig)
		}},
		{"özet-değişti", func() error {
			return VerifyArtifact(pub, "agent.deb", int64(len(data)), strings.Repeat("0", 64), sig)
		}},
		{"imza-bozuk", func() error {
			bad := sig[:len(sig)-2] + "AA"
			return VerifyArtifact(pub, "agent.deb", int64(len(data)), digest, bad)
		}},
		{"yanlış-anahtar", func() error {
			other, _ := testKey(t)
			return VerifyArtifact(other, "agent.deb", int64(len(data)), digest, sig)
		}},
	}
	for _, tc := range cases {
		if err := tc.call(); err == nil {
			t.Fatalf("%s: reddedilmedi", tc.name)
		}
	}
}

func TestCheckName(t *testing.T) {
	for _, ok := range []string{"agent.deb", "server-control-agent_1.0_amd64.rpm", "A.b-c_d9.zip"} {
		if err := CheckName(ok); err != nil {
			t.Fatalf("%q reddedildi: %v", ok, err)
		}
	}
	for _, bad := range []string{"../evil", "a/b", "a\\b", ".hidden", "", "x..y", strings.Repeat("a", 200)} {
		if err := CheckName(bad); err == nil {
			t.Fatalf("%q kabul edildi", bad)
		}
	}
}

func TestCanonicalJSONDeterministic(t *testing.T) {
	a := json.RawMessage(`{"b":2,"a":{"y":"1","x":10},"c":[1,2]}`)
	b := json.RawMessage(`{"c":[1,2],"a":{"x":10,"y":"1"},"b":2}`)
	ca, err := CanonicalJSON(a)
	if err != nil {
		t.Fatalf("CanonicalJSON a: %v", err)
	}
	cb, err := CanonicalJSON(b)
	if err != nil {
		t.Fatalf("CanonicalJSON b: %v", err)
	}
	if string(ca) != string(cb) {
		t.Fatalf("kanonik yazım sıra-bağımsız değil:\n%s\n%s", ca, cb)
	}
	// Sayılar tam korunmalı (float64 yuvarlaması olmamalı).
	big := json.RawMessage(`{"n":12345678901234567890}`)
	cbig, err := CanonicalJSON(big)
	if err != nil {
		t.Fatalf("CanonicalJSON big: %v", err)
	}
	if !strings.Contains(string(cbig), "12345678901234567890") {
		t.Fatalf("büyük sayı bozuldu: %s", cbig)
	}
}

func TestVerifyManifestBytes(t *testing.T) {
	pub, priv := testKey(t)
	raw := json.RawMessage(`{"name":"demo","version":"1.0.0","api_version":">=1.0.0"}`)
	canonical, err := CanonicalJSON(raw)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	sig := ed25519.Sign(priv, canonical)
	sigB64 := base64.StdEncoding.EncodeToString(sig)
	if err := VerifyManifestBytes(pub, canonical, sigB64); err != nil {
		t.Fatalf("manifest doğrulaması: %v", err)
	}
	// Tek alan eklense bile imza düşer (kanonik baytlar değişir).
	tampered := append([]byte{}, canonical...)
	tampered = append(tampered, []byte(" ")...)
	if err := VerifyManifestBytes(pub, tampered, sigB64); err == nil {
		t.Fatalf("bozulmuş manifest reddedilmedi")
	}
}

func TestParsePublicKey(t *testing.T) {
	pub, _ := testKey(t)
	b64 := base64.StdEncoding.EncodeToString(pub)
	got, err := ParsePublicKey(b64)
	if err != nil {
		t.Fatalf("ParsePublicKey: %v", err)
	}
	if len(got) != ed25519.PublicKeySize {
		t.Fatalf("anahtar uzunluğu: %d", len(got))
	}
	if _, err := ParsePublicKey("not-base64!!"); err == nil {
		t.Fatalf("geçersiz base64 reddedilmedi")
	}
	if _, err := ParsePublicKey(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatalf("kısa anahtar reddedilmedi")
	}
}
