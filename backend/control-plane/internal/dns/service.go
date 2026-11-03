package dns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/secrets"
)

// Service is the Faz 09 DNS operation surface used by task executors and
// REST handlers. Credentials are sealed at rest (AES-256-GCM) and ONLY
// decrypted inside buildProvider for executor use — no other path sees
// plaintext (criterion 5).
type Service struct {
	Store *Store
	Key   secrets.Key // zero value → ErrKeyMissing at provider build
}

// ErrKeyMissing marks the unconfigured-key state honestly.
var ErrKeyMissing = errors.New("dns: şifreleme anahtarı yapılandırılmadı (CP_SECRETS_KEY)")

// ErrKindUnknown marks a provider kind outside the closed supported set.
var ErrKindUnknown = errors.New("dns: sağlayıcı türü desteklenmiyor (desteklenen: mock, cloudflare)")

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidProviderID checks the UUID shape of provider references.
func ValidProviderID(id string) bool { return uuidRE.MatchString(id) }

// CreateProvider seals credentials and registers the provider.
// credentials must be the JSON credential object for the kind.
func (s *Service) CreateProvider(ctx context.Context, name, kind string, credentials json.RawMessage) (ProviderRow, error) {
	if name == "" || len(name) > 100 {
		return ProviderRow{}, errors.New("dns: sağlayıcı adı 1..100 karakter olmalı")
	}
	var caps Capabilities
	var credKeys []string
	switch kind {
	case ProviderKindMock:
		if _, err := BuildMock(credentials); err != nil {
			return ProviderRow{}, err
		}
		caps = NewMock().Capabilities()
		credKeys = []string{"api_key"}
	case ProviderKindCloudflare:
		// Faz 16: gerçek sağlayıcı — kimlik bilgisi burada DOĞRULANIR
		// (BuildCloudflare, API çağrısı yok) ve yalnız mühürlü saklanır.
		p, err := BuildCloudflare(credentials)
		if err != nil {
			return ProviderRow{}, err
		}
		caps = p.Capabilities()
		credKeys = []string{"api_token"}
	default:
		// Dürüst kapsam: Route53 vb. bilinmeyen tür tahminle açılmaz.
		return ProviderRow{}, fmt.Errorf("%w: %q", ErrKindUnknown, kind)
	}
	if !s.hasKey() {
		return ProviderRow{}, ErrKeyMissing
	}
	sealed, err := s.Key.Encrypt(credentials)
	if err != nil {
		return ProviderRow{}, fmt.Errorf("dns: kimlik bilgisi şifrelenemedi: %w", err)
	}
	return s.Store.Create(ctx, name, kind, caps, sealed, credKeys)
}

// DeleteProvider removes a registration (credential blob dies with it).
func (s *Service) DeleteProvider(ctx context.Context, id string) error {
	return s.Store.Delete(ctx, id)
}

// ListProviders returns registrations WITHOUT credential material.
func (s *Service) ListProviders(ctx context.Context) ([]ProviderRow, error) {
	return s.Store.List(ctx)
}

// buildProvider decrypts credentials and instantiates the provider client.
// Executor-only path.
func (s *Service) buildProvider(ctx context.Context, id string) (Provider, error) {
	if !s.hasKey() {
		return nil, ErrKeyMissing
	}
	row, err := s.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	sealed, err := s.Store.GetEncryptedCredentials(ctx, id)
	if err != nil {
		return nil, err
	}
	plain, err := s.Key.Decrypt(sealed)
	if err != nil {
		return nil, fmt.Errorf("dns: kimlik bilgisi çözülemedi: %w", err)
	}
	switch row.Kind {
	case ProviderKindMock:
		return BuildMock(plain)
	case ProviderKindCloudflare:
		return BuildCloudflare(plain)
	default:
		return nil, fmt.Errorf("%w: %q", ErrKindUnknown, row.Kind)
	}
}

func (s *Service) hasKey() bool {
	var zero secrets.Key
	return s.Key != zero
}

// Zones lists the provider's hosted zones.
func (s *Service) Zones(ctx context.Context, providerID string) ([]string, Capabilities, error) {
	p, err := s.buildProvider(ctx, providerID)
	if err != nil {
		return nil, Capabilities{}, err
	}
	zs, err := p.Zones(ctx)
	if err != nil {
		return nil, Capabilities{}, err
	}
	if len(zs) > MaxZones {
		zs = zs[:MaxZones]
	}
	return zs, p.Capabilities(), nil
}

// Records lists records of one zone.
func (s *Service) Records(ctx context.Context, providerID, zone string, limit int) ([]Record, Capabilities, error) {
	if !ValidZoneName(zone) {
		return nil, Capabilities{}, fmt.Errorf("%w: %q", ErrInvalidZone, zone)
	}
	p, err := s.buildProvider(ctx, providerID)
	if err != nil {
		return nil, Capabilities{}, err
	}
	rs, err := p.Records(ctx, zone, limit)
	if err != nil {
		return nil, Capabilities{}, err
	}
	return rs, p.Capabilities(), nil
}

// Diff computes the change preview for a proposed record against live
// state (criterion 2: diff preview BEFORE apply).
func (s *Service) Diff(ctx context.Context, providerID, zone string, proposed Record) (Change, Capabilities, error) {
	caps, existing, err := s.recordsForDiff(ctx, providerID, zone, proposed)
	if err != nil {
		return Change{}, Capabilities{}, err
	}
	ch, err := Diff(existing, proposed)
	return ch, caps, err
}

func (s *Service) recordsForDiff(ctx context.Context, providerID, zone string, proposed Record) (Capabilities, []Record, error) {
	if !ValidZoneName(zone) {
		return Capabilities{}, nil, fmt.Errorf("%w: %q", ErrInvalidZone, zone)
	}
	p, err := s.buildProvider(ctx, providerID)
	if err != nil {
		return Capabilities{}, nil, err
	}
	caps := p.Capabilities()
	if err := ValidateRecord(caps, proposed); err != nil {
		return caps, nil, err
	}
	existing, err := p.Records(ctx, zone, MaxRecords)
	if err != nil {
		return caps, nil, err
	}
	return caps, existing, nil
}

// Apply validates and applies ONE change; the returned Change includes the
// before/after snapshot for the task result (audit evidence).
func (s *Service) Apply(ctx context.Context, providerID, zone string, proposed Record) (Change, error) {
	caps, existing, err := s.recordsForDiff(ctx, providerID, zone, proposed)
	if err != nil {
		return Change{}, err
	}
	ch, err := Diff(existing, proposed)
	if err != nil {
		return Change{}, err
	}
	if err := ValidateRecord(caps, proposed); err != nil {
		return Change{}, err
	}
	p, err := s.buildProvider(ctx, providerID)
	if err != nil {
		return Change{}, err
	}
	var before *Record
	if ch.Before != nil {
		b := *ch.Before
		before = &b
	}
	after := proposed
	res, err := p.ApplyChange(ctx, zone, Change{Action: ch.Action, Before: before, After: &after})
	if err != nil {
		return Change{}, err
	}
	return Change{Action: ch.Action, Before: before, After: &res}, nil
}

// DeleteRecord removes one record (typed by name+type).
func (s *Service) DeleteRecord(ctx context.Context, providerID, zone, name, typ string) error {
	if !ValidZoneName(zone) {
		return fmt.Errorf("%w: %q", ErrInvalidZone, zone)
	}
	if !ValidRecordName(name, true) {
		return fmt.Errorf("%w: %q", ErrInvalidRecord, name)
	}
	okType := false
	for _, t := range RecordTypes {
		if t == typ {
			okType = true
			break
		}
	}
	if !okType {
		return fmt.Errorf("%w: %q", ErrTypeUnsupport, typ)
	}
	p, err := s.buildProvider(ctx, providerID)
	if err != nil {
		return err
	}
	existing, err := p.Records(ctx, zone, MaxRecords)
	if err != nil {
		return err
	}
	var before *Record
	for i := range existing {
		if existing[i].Name == name && existing[i].Type == typ {
			b := existing[i]
			before = &b
			break
		}
	}
	if before == nil {
		return fmt.Errorf("%w: %s/%s", ErrNotFound, name, typ)
	}
	_, err = p.ApplyChange(ctx, zone, Change{Action: "delete", Before: before})
	return err
}

// SetTXTRecord publishes one TXT record (DNS-01 bridge for acmex).
// Zone is auto-resolved: longest provider zone that suffix-matches domain.
func (s *Service) SetTXTRecord(ctx context.Context, providerID, domain, value string) error {
	p, err := s.buildProvider(ctx, providerID)
	if err != nil {
		return err
	}
	return s.setTXTOn(p, ctx, domain, value)
}

// setTXTOn is the provider-facing TXT upsert (separated for tests).
func (s *Service) setTXTOn(p Provider, ctx context.Context, domain, value string) error {
	if strings.TrimSpace(domain) == "" || len(domain) > MaxNameLen {
		return fmt.Errorf("%w: domain %q", ErrInvalidRecord, domain)
	}
	zs, err := p.Zones(ctx)
	if err != nil {
		return err
	}
	zone, name := resolveZone(zs, domain)
	if zone == "" {
		return fmt.Errorf("%w: %q sağlayıcı bölgeleriyle eşleşmiyor", ErrInvalidZone, domain)
	}
	rec := Record{Name: name, Type: "TXT", Value: value, TTL: 60}
	existing, err := p.Records(ctx, zone, MaxRecords)
	if err != nil {
		return err
	}
	action := "create"
	var before *Record
	for i := range existing {
		if existing[i].Name == name && existing[i].Type == "TXT" {
			b := existing[i]
			before = &b
			action = "update"
			break
		}
	}
	_, err = p.ApplyChange(ctx, zone, Change{Action: action, Before: before, After: &rec})
	return err
}

// resolveZone picks the longest suffix-matching zone and the relative
// record name ("@" at apex).
func resolveZone(zones []string, domain string) (zone, name string) {
	best := ""
	for _, z := range zones {
		if domain == z || strings.HasSuffix(domain, "."+z) {
			if len(z) > len(best) {
				best = z
			}
		}
	}
	if best == "" {
		return "", ""
	}
	if domain == best {
		return best, "_acme-challenge"
	}
	rel := strings.TrimSuffix(domain, "."+best)
	return best, "_acme-challenge." + rel
}
