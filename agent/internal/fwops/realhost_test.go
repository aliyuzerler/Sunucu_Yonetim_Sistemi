//go:build linux

package fwops

import (
	"context"
	"os"
	"testing"
)

// TestRealFirewall runs against the REAL host. Invoked by the CI agent-rhel
// job AFTER it starts firewalld inside the disposable container: probe →
// firewalld active, rules listing bounded, preview renders per-backend
// commands WITHOUT executing anything (ADR-0010).
func TestRealFirewall(t *testing.T) {
	if os.Getenv("EXPECTED_OS_ID") == "" {
		t.Skip("EXPECTED_OS_ID verilmedi (yalnız CI agent-rhel job'ında koşar)")
	}
	r := &Runner{}
	ctx := context.Background()
	st := r.Probe(ctx)
	t.Logf("gerçek firewall: backend=%s active=%v reason=%s", st.Backend, st.Active, st.Reason)
	if st.Backend != BackendFirewalld {
		t.Fatalf("gerçek container'da firewalld beklenir: %+v", st)
	}
	if !st.Active {
		t.Fatalf("firewalld çalışıyor olmalı (job başlattı): %+v", st)
	}
	rs, err := r.Rules(ctx, 200)
	if err != nil {
		t.Fatalf("Rules: %v", err)
	}
	if rs.Backend != BackendFirewalld || len(rs.Lines) == 0 {
		t.Fatalf("firewalld kural listesi boş olmamalı: backend=%s lines=%d", rs.Backend, len(rs.Lines))
	}
	// Önizleme: komutları VERİ olarak döndürür; asla çalıştırmaz.
	pv, err := RenderPreview(st.Backend, PreviewParams{Zone: "public", Port: 8443, Protocol: "tcp", Action: ActionAllow})
	if err != nil {
		t.Fatalf("RenderPreview: %v", err)
	}
	if pv.Executed {
		t.Fatal("önizleme asla executed=true olamaz (ADR-0010)")
	}
	if len(pv.Commands) == 0 || pv.Commands[0] != "firewall-cmd --permanent --zone=public --add-port=8443/tcp" {
		t.Fatalf("firewalld önizleme komutları: %+v", pv.Commands)
	}
	t.Logf("gerçek kural listesi: %d satır; önizleme: %q", len(rs.Lines), pv.Commands[0])
}
