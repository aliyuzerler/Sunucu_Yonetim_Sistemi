package inventory

import (
	"context"
	"testing"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/testdb"
)

func pgSvc(t *testing.T) (*Service, *PGStore) {
	t.Helper()
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	store := NewPGStore(pool)
	return NewService(store, audit.NewPGRecorder(pool)), store
}

// TestNodesPG: node oluşturma, etiket, filtre, imleç, facts, capability.
func TestNodesPG(t *testing.T) {
	svc, store := pgSvc(t)
	ctx := context.Background()

	n1, err := svc.CreateNode(ctx, "admin", "corr-i-1", CreateNodeInput{
		DisplayName: "web-01", Hostname: strPtr("web01.internal"),
		OSFamily: OSLinux, Tags: []string{"prod", "web"},
	})
	if err != nil {
		t.Fatalf("node oluşturulamadı: %v", err)
	}
	n2, err := svc.CreateNode(ctx, "admin", "corr-i-2", CreateNodeInput{
		DisplayName: "db-01", OSFamily: OSLinux, Tags: []string{"prod", "db"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get + tags.
	got, err := svc.GetNode(ctx, n1.ID)
	if err != nil || len(got.Tags) != 2 {
		t.Fatalf("node + etiketler: %+v %v", got, err)
	}

	// Filtre: tag=db yalnız db-01.
	page, err := svc.ListNodes(ctx, ListFilter{Tag: "db"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Nodes) != 1 || page.Nodes[0].ID != n2.ID {
		t.Fatalf("tag filtresi: %+v", page.Nodes)
	}
	// Filtre: status.
	page, err = svc.ListNodes(ctx, ListFilter{Status: StatusPending})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Nodes) != 2 {
		t.Fatalf("status filtresi: %d", len(page.Nodes))
	}

	// Facts upsert: sürüm artmalı.
	if err := svc.SetFacts(ctx, "admin", "corr-i-3", n1.ID, map[string]any{"kernel": "6.12"}); err != nil {
		t.Fatalf("facts: %v", err)
	}
	if err := svc.SetFacts(ctx, "admin", "corr-i-3", n1.ID, map[string]any{"kernel": "6.12.1"}); err != nil {
		t.Fatalf("facts ikinci: %v", err)
	}
	var version int
	if err := store.pool.QueryRow(ctx,
		`SELECT facts_version FROM node_facts WHERE node_id = $1`, n1.ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("facts sürümü 2 olmalı: %d", version)
	}

	// Capability upsert.
	if err := svc.UpsertCapability(ctx, "admin", "corr-i-4", &Capability{
		NodeID: n1.ID, CapabilityID: "linux.systemd.service.restart", Version: "1.0.0", Available: true,
	}); err != nil {
		t.Fatalf("capability: %v", err)
	}
	caps, err := svc.ListCapabilities(ctx, n1.ID)
	if err != nil || len(caps) != 1 || !caps[0].Available {
		t.Fatalf("capability listesi: %+v %v", caps, err)
	}

	// Count.
	if c, err := svc.CountNodes(ctx); err != nil || c != 2 {
		t.Fatalf("count: %d %v", c, err)
	}
}

// TestGroupsPG: grup oluşturma, atama, RBAC scope verisi.
func TestGroupsPG(t *testing.T) {
	svc, store := pgSvc(t)
	ctx := context.Background()

	n, err := svc.CreateNode(ctx, "admin", "corr-g-1", CreateNodeInput{DisplayName: "app-01", OSFamily: OSLinux})
	if err != nil {
		t.Fatal(err)
	}
	g1, err := svc.CreateGroup(ctx, "admin", "corr-g-1", "prod-tr", "Üretim TR")
	if err != nil {
		t.Fatalf("grup: %v", err)
	}
	g2, err := svc.CreateGroup(ctx, "admin", "corr-g-1", "prod-eu", "")
	if err != nil {
		t.Fatal(err)
	}

	updated, err := svc.AssignGroup(ctx, "admin", "corr-g-1", n.ID, []string{g1.ID, g2.ID})
	if err != nil {
		t.Fatalf("atama: %v", err)
	}
	if len(updated.GroupIDs) != 2 {
		t.Fatalf("grup üyelikleri: %v", updated.GroupIDs)
	}
	ids, err := store.NodeGroupIDs(ctx, n.ID)
	if err != nil || len(ids) != 2 {
		t.Fatalf("NodeGroupIDs: %v %v", ids, err)
	}
	// Tekrar atama: replace davranışı.
	if _, err := svc.AssignGroup(ctx, "admin", "corr-g-1", n.ID, []string{g1.ID}); err != nil {
		t.Fatal(err)
	}
	ids, _ = store.NodeGroupIDs(ctx, n.ID)
	if len(ids) != 1 {
		t.Fatalf("replace bekleniyordu: %v", ids)
	}
	// Grup listesi.
	groups, err := svc.ListGroups(ctx)
	if err != nil || len(groups) != 2 {
		t.Fatalf("grup listesi: %d %v", len(groups), err)
	}
}
