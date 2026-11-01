package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/inventory"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/testdb"
)

func pgSvc(t *testing.T) (*Service, *inventory.Service) {
	t.Helper()
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	inv := inventory.NewService(inventory.NewPGStore(pool), nil)
	return NewService(NewPGStore(pool)), inv
}

// TestFactsUpsertPG: upsert son yazanı saklar; node silinince facts silinir.
func TestFactsUpsertPG(t *testing.T) {
	svc, inv := pgSvc(t)
	ctx := context.Background()

	node, err := inv.CreateNode(ctx, "admin", "corr-t-1", inventory.CreateNodeInput{
		DisplayName: "web-01", OSFamily: inventory.OSLinux,
	})
	if err != nil {
		t.Fatal(err)
	}

	t1 := time.Now().UTC().Add(-time.Minute)
	err = svc.UpsertFacts(ctx, &SystemFacts{
		NodeID: node.ID, OSID: "ubuntu", OSVersionID: "24.04",
		OSPrettyName: "Ubuntu 24.04.3 LTS", KernelRelease: "6.8.0-49-generic",
		Arch: "amd64", SystemdVersion: "255", PackageManager: "apt",
		SystemdAvailable: true, Source: "os-release+procfs+systemctl", CollectedAt: t1,
	})
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}

	f, err := svc.GetFacts(ctx, node.ID)
	if err != nil || f.OSID != "ubuntu" || !f.SystemdAvailable {
		t.Fatalf("facts okunmalı: %+v %v", f, err)
	}

	// İkinci rapor üstüne yazar (Debian'a re-install senaryosu).
	t2 := time.Now().UTC()
	err = svc.UpsertFacts(ctx, &SystemFacts{
		NodeID: node.ID, OSID: "debian", OSVersionID: "13",
		OSPrettyName: "Debian GNU/Linux 13 (trixie)", PackageManager: "apt",
		SystemdAvailable: true, Source: "os-release", CollectedAt: t2,
	})
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	f, err = svc.GetFacts(ctx, node.ID)
	if err != nil || f.OSID != "debian" || f.OSVersionID != "13" {
		t.Fatalf("son rapor kazanmalı: %+v %v", f, err)
	}
	if f.KernelRelease != "" {
		t.Fatalf("boş alan sıfırlanmalı: %q", f.KernelRelease)
	}

	// Node yoksa GetFacts 404 mantığı döner.
	if _, err := svc.GetFacts(ctx, "00000000-0000-0000-0000-0000000000ff"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("yok kayıt ErrNotFound: %v", err)
	}

	// NodeID boşsa reddedilir.
	if err := svc.UpsertFacts(ctx, &SystemFacts{OSID: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("boş node reddedilmeli: %v", err)
	}
}

// TestMetricsPG: insert + latest + list + validate + prune.
func TestMetricsPG(t *testing.T) {
	svc, inv := pgSvc(t)
	ctx := context.Background()

	node, err := inv.CreateNode(ctx, "admin", "corr-t-2", inventory.CreateNodeInput{
		DisplayName: "db-01", OSFamily: inventory.OSLinux,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	snap := &Snapshot{
		CollectedAt: now, Source: "procfs+statfs",
		CPU:    CPUSample{Percent: 12.5, LogicalCores: 4, Load1m: 0.4, Load5m: 0.3, Load15m: 0.2},
		Memory: MemorySample{TotalBytes: 16 << 30, AvailableBytes: 8 << 30, UsedBytes: 8 << 30, UsedPercent: 50},
		Disks:  []DiskSample{{Device: "/dev/vda1", MountPoint: "/", FSType: "ext4", TotalBytes: 40 << 30, UsedBytes: 10 << 30, AvailableBytes: 30 << 30}},
		Networks: []NetworkSample{
			{Interface: "ens160", RxBytes: 1000, TxBytes: 2000},
		},
		UptimeSeconds: 86400,
	}
	if err := svc.InsertMetrics(ctx, node.ID, snap); err != nil {
		t.Fatalf("insert: %v", err)
	}

	latest, err := svc.LatestMetrics(ctx, node.ID)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest.CPU.Percent != 12.5 || latest.Memory.TotalBytes != 16<<30 || len(latest.Disks) != 1 || len(latest.Networks) != 1 {
		t.Fatalf("latest okunmalı: %+v", latest)
	}
	if latest.Source != "procfs+statfs" {
		t.Fatalf("kaynak korunmalı: %q", latest.Source)
	}

	// İkinci, daha yeni anlık görüntü: latest değişir, list 2 olur.
	later := *snap
	later.CollectedAt = now.Add(time.Minute)
	later.CPU.Percent = 55.5
	if err := svc.InsertMetrics(ctx, node.ID, &later); err != nil {
		t.Fatal(err)
	}
	latest, err = svc.LatestMetrics(ctx, node.ID)
	if err != nil || latest.CPU.Percent != 55.5 {
		t.Fatalf("en yeni snapshot: %+v %v", latest, err)
	}
	list, err := svc.ListMetrics(ctx, node.ID, 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %d %v", len(list), err)
	}
	if list[0].CollectedAt.Before(list[1].CollectedAt) {
		t.Fatalf("list azalan sırada olmalı")
	}

	// Geçersiz snapshot reddedilir (DB'ye yazılmaz).
	bad := &Snapshot{Source: "x"} // CollectedAt sıfır
	if err := svc.InsertMetrics(ctx, node.ID, bad); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("geçersiz snapshot reddedilmeli: %v", err)
	}
	tooMany := &Snapshot{CollectedAt: now, Disks: make([]DiskSample, MaxDisks+1)}
	if err := svc.InsertMetrics(ctx, node.ID, tooMany); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("limit aşımı reddedilmeli: %v", err)
	}

	// Prune: cutoff'tan eski satırlar silinir; yeni tutulur.
	n, err := svc.store.Prune(ctx, now.Add(30*time.Second))
	if err != nil || n != 1 {
		t.Fatalf("prune 1 satır silmeli (now+30s cutoff): %d %v", n, err)
	}
	n, err = svc.store.Prune(ctx, now.Add(2*time.Minute))
	if err != nil || n != 1 {
		t.Fatalf("prune kalan 1 satırı da silmeli (now+2dk cutoff): %d %v", n, err)
	}
	list, err = svc.ListMetrics(ctx, node.ID, 10)
	if err != nil || len(list) != 0 {
		t.Fatalf("prune sonrası boş: %d %v", len(list), err)
	}

	// ListMetrics limit sınırlaması (≤200).
	if _, err := svc.ListMetrics(ctx, node.ID, 500); err != nil {
		t.Fatalf("limit kırpılmalı, hata dönmemeli: %v", err)
	}
}
