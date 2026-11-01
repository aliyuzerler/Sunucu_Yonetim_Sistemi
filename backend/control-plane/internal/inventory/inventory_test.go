package inventory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
)

// memStore is an in-memory Store fake for unit tests.
type memStore struct {
	nodes  map[string]*Node
	groups map[string]*Group
	facts  map[string]map[string]any
	caps   map[string][]Capability
	seq    int
}

func newMemStore() *memStore {
	return &memStore{
		nodes:  map[string]*Node{},
		groups: map[string]*Group{},
		facts:  map[string]map[string]any{},
		caps:   map[string][]Capability{},
	}
}

func (m *memStore) CreateNode(_ context.Context, n *Node) error {
	m.seq++
	n.ID = "11111111-1111-1111-1111-" + pad(m.seq)
	n.Status = StatusPending
	cp := *n
	m.nodes[n.ID] = &cp
	return nil
}

func pad(i int) string {
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	for len(s) < 12 {
		s = "0" + s
	}
	return s
}

func (m *memStore) GetNode(_ context.Context, id string) (*Node, error) {
	if n, ok := m.nodes[id]; ok {
		cp := *n
		return &cp, nil
	}
	return nil, ErrNotFound
}

func (m *memStore) ListNodes(_ context.Context, f ListFilter) (NodePage, error) {
	page := NodePage{Nodes: []Node{}}
	for _, n := range m.nodes {
		if f.Status != "" && n.Status != f.Status {
			continue
		}
		if f.Tag != "" && !contains(n.Tags, f.Tag) {
			continue
		}
		page.Nodes = append(page.Nodes, *n)
	}
	return page, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (m *memStore) UpdateNode(_ context.Context, n *Node) error {
	if _, ok := m.nodes[n.ID]; !ok {
		return ErrNotFound
	}
	cp := *n
	m.nodes[n.ID] = &cp
	return nil
}

func (m *memStore) SetTags(_ context.Context, nodeID string, tags []string) error {
	n := m.nodes[nodeID]
	if n == nil {
		return ErrNotFound
	}
	n.Tags = tags
	return nil
}

func (m *memStore) ReplaceGroupMemberships(_ context.Context, nodeID string, groupIDs []string) error {
	n := m.nodes[nodeID]
	if n == nil {
		return ErrNotFound
	}
	n.GroupIDs = groupIDs
	return nil
}

func (m *memStore) SetFacts(_ context.Context, nodeID string, facts map[string]any, _ time.Time) error {
	if _, ok := m.nodes[nodeID]; !ok {
		return ErrNotFound
	}
	m.facts[nodeID] = facts
	return nil
}

func (m *memStore) UpsertCapability(_ context.Context, c *Capability) error {
	if _, ok := m.nodes[c.NodeID]; !ok {
		return ErrNotFound
	}
	list := m.caps[c.NodeID]
	for i := range list {
		if list[i].CapabilityID == c.CapabilityID {
			list[i] = *c
			return nil
		}
	}
	m.caps[c.NodeID] = append(list, *c)
	return nil
}

func (m *memStore) ListCapabilities(_ context.Context, nodeID string) ([]Capability, error) {
	return m.caps[nodeID], nil
}

func (m *memStore) CreateGroup(_ context.Context, g *Group) error {
	m.seq++
	g.ID = "22222222-2222-2222-2222-" + pad(m.seq)
	cp := *g
	m.groups[g.ID] = &cp
	return nil
}

func (m *memStore) GetGroup(_ context.Context, id string) (*Group, error) {
	if g, ok := m.groups[id]; ok {
		cp := *g
		return &cp, nil
	}
	return nil, ErrNotFound
}

func (m *memStore) ListGroups(_ context.Context) ([]Group, error) {
	out := []Group{}
	for _, g := range m.groups {
		out = append(out, *g)
	}
	return out, nil
}

func (m *memStore) NodeGroupIDs(_ context.Context, nodeID string) ([]string, error) {
	if n, ok := m.nodes[nodeID]; ok {
		return n.GroupIDs, nil
	}
	return nil, ErrNotFound
}

func (m *memStore) CountNodes(_ context.Context) (int64, error) {
	return int64(len(m.nodes)), nil
}

// auditSpy captures audit events.
type auditSpy struct{ events []audit.Event }

func (a *auditSpy) Record(_ context.Context, e audit.Event) error {
	a.events = append(a.events, e)
	return nil
}

func (a *auditSpy) actions() []string {
	out := []string{}
	for _, e := range a.events {
		out = append(out, e.Action)
	}
	return out
}

func newSvc() (*Service, *memStore, *auditSpy) {
	store := newMemStore()
	spy := &auditSpy{}
	return NewService(store, spy), store, spy
}

func TestCreateNodeValidation(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := context.Background()

	cases := []struct {
		name string
		in   CreateNodeInput
		want error
	}{
		{"boş ad", CreateNodeInput{DisplayName: ""}, ErrInvalidDisplayName},
		{"uzun ad", CreateNodeInput{DisplayName: strings.Repeat("x", 129)}, ErrInvalidDisplayName},
		{"geçersiz os", CreateNodeInput{DisplayName: "n", OSFamily: "plan9"}, ErrInvalidOSFamily},
		{"geçersiz hostname", CreateNodeInput{DisplayName: "n", Hostname: strPtr("bad_host!")}, ErrInvalidHostname},
		{"geçersiz etiket", CreateNodeInput{DisplayName: "n", Tags: []string{"BIG TAG"}}, ErrInvalidTag},
		{"çok fazla etiket", CreateNodeInput{DisplayName: "n", Tags: repeatTag(33)}, ErrTooManyTags},
		{"geçersiz grup", CreateNodeInput{DisplayName: "n", GroupIDs: []string{"x"}}, ErrInvalidUUID},
	}
	for _, tc := range cases {
		if _, err := svc.CreateNode(ctx, "admin", "c-1", tc.in); err != tc.want {
			t.Fatalf("%s: %v bekleniyordu, %v geldi", tc.name, tc.want, err)
		}
	}
}

func strPtr(s string) *string { return &s }

func repeatTag(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "t" + string(rune('a'+i%26)) + itoa(i)
	}
	return out
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

func TestCreateNodeAuditsAndDefaults(t *testing.T) {
	svc, store, spy := newSvc()
	ctx := context.Background()

	n, err := svc.CreateNode(ctx, "admin", "corr-1", CreateNodeInput{
		DisplayName: "web-01",
		Hostname:    strPtr("web01.example.internal"),
		OSFamily:    OSLinux,
		Tags:        []string{"prod", "web", "prod"},
	})
	if err != nil {
		t.Fatalf("node oluşturulamadı: %v", err)
	}
	if n.Status != StatusPending {
		t.Fatalf("başlangıç durumu pending olmalı: %s", n.Status)
	}
	if len(n.Tags) != 2 {
		t.Fatalf("etiketler dedupe edilmeli: %v", n.Tags)
	}
	if len(spy.actions()) != 1 || spy.actions()[0] != "node.create" {
		t.Fatalf("node.create audit bekleniyordu: %v", spy.actions())
	}
	if spy.events[0].CorrelationID != "corr-1" {
		t.Fatal("correlation ID audit'e taşınmalı")
	}
	if store.nodes[n.ID] == nil {
		t.Fatal("node store'a yazılmalı")
	}
}

func TestUpdateNodeAndTags(t *testing.T) {
	svc, _, spy := newSvc()
	ctx := context.Background()
	n, _ := svc.CreateNode(ctx, "admin", "c", CreateNodeInput{DisplayName: "db-01", OSFamily: OSLinux})

	status := StatusActive
	updated, err := svc.UpdateNode(ctx, "admin", "c", n.ID, UpdateNodeInput{Status: &status})
	if err != nil {
		t.Fatalf("güncellenemedi: %v", err)
	}
	if updated.Status != StatusActive {
		t.Fatal("durum güncellenmeli")
	}

	if _, err := svc.SetTags(ctx, "admin", "c", n.ID, []string{"db", "üretim!"}); err != ErrInvalidTag {
		t.Fatalf("geçersiz etiket reddedilmeli: %v", err)
	}
	if _, err := svc.SetTags(ctx, "admin", "c", n.ID, []string{"db", "prod"}); err != nil {
		t.Fatalf("etiket yazılamadı: %v", err)
	}
	bad := "11111111-1111-1111-1111-999999999999"
	if _, err := svc.UpdateNode(ctx, "admin", "c", bad, UpdateNodeInput{}); err != ErrNotFound {
		t.Fatalf("olmayan node: %v", err)
	}
	found := false
	for _, a := range spy.actions() {
		if a == "node.tag" {
			found = true
		}
	}
	if !found {
		t.Fatal("node.tag audit bekleniyordu")
	}
}

func TestFactsLimits(t *testing.T) {
	svc, _, spy := newSvc()
	ctx := context.Background()
	n, _ := svc.CreateNode(ctx, "admin", "c", CreateNodeInput{DisplayName: "n", OSFamily: OSLinux})

	if err := svc.SetFacts(ctx, "admin", "c", n.ID, nil); err != ErrFactsNotObject {
		t.Fatalf("nil facts reddedilmeli: %v", err)
	}
	big := map[string]any{"blob": strings.Repeat("x", 70_000)}
	if err := svc.SetFacts(ctx, "admin", "c", n.ID, big); err != ErrFactsTooLarge {
		t.Fatalf("büyük facts reddedilmeli: %v", err)
	}
	if err := svc.SetFacts(ctx, "admin", "c", n.ID, map[string]any{"kernel": "6.12", "distro": "debian"}); err != nil {
		t.Fatalf("facts yazılamadı: %v", err)
	}
	if err := svc.SetFacts(ctx, "admin", "c", "geçersiz", map[string]any{}); err != ErrInvalidUUID {
		t.Fatalf("geçersiz uuid reddedilmeli: %v", err)
	}
	found := false
	for _, a := range spy.actions() {
		if a == "node.facts" {
			found = true
		}
	}
	if !found {
		t.Fatal("node.facts audit bekleniyordu")
	}
}

func TestCapabilityUpsertAndList(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := context.Background()
	n, _ := svc.CreateNode(ctx, "admin", "c", CreateNodeInput{DisplayName: "n", OSFamily: OSLinux})

	reason := "systemd yok"
	cap := &Capability{
		NodeID:            n.ID,
		CapabilityID:      "linux.systemd.service.restart",
		Version:           "1.0.0",
		Available:         false,
		ReasonUnavailable: &reason,
	}
	if err := svc.UpsertCapability(ctx, "admin", "c", cap); err != nil {
		t.Fatalf("capability yazılamadı: %v", err)
	}
	cap2 := &Capability{NodeID: n.ID, CapabilityID: "linux.systemd.service.restart", Version: "1.0.1", Available: true}
	if err := svc.UpsertCapability(ctx, "admin", "c", cap2); err != nil {
		t.Fatalf("capability güncellenemedi: %v", err)
	}
	list, err := svc.ListCapabilities(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Version != "1.0.1" || !list[0].Available {
		t.Fatalf("upsert bekleniyordu: %+v", list)
	}
	if err := svc.UpsertCapability(ctx, "admin", "c", &Capability{NodeID: n.ID, CapabilityID: "BAD ID"}); err == nil {
		t.Fatal("geçersiz capability kimliği reddedilmeli")
	}
}

func TestGroupsFlow(t *testing.T) {
	svc, _, spy := newSvc()
	ctx := context.Background()
	n, _ := svc.CreateNode(ctx, "admin", "c", CreateNodeInput{DisplayName: "n", OSFamily: OSLinux})

	g1, err := svc.CreateGroup(ctx, "admin", "c", "prod-tr", "Üretim TR")
	if err != nil {
		t.Fatalf("grup oluşturulamadı: %v", err)
	}
	if _, err := svc.CreateGroup(ctx, "admin", "c", "GEÇERSİZ AD", ""); err == nil {
		t.Fatal("geçersiz grup adı reddedilmeli")
	}
	updated, err := svc.AssignGroup(ctx, "admin", "c", n.ID, []string{g1.ID})
	if err != nil {
		t.Fatalf("grup ataması: %v", err)
	}
	if len(updated.GroupIDs) != 1 || updated.GroupIDs[0] != g1.ID {
		t.Fatalf("grup üyeliği bekleniyordu: %v", updated.GroupIDs)
	}
	// Olmayan grup reddedilir.
	if _, err := svc.AssignGroup(ctx, "admin", "c", n.ID, []string{"33333333-3333-3333-3333-333333333333"}); err != ErrNotFound {
		t.Fatalf("olmayan grup reddedilmeli: %v", err)
	}
	found := false
	for _, a := range spy.actions() {
		if a == "node.group.assign" {
			found = true
		}
	}
	if !found {
		t.Fatal("node.group.assign audit bekleniyordu")
	}
}

func TestListFilterValidation(t *testing.T) {
	svc, _, _ := newSvc()
	if _, err := svc.ListNodes(context.Background(), ListFilter{Status: "hatalı"}); err != ErrInvalidStatus {
		t.Fatalf("geçersiz durum filtresi reddedilmeli: %v", err)
	}
	if _, err := svc.ListNodes(context.Background(), ListFilter{Tag: "OK!"}); err != ErrInvalidTag {
		t.Fatalf("geçersiz etiket filtresi reddedilmeli: %v", err)
	}
	page, err := svc.ListNodes(context.Background(), ListFilter{Limit: 500})
	if err != nil {
		t.Fatalf("limit sınırlanmalı, hata değil: %v", err)
	}
	_ = page
}

func TestCursorRoundTrip(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	enc := encodeNodeCursor(ts, "abc")
	gotTS, gotID, err := decodeNodeCursor(enc)
	if err != nil || gotID != "abc" || !gotTS.Equal(ts) {
		t.Fatalf("cursor round-trip: %v %v %v", gotTS, gotID, err)
	}
	if _, _, err := decodeNodeCursor("!!!"); err == nil {
		t.Fatal("geçersiz cursor reddedilmeli")
	}
}
