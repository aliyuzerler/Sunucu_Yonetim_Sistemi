package identity

import (
	"strings"
	"testing"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("hash hatası: %v", err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Fatalf("PHC öneki bekleniyordu: %s", h)
	}
	ok, err := VerifyPassword("correct horse battery", h)
	if err != nil || !ok {
		t.Fatalf("doğru parola reddedildi: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("wrong horse battery", h)
	if err != nil {
		t.Fatalf("yanlış parola doğrulaması hata verdi: %v", err)
	}
	if ok {
		t.Fatal("yanlış parola kabul edildi")
	}
}

func TestHashUniquePerCall(t *testing.T) {
	a, _ := HashPassword("same password 123")
	b, _ := HashPassword("same password 123")
	if a == b {
		t.Fatal("her hash çağrısı benzersiz salt üretmeli")
	}
}

func TestHashPasswordRejectsShortAndLong(t *testing.T) {
	if _, err := HashPassword("kısa"); err == nil {
		t.Fatal("kısa parola reddedilmeli")
	}
	if _, err := HashPassword(string(make([]byte, 257))); err == nil {
		t.Fatal("çok uzun parola reddedilmeli")
	}
}

func TestVerifyRejectsTamperedPHC(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPassword("x", "$argon2id$v=19$m=1,t=1,p=1$abc$def"); err == nil {
		t.Fatal("bilinmeyen parametreli PHC reddedilmeli")
	}
	if _, err := VerifyPassword("x", "not-a-phc"); err == nil {
		t.Fatal("bozuk PHC reddedilmeli")
	}
	// Hash byte'larını boz: doğrulama başarısız olmalı, panik değil.
	bad := h[:len(h)-2] + "xx"
	if ok, err := VerifyPassword("correct horse battery", bad); ok && err == nil {
		t.Fatal("bozulmuş hash kabul edildi")
	}
}
