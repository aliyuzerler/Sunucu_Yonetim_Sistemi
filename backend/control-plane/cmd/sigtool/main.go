// Command sigtool signs and verifies release artifacts with the SCP-SIG-V1
// scheme (ADR-0020): ed25519 over `scp-sign-v1\n<name>\n<size>\n<sha256>\n`.
//
// Kullanım:
//
//	sigtool keygen [--priv <dosya>]        → yeni anahtar çifti; özel base64
//	                                         dosyaya (0600), genel yazdırılır
//	sigtool sign --priv <dosya|--priv-b64> --file <artifact>
//	                                       → "<sha256>  <sig-b64>  <name>" satırı
//	sigtool verify --pub <base64> --file <artifact> --sig <base64>
//
// Özel anahtar ASLA depoya yazılmaz; yalnız sürüm yöneticisinde kalır. Genel
// anahtar repo'da yayınlanır (agents + operatör doğrulama güven kökü).
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/signpkg"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "sigtool: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("kullanım: sigtool keygen | sign | verify")
	}
	switch args[0] {
	case "keygen":
		return keygen(args[1:])
	case "sign":
		return sign(args[1:])
	case "verify":
		return verify(args[1:])
	default:
		return fmt.Errorf("bilinmeyen alt komut: %s", args[0])
	}
}

func keygen(args []string) error {
	privPath := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--priv" && i+1 < len(args) {
			i++
			privPath = args[i]
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	privB64 := base64.StdEncoding.EncodeToString(priv)
	if privPath != "" {
		if err := os.WriteFile(privPath, []byte(privB64+"\n"), 0o600); err != nil {
			return fmt.Errorf("özel anahtar yazılamadı: %w", err)
		}
		fmt.Println("Özel anahtar:", privPath, "(0600 — ASLA depoya yazılmaz)")
		fmt.Println("Genel anahtar (repo'ya yayınlanır):")
	} else {
		fmt.Println("Özel anahtar (base64 — güvenli saklayın):")
	}
	fmt.Println(privB64)
	fmt.Println()
	fmt.Println("GENEL (base64):")
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

func sign(args []string) error {
	privB64, privFile, file := "", "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--priv":
			i++
			if i < len(args) {
				privB64 = args[i]
			}
		case "--priv-file":
			i++
			if i < len(args) {
				privFile = args[i]
			}
		case "--file":
			i++
			if i < len(args) {
				file = args[i]
			}
		}
	}
	if file == "" || (privB64 == "" && privFile == "") {
		return errors.New("kullanım: sigtool sign (--priv <base64> | --priv-file <yol>) --file <artifact>")
	}
	if privB64 == "" {
		raw, err := os.ReadFile(privFile)
		if err != nil {
			return err
		}
		privB64 = strings.TrimSpace(string(raw))
	}
	raw, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return errors.New("özel anahtar geçersiz")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	sig, digest, err := signpkg.SignArtifact(ed25519.PrivateKey(raw), baseName(file), data)
	if err != nil {
		return err
	}
	fmt.Printf("%s  %s  %s\n", digest, sig, baseName(file))
	return nil
}

func verify(args []string) error {
	pubB64, file, sig := "", "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--pub":
			i++
			if i < len(args) {
				pubB64 = args[i]
			}
		case "--file":
			i++
			if i < len(args) {
				file = args[i]
			}
		case "--sig":
			i++
			if i < len(args) {
				sig = args[i]
			}
		}
	}
	if pubB64 == "" || file == "" || sig == "" {
		return errors.New("kullanım: sigtool verify --pub <base64> --file <artifact> --sig <base64>")
	}
	pub, err := signpkg.ParsePublicKey(pubB64)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if err := signpkg.VerifyArtifact(pub, baseName(file), int64(len(data)), signpkg.SHA256Hex(data), sig); err != nil {
		return fmt.Errorf("DOĞRULAMA BAŞARISIZ: %w", err)
	}
	fmt.Println("İmza GEÇERLİ:", file)
	return nil
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
