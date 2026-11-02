package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/signpkg"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/updates"
)

// runVerifyUpdate implements `control-plane verify-update --manifest <dosya>
// --key <base64>`: it validates an update manifest and verifies its ed25519
// signature (SCP-UPDATE-MANIFEST-V1 / SCP-SIG-V1, ADR-0020). Exit code 0 only
// on a fully verified manifest — release procedure refuses otherwise.
func runVerifyUpdate(args []string) error {
	manifestPath, keyB64, keyFile := "", "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--manifest":
			i++
			if i < len(args) {
				manifestPath = args[i]
			}
		case "--key":
			i++
			if i < len(args) {
				keyB64 = args[i]
			}
		case "--key-file":
			i++
			if i < len(args) {
				keyFile = args[i]
			}
		}
	}
	if manifestPath == "" || (keyB64 == "" && keyFile == "") {
		return errors.New("kullanım: verify-update --manifest <dosya> (--key <base64> | --key-file <yol>)")
	}
	if keyB64 == "" {
		raw, err := os.ReadFile(keyFile)
		if err != nil {
			return fmt.Errorf("anahtar dosyası okunamadı: %w", err)
		}
		keyB64 = strings.TrimSpace(string(raw))
	}
	pub, err := signpkg.ParsePublicKey(keyB64)
	if err != nil {
		return fmt.Errorf("anahtar: %w", err)
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("manifest okunamadı: %w", err)
	}
	m, _, err := updates.ParseManifest(raw)
	if err != nil {
		return err
	}
	canonical, err := m.Canonical(json.RawMessage(raw))
	if err != nil {
		return err
	}
	var key ed25519.PublicKey = pub
	if err := signpkg.VerifyManifestBytes(key, canonical, m.Signature); err != nil {
		return fmt.Errorf("İMZA GEÇERSİZ: %w", err)
	}
	fmt.Println("İmza GEÇERLİ (SCP-UPDATE-MANIFEST-V1):")
	fmt.Println("  hedef    :", m.Target)
	fmt.Println("  sürüm    :", m.Version)
	fmt.Println("  dosya    :", m.FileName)
	fmt.Printf("  boyut    : %d bayt\n", m.SizeBytes)
	fmt.Println("  sha256   :", m.SHA256)
	if m.MinPrev != "" {
		fmt.Println("  min_prev :", m.MinPrev)
	}
	fmt.Println("  api      :", m.APIVersion)
	return nil
}
