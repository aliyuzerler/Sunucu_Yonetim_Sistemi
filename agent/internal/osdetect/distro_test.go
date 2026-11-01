//go:build linux

package osdetect

import "testing"

// Faz 06: distro support matrix — false-positive guards. Pure-function
// classification is the contract; fixtures below cover Rocky/Alma and the
// negative cases the acceptance criteria demand.

func TestClassifyDistroSupported(t *testing.T) {
	cases := []struct {
		id, ver string
		family  string
	}{
		{"rocky", "9.5", "rhel"},
		{"rocky", "10.0", "rhel"},
		{"almalinux", "9.6", "rhel"},
		{"almalinux", "10", "rhel"},
		{"ubuntu", "24.04", "deb"},
		{"debian", "13", "deb"},
	}
	for _, c := range cases {
		fam, state, reason := ClassifyDistro(c.id, c.ver, "")
		if fam != c.family || state != SupportSupported {
			t.Errorf("ClassifyDistro(%s,%s) = %s/%s, beklenen %s/supported (%s)", c.id, c.ver, fam, state, c.family, reason)
		}
	}
}

func TestClassifyDistroUntestedMajor(t *testing.T) {
	// Rocky 8: known distro, UNLISTED major → untested (never supported).
	fam, state, reason := ClassifyDistro("rocky", "8.10", "rhel centos fedora")
	if fam != "rhel" || state != SupportUntested {
		t.Fatalf("rocky 8.10 = %s/%s, beklenen rhel/untested", fam, state)
	}
	if reason == "" {
		t.Fatal("gerekçe boş olmamalı")
	}
	// Malformed VERSION_ID: never a false positive.
	if _, state, _ := ClassifyDistro("rocky", "", ""); state != SupportUntested {
		t.Fatalf("boş VERSION_ID untested beklenir: %s", state)
	}
	if _, state, _ := ClassifyDistro("rocky", "nine", ""); state != SupportUntested {
		t.Fatalf("sayısal olmayan VERSION_ID untested beklenir: %s", state)
	}
}

func TestClassifyDistroUnknownIDNoFalsePositive(t *testing.T) {
	// An ID_LIKE=rhel distro that is NOT in the matrix must never claim
	// support (criterion: detection must not produce false positives).
	fam, state, _ := ClassifyDistro("openmandriva", "5.0", "rhel fedora")
	if fam != "rhel-like" || state != SupportUnknown {
		t.Fatalf("ID_LIKE yalnız coarse family vermeli: %s/%s", fam, state)
	}
	if _, state, _ := ClassifyDistro("sles", "15.6", ""); state != SupportUnknown {
		t.Fatalf("bilinmeyen ID unknown beklenir: %s", state)
	}
}

func TestMajorOf(t *testing.T) {
	cases := map[string]string{
		"9.5": "9", "24.04": "24", "10": "10", "13": "13",
		"9.5-elan": "9", "9+deb12u5": "9", "12~test": "12",
		"": "", "nine": "", ".5": "",
	}
	for in, want := range cases {
		if got := majorOf(in); got != want {
			t.Errorf("majorOf(%q) = %q, beklenen %q", in, got, want)
		}
	}
}

// Real os-release fixtures for Rocky/Alma (disposable container evidence is
// produced by the CI agent-rhel job; here the parse+classify path is pinned).
func TestDetectRocky9Fixture(t *testing.T) {
	f, err := Detect("testdata/rocky-9", func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if f.OSID != "rocky" || f.OSVersionID != "9.5" {
		t.Fatalf("rocky kimliği beklenir: %s %s", f.OSID, f.OSVersionID)
	}
	if f.DistroFamily != "rhel" || f.SupportState != SupportSupported {
		t.Fatalf("rocky 9 destekli-rhel beklenir: %s/%s", f.DistroFamily, f.SupportState)
	}
	if f.PackageManager != "dnf" {
		t.Fatalf("dnf beklenir: %s", f.PackageManager)
	}
	if f.DistroLike != "rhel centos fedora" {
		t.Fatalf("ID_LIKE: %q", f.DistroLike)
	}
}

func TestDetectAlmalinux9Fixture(t *testing.T) {
	f, err := Detect("testdata/almalinux-9", func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if f.OSID != "almalinux" || f.OSVersionID != "9.6" {
		t.Fatalf("almalinux kimliği beklenir: %s %s", f.OSID, f.OSVersionID)
	}
	if f.DistroFamily != "rhel" || f.SupportState != SupportSupported {
		t.Fatalf("almalinux 9 destekli-rhel beklenir: %s/%s", f.DistroFamily, f.SupportState)
	}
}
