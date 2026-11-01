//go:build linux

package pkgops

import (
	"context"
	"os"
	"testing"
)

// TestRealPkgList runs against the REAL host. Invoked by the CI agent-rhel
// job inside disposable Rocky/Alma containers (dnf/rpm present). Gated by
// EXPECTED_OS_ID; fails loudly when the gate is set but the host doesn't
// match — a silent skip would prove nothing.
func TestRealPkgList(t *testing.T) {
	if os.Getenv("EXPECTED_OS_ID") == "" {
		t.Skip("EXPECTED_OS_ID verilmedi (yalnız CI agent-rhel job'ında koşar)")
	}
	r := &Runner{}
	pkgs, err := r.List(context.Background(), 500)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(pkgs) < 10 {
		t.Fatalf("gerçek rpm envanteri en az 10 paket beklenir: %d", len(pkgs))
	}
	rpmCount := 0
	for _, p := range pkgs {
		if p.Name == "" || p.Version == "" {
			t.Fatalf("alanlar boş: %+v", p)
		}
		if p.Source == "rpm" {
			rpmCount++
		}
	}
	if rpmCount == 0 {
		t.Fatalf("rpm kaynaklı paket beklenir (rocky/alma): %+v", pkgs[:3])
	}
	t.Logf("gerçek envanter: %d paket (ör. %s %s)", len(pkgs), pkgs[0].Name, pkgs[0].Version)
}
