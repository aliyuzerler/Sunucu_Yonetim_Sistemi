package secrets

import (
	"bytes"
	"errors"
	"testing"
)

func testKey(t *testing.T) Key {
	t.Helper()
	k, err := Generate()
	if err != nil {
		t.Fatalf("anahtar üretilemedi: %v", err)
	}
	return k
}

func TestRoundTrip(t *testing.T) {
	k := testKey(t)
	plain := []byte(`{"api_token":"gizli-token-degeri-1234567890"}`)
	sealed, err := k.Encrypt(plain)
	if err != nil {
		t.Fatalf("şifreleme: %v", err)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("şifreli çıktı düz metni içermemeli")
	}
	got, err := k.Decrypt(sealed)
	if err != nil {
		t.Fatalf("çözme: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("düz metin birebir dönmeli")
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	k1, k2 := testKey(t), testKey(t)
	sealed, err := k1.Encrypt([]byte("gizli"))
	if err != nil {
		t.Fatalf("şifreleme: %v", err)
	}
	if _, err := k2.Decrypt(sealed); !errors.Is(err, ErrCiphertextInval) {
		t.Fatalf("yanlış anahtar ErrCiphertextInval olmalı: %v", err)
	}
}

func TestTamperedCiphertextFails(t *testing.T) {
	k := testKey(t)
	sealed, err := k.Encrypt([]byte("gizli-veri-0123456789"))
	if err != nil {
		t.Fatalf("şifreleme: %v", err)
	}
	sealed[len(sealed)-1] ^= 0x01
	if _, err := k.Decrypt(sealed); !errors.Is(err, ErrCiphertextInval) {
		t.Fatalf("bozulmuş şifreli veri reddedilmeli: %v", err)
	}
}

func TestKeyValidation(t *testing.T) {
	if _, err := NewKey([]byte("kısa")); !errors.Is(err, ErrKeyInvalid) {
		t.Fatalf("kısa anahtar reddedilmeli: %v", err)
	}
	bad := "!!!geçersiz-base64!!!"
	if _, err := ParseKey(bad); !errors.Is(err, ErrKeyInvalid) {
		t.Fatalf("geçersiz base64 reddedilmeli: %v", err)
	}
	// 16 baytlık base64 de reddedilmeli (AES-256 için 32 gerekli).
	if _, err := ParseKey("MTIzNDU2Nzg5MDEyMzQ1Ng=="); !errors.Is(err, ErrKeyInvalid) {
		t.Fatalf("16 baytlık anahtar reddedilmeli: %v", err)
	}
}

func TestParseKeyEncodingTolerance(t *testing.T) {
	k, err := ParseKey("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatalf("standart base64 kabul edilmeli: %v", err)
	}
	// URL-safe, padsiz aynı baytlar.
	if _, err := ParseKey("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"); err != nil {
		t.Fatalf("padsiz base64 kabul edilmeli: %v", err)
	}
	_ = k
}

func TestPlaintextLimit(t *testing.T) {
	k := testKey(t)
	big := make([]byte, MaxPlaintext+1)
	if _, err := k.Encrypt(big); !errors.Is(err, ErrCiphertextTooBig) {
		t.Fatalf("sınır aşımı reddedilmeli: %v", err)
	}
}

func TestCiphertextLengthDiffersPerCall(t *testing.T) {
	// Rastgele nonce: aynı düz metin iki farklı şifreli üretmeli.
	k := testKey(t)
	a, _ := k.Encrypt([]byte("aynı"))
	b, _ := k.Encrypt([]byte("aynı"))
	if bytes.Equal(a, b) {
		t.Fatal("nonce determinizm kusuru: aynı düz metin aynı şifreli üretti")
	}
}
