package dns

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// MockProvider is the built-in in-memory provider. It is the honest test
// and offline-evaluation backend: full CRUD, declared capabilities, DNS-01
// support (it stores TXT records like any provider would), deterministic
// error cases. Nothing leaves the process.
//
// Concurrency: mutex-guarded; executors may run in parallel.
type MockProvider struct {
	mu      sync.Mutex
	zones   map[string]map[recordKey]Record
	caps    Capabilities
	nextTag int
}

type recordKey struct{ name, typ string }

// MockCredentials is the credential shape accepted for kind "mock".
// The mock does not call anything external, but the credential contract is
// still enforced (non-empty api_key) so the encrypted-at-rest path is
// exercised end to end in tests.
type MockCredentials struct {
	APIKey string `json:"api_key"`
}

// NewMock builds a mock provider with the default capability set:
// TTL 60..86400, closed record-type matrix, DNS-01 + wildcard support.
func NewMock() *MockProvider {
	return &MockProvider{
		zones: map[string]map[recordKey]Record{},
		caps: Capabilities{
			RecordTypes:   append([]string(nil), RecordTypes...),
			TTLMin:        60,
			TTLMax:        86400,
			SupportsDNS01: true,
			Wildcard:      true,
			Note:          "yerleşik mock sağlayıcı (in-memory; yalnız test/offline değerlendirme)",
		},
	}
}

// SeedZone adds a hosted zone with a SOA-ish apex A record so list views
// have honest content. Test/e2e helper.
func (m *MockProvider) SeedZone(zone string) error {
	if !ValidZoneName(zone) {
		return fmt.Errorf("%w: %q", ErrInvalidZone, zone)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.zones[zone]; ok {
		return nil
	}
	m.zones[zone] = map[recordKey]Record{}
	return nil
}

// SeedRecord inserts a record directly (test bootstrap; bypasses Change).
func (m *MockProvider) SeedRecord(zone string, r Record) error {
	if !ValidZoneName(zone) {
		return fmt.Errorf("%w: %q", ErrInvalidZone, zone)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	records, ok := m.zones[zone]
	if !ok {
		return fmt.Errorf("%w: %q", ErrInvalidZone, zone)
	}
	records[recordKey{r.Name, r.Type}] = r
	return nil
}

// GetRecord returns one record (used by the fake ACME server to verify
// DNS-01 TXT propagation in integration tests).
func (m *MockProvider) GetRecord(zone, name, typ string) (Record, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	records, ok := m.zones[zone]
	if !ok {
		return Record{}, false
	}
	r, ok := records[recordKey{name, typ}]
	return r, ok
}

func (m *MockProvider) Capabilities() Capabilities { return m.caps }

func (m *MockProvider) Zones(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.zones))
	for z := range m.zones {
		out = append(out, z)
	}
	sort.Strings(out)
	return out, nil
}

func (m *MockProvider) Records(_ context.Context, zone string, limit int) ([]Record, error) {
	if limit < 1 || limit > MaxRecords {
		return nil, fmt.Errorf("%w: limit 1..%d olmalı", ErrLimit, MaxRecords)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	records, ok := m.zones[zone]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrInvalidZone, zone)
	}
	out := make([]Record, 0, len(records))
	for _, r := range records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Type < out[j].Type
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MockProvider) ApplyChange(_ context.Context, zone string, ch Change) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	records, ok := m.zones[zone]
	if !ok {
		return Record{}, fmt.Errorf("%w: %q", ErrInvalidZone, zone)
	}
	switch ch.Action {
	case "create", "update":
		after := *ch.After
		key := recordKey{after.Name, after.Type}
		if ch.Action == "create" {
			if _, exists := records[key]; exists {
				return Record{}, fmt.Errorf("%w: %s/%s zaten var (update kullanın)", ErrConflict, after.Name, after.Type)
			}
		}
		records[key] = after
		return after, nil
	case "delete":
		if ch.Before == nil {
			return Record{}, fmt.Errorf("%w: delete Before kaydı gerektirir", ErrInvalidRecord)
		}
		key := recordKey{ch.Before.Name, ch.Before.Type}
		if _, exists := records[key]; !exists {
			return Record{}, fmt.Errorf("%w: %s/%s", ErrNotFound, ch.Before.Name, ch.Before.Type)
		}
		delete(records, key)
		return Record{}, nil
	default:
		return Record{}, fmt.Errorf("%w: action %q", ErrInvalidRecord, ch.Action)
	}
}

// ProviderKind is the closed set of provider kinds this build supports.
const ProviderKindMock = "mock"

// BuildMock validates credentials and returns the mock provider.
func BuildMock(creds []byte) (*MockProvider, error) {
	c, err := decodeMockCreds(creds)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, fmt.Errorf("%w: mock için api_key gerekli (boş olamaz)", ErrInvalidRecord)
	}
	if len(c.APIKey) > 512 {
		return nil, fmt.Errorf("%w: api_key 512 karakteri aşamaz", ErrInvalidRecord)
	}
	return NewMock(), nil
}
