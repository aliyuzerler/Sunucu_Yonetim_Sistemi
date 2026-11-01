package tasks

// ---- Faz 09: Domain/DNS + SSL sertifika yaşam döngüsü ----
//
// İki yürütme yüzeyi vardır:
//   - Control-plane hedefli DNS/cert operasyonları: worker'da Exec ile
//     koşar (sağlayıcı kimlik bilgileri ve ACME hesabı control-plane'dedir;
//     node'lar asla sağlayıcı anahtarı görmez — ADR-0013).
//   - Node hedefli web.cert.* operasyonları: broker'da --web-enable ile
//     koşar (TLS özel anahtarı yalnız node'da doğar; CSR public materyal
//     olarak task sonucuna döner).
//
// Doğrulama AYNA disiplinindedir (ops_faz06/07/08 gibi): payload şemaları
// agent tarafındaki tlsops ile birebir tutulur, iki taraf da strict doğrular.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/certmgmt"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/dns"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
)

// Faz09Services carries the executor dependencies. Nil services are legal
// (registry must build even when the feature is not wired); the executors
// then fail with a typed, honest configuration error.
type Faz09Services struct {
	DNS  *dns.Service
	Cert *certmgmt.Service
}

// ---- Payload şemaları (mirror of tlsops) ----

type dnsProviderPayload struct {
	ProviderID string `json:"provider_id"`
}

type dnsZonePayload struct {
	ProviderID string `json:"provider_id"`
	Zone       string `json:"zone"`
}

type dnsRecordPayload struct {
	ProviderID string `json:"provider_id"`
	Zone       string `json:"zone"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Value      string `json:"value"`
	TTL        int    `json:"ttl"`
}

type dnsRecordDeletePayload struct {
	ProviderID string `json:"provider_id"`
	Zone       string `json:"zone"`
	Name       string `json:"name"`
	Type       string `json:"type"`
}

type certIssuePayload struct {
	CertID     string   `json:"cert_id,omitempty"`
	Name       string   `json:"name"`
	SANs       []string `json:"sans,omitempty"`
	ProviderID string   `json:"provider_id"`
	NodeID     string   `json:"node_id,omitempty"`
	CSRPEM     string   `json:"csr_pem"`
}

type certIDPayload struct {
	CertID string `json:"cert_id"`
	Reason int    `json:"reason,omitempty"`
}

type webCertRequestPayload struct {
	Domain string   `json:"domain"`
	SANs   []string `json:"sans,omitempty"`
}

type webCertInstallPayload struct {
	Domain   string `json:"domain"`
	CertPEM  string `json:"cert_pem"`
	ChainPEM string `json:"chain_pem,omitempty"`
}

// Limits (mirror of dns package bounds + tlsops).
const (
	MaxDNSPayloadBytes   = 8 << 10
	MaxCertPayloadBytes  = 24 << 10
	MaxCSRPEMLen         = 8 << 10
	MaxCertPEMLen        = 16 << 10
	MaxDomainsPerRequest = 100
)

// validDNSDomain mirrors tlsops/ssl domain label rules (lowercase FQDN).
func validDNSDomain(d string) bool {
	if d == "" || len(d) > 253 || strings.Contains(d, "..") {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for i, r := range label {
			ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' ||
				(r == '-' && i > 0 && i < len(label)-1)
			if !ok {
				return false
			}
		}
	}
	return true
}

// ValidateDNSProviderID enforces {"provider_id": uuid}.
func ValidateDNSProviderID(payload json.RawMessage) error {
	var p dnsProviderPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !dns.ValidProviderID(p.ProviderID) {
		return errors.New("provider_id UUID olmalı")
	}
	return nil
}

// ValidateDNSZone enforces {provider_id, zone}.
func ValidateDNSZone(payload json.RawMessage) error {
	var p dnsZonePayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !dns.ValidProviderID(p.ProviderID) {
		return errors.New("provider_id UUID olmalı")
	}
	if !dns.ValidZoneName(p.Zone) {
		return errors.New("zone adı geçersiz")
	}
	return nil
}

// ValidateDNSRecord enforces {provider_id, zone, name, type, value, ttl}.
// TTL/type/value sağlayıcı yeteneği executor tarafında tekrar denetlenir
// (capability-declared bounds); burada biçimsel sınırlar denetlenir.
func ValidateDNSRecord(payload json.RawMessage) error {
	var p dnsRecordPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !dns.ValidProviderID(p.ProviderID) {
		return errors.New("provider_id UUID olmalı")
	}
	if !dns.ValidZoneName(p.Zone) {
		return errors.New("zone adı geçersiz")
	}
	rec := dns.Record{Name: p.Name, Type: p.Type, Value: p.Value, TTL: p.TTL}
	if !dns.ValidRecordName(rec.Name, true) {
		return fmt.Errorf("kayıt adı geçersiz: %q", rec.Name)
	}
	okType := false
	for _, t := range dns.RecordTypes {
		if t == rec.Type {
			okType = true
			break
		}
	}
	if !okType {
		return fmt.Errorf("kayıt türü desteklenmiyor: %q", rec.Type)
	}
	if err := dns.ValidateValue(rec.Type, rec.Value); err != nil {
		return err
	}
	if rec.TTL < 60 || rec.TTL > 86400 {
		return errors.New("ttl 60..86400 olmalı (sağlayıcı sınırı executor'da uygulanır)")
	}
	return nil
}

// ValidateDNSRecordDelete enforces {provider_id, zone, name, type}.
func ValidateDNSRecordDelete(payload json.RawMessage) error {
	var p dnsRecordDeletePayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !dns.ValidProviderID(p.ProviderID) {
		return errors.New("provider_id UUID olmalı")
	}
	if !dns.ValidZoneName(p.Zone) {
		return errors.New("zone adı geçersiz")
	}
	if !dns.ValidRecordName(p.Name, true) {
		return fmt.Errorf("kayıt adı geçersiz: %q", p.Name)
	}
	okType := false
	for _, t := range dns.RecordTypes {
		if t == p.Type {
			okType = true
			break
		}
	}
	if !okType {
		return fmt.Errorf("kayıt türü desteklenmiyor: %q", p.Type)
	}
	return nil
}

// ValidateCertIssue enforces the issue/renew request shape.
func ValidateCertIssue(payload json.RawMessage) error {
	if len(payload) > MaxCertPayloadBytes {
		return fmt.Errorf("payload %d bayt > %d sınırı", len(payload), MaxCertPayloadBytes)
	}
	var p certIssuePayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if p.CertID != "" && !uuidShape(p.CertID) {
		return errors.New("cert_id UUID olmalı")
	}
	if !validDNSDomain(p.Name) {
		return fmt.Errorf("birincil domain geçersiz: %q", p.Name)
	}
	seen := map[string]bool{p.Name: true}
	for _, s := range p.SANs {
		if !validDNSDomain(s) || seen[s] {
			return fmt.Errorf("SAN geçersiz/tekrarlı: %q", s)
		}
		seen[s] = true
	}
	if len(p.SANs) > MaxDomainsPerRequest {
		return errors.New("en fazla 100 SAN")
	}
	if p.ProviderID != "" && !dns.ValidProviderID(p.ProviderID) {
		return errors.New("provider_id UUID olmalı")
	}
	if p.NodeID != "" && !uuidShape(p.NodeID) {
		return errors.New("node_id UUID olmalı")
	}
	if p.CSRPEM == "" || len(p.CSRPEM) > MaxCSRPEMLen {
		return errors.New("csr_pem 1..8KiB olmalı")
	}
	if !strings.Contains(p.CSRPEM, "BEGIN CERTIFICATE REQUEST") {
		return errors.New("csr_pem CERTIFICATE REQUEST PEM olmalı")
	}
	return nil
}

// ValidateCertID enforces {cert_id[, reason]} (renew/revoke).
func ValidateCertID(payload json.RawMessage) error {
	var p certIDPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !uuidShape(p.CertID) {
		return errors.New("cert_id UUID olmalı")
	}
	if !certmgmt.ValidRevokeReason(p.Reason) {
		return errors.New("reason 0..10 olmalı")
	}
	return nil
}

// ValidateWebCertRequest enforces {domain, sans[]} (node op mirror).
func ValidateWebCertRequest(payload json.RawMessage) error {
	var p webCertRequestPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !validDNSDomain(p.Domain) {
		return fmt.Errorf("domain geçersiz: %q", p.Domain)
	}
	seen := map[string]bool{p.Domain: true}
	for _, s := range p.SANs {
		if !validDNSDomain(s) || seen[s] {
			return fmt.Errorf("SAN geçersiz/tekrarlı: %q", s)
		}
		seen[s] = true
	}
	if len(p.SANs) > MaxDomainsPerRequest {
		return errors.New("en fazla 100 SAN")
	}
	return nil
}

// ValidateWebCertInstall enforces {domain, cert_pem[, chain_pem]}.
func ValidateWebCertInstall(payload json.RawMessage) error {
	if len(payload) > MaxCertPayloadBytes {
		return fmt.Errorf("payload %d bayt > %d sınırı", len(payload), MaxCertPayloadBytes)
	}
	var p webCertInstallPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !validDNSDomain(p.Domain) {
		return fmt.Errorf("domain geçersiz: %q", p.Domain)
	}
	if p.CertPEM == "" || len(p.CertPEM) > MaxCertPEMLen {
		return errors.New("cert_pem 1..16KiB olmalı")
	}
	if !strings.Contains(p.CertPEM, "BEGIN CERTIFICATE") {
		return errors.New("cert_pem CERTIFICATE PEM olmalı")
	}
	if len(p.ChainPEM) > MaxCertPEMLen {
		return errors.New("chain_pem 16KiB sınırı aşıldı")
	}
	if p.ChainPEM != "" && !strings.Contains(p.ChainPEM, "BEGIN CERTIFICATE") {
		return errors.New("chain_pem CERTIFICATE PEM olmalı")
	}
	return nil
}

// ValidateWebCertStatus enforces {}.
func ValidateWebCertStatus(payload json.RawMessage) error {
	var p struct{}
	if len(payload) == 0 {
		return nil
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	return nil
}

// ---- Executors (control-plane hedefli) ----

// DNSZonesExecutor lists zones via the provider adapter.
type DNSZonesExecutor struct{ Svc *dns.Service }

func (e DNSZonesExecutor) Execute(ctx context.Context, t *Task) (map[string]any, string, error) {
	if e.Svc == nil {
		return nil, "", errors.New("dns servisi yapılandırılmadı")
	}
	var p dnsProviderPayload
	if err := decodeStrict(t.Payload, &p); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	zones, caps, err := e.Svc.Zones(ctx, p.ProviderID)
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"zones": zones, "capabilities": caps}, "", nil
}

// DNSRecordsExecutor lists records of one zone.
type DNSRecordsExecutor struct{ Svc *dns.Service }

func (e DNSRecordsExecutor) Execute(ctx context.Context, t *Task) (map[string]any, string, error) {
	if e.Svc == nil {
		return nil, "", errors.New("dns servisi yapılandırılmadı")
	}
	var p dnsZonePayload
	if err := decodeStrict(t.Payload, &p); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	records, caps, err := e.Svc.Records(ctx, p.ProviderID, p.Zone, dns.MaxRecords)
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"records": records, "capabilities": caps}, "", nil
}

// DNSDiffExecutor computes the change preview (kriter 2: diff preview).
type DNSDiffExecutor struct{ Svc *dns.Service }

func (e DNSDiffExecutor) Execute(ctx context.Context, t *Task) (map[string]any, string, error) {
	if e.Svc == nil {
		return nil, "", errors.New("dns servisi yapılandırılmadı")
	}
	var p dnsRecordPayload
	if err := decodeStrict(t.Payload, &p); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	proposed := dns.Record{Name: p.Name, Type: p.Type, Value: p.Value, TTL: p.TTL}
	ch, caps, err := e.Svc.Diff(ctx, p.ProviderID, p.Zone, proposed)
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"change": ch, "capabilities": caps}, "", nil
}

// DNSApplyExecutor applies the record change (audit: task kaydı + result
// before/after anlık görüntüsü).
type DNSApplyExecutor struct{ Svc *dns.Service }

func (e DNSApplyExecutor) Execute(ctx context.Context, t *Task) (map[string]any, string, error) {
	if e.Svc == nil {
		return nil, "", errors.New("dns servisi yapılandırılmadı")
	}
	var p dnsRecordPayload
	if err := decodeStrict(t.Payload, &p); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	proposed := dns.Record{Name: p.Name, Type: p.Type, Value: p.Value, TTL: p.TTL}
	ch, err := e.Svc.Apply(ctx, p.ProviderID, p.Zone, proposed)
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"change": ch}, "", nil
}

// DNSDeleteExecutor deletes one typed record.
type DNSDeleteExecutor struct{ Svc *dns.Service }

func (e DNSDeleteExecutor) Execute(ctx context.Context, t *Task) (map[string]any, string, error) {
	if e.Svc == nil {
		return nil, "", errors.New("dns servisi yapılandırılmadı")
	}
	var p dnsRecordDeletePayload
	if err := decodeStrict(t.Payload, &p); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	if err := e.Svc.DeleteRecord(ctx, p.ProviderID, p.Zone, p.Name, p.Type); err != nil {
		return nil, "", err
	}
	return map[string]any{"deleted": map[string]string{"name": p.Name, "type": p.Type}}, "", nil
}

// CertIssueExecutor runs the ACME issue/renew flow and persists state.
type CertIssueExecutor struct{ Svc *certmgmt.Service }

func (e CertIssueExecutor) Execute(ctx context.Context, t *Task) (map[string]any, string, error) {
	if e.Svc == nil {
		return nil, "", errors.New("sertifika servisi yapılandırılmadı")
	}
	var p certIssuePayload
	if err := decodeStrict(t.Payload, &p); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	out, err := e.Svc.Issue(ctx, certmgmt.IssueRequest{
		CertID: p.CertID, Name: p.Name, SANs: p.SANs,
		ProviderID: p.ProviderID, NodeID: p.NodeID, CSRPEM: p.CSRPEM,
	})
	if err != nil {
		return nil, "", err
	}
	// cert_pem/chain_pem PUBLIC materyaldir: web.cert.install zinciri için
	// sonuçta taşınır (özel anahtar asla — node'da doğar).
	return map[string]any{"certificate": out.Record, "cert_pem": out.CertPEM, "chain_pem": out.ChainPEM}, "", nil
}

// CertRenewExecutor renews from the stored CSR.
type CertRenewExecutor struct{ Svc *certmgmt.Service }

func (e CertRenewExecutor) Execute(ctx context.Context, t *Task) (map[string]any, string, error) {
	if e.Svc == nil {
		return nil, "", errors.New("sertifika servisi yapılandırılmadı")
	}
	var p certIDPayload
	if err := decodeStrict(t.Payload, &p); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	out, err := e.Svc.Renew(ctx, p.CertID)
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"certificate": out.Record, "cert_pem": out.CertPEM, "chain_pem": out.ChainPEM}, "", nil
}

// CertRevokeExecutor revokes via the account key path.
type CertRevokeExecutor struct{ Svc *certmgmt.Service }

func (e CertRevokeExecutor) Execute(ctx context.Context, t *Task) (map[string]any, string, error) {
	if e.Svc == nil {
		return nil, "", errors.New("sertifika servisi yapılandırılmadı")
	}
	var p certIDPayload
	if err := decodeStrict(t.Payload, &p); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	rec, err := e.Svc.Revoke(ctx, p.CertID, p.Reason)
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"certificate": rec}, "", nil
}

// uuidShape checks the canonical UUID form (service.uuidRE).
func uuidShape(s string) bool { return uuidRE.MatchString(s) }

// buildFaz09Registry returns the Faz 09 operation specs (11 op).
func buildFaz09Registry(svc Faz09Services) []*OperationSpec {
	return []*OperationSpec{
		// ---- DNS (control-plane yürütmesi; sağlayıcı kimlik bilgileri
		// yalnız buradadır) ----
		{
			Name: "dns.zone.list", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetControlPlane, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateDNSProviderID,
			Exec:         DNSZonesExecutor{Svc: svc.DNS},
			RequiredPerm: rbac.PermDNSRead,
		},
		{
			Name: "dns.record.list", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetControlPlane, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateDNSZone,
			Exec:         DNSRecordsExecutor{Svc: svc.DNS},
			RequiredPerm: rbac.PermDNSRead,
		},
		{
			Name: "dns.record.diff", Version: "1.0.0",
			// Kriter 2: apply ÖNCE diff önizleme (salt-okuma).
			Class: ClassReadOnly, Target: TargetControlPlane, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: MaxDNSPayloadBytes,
			Validate:     ValidateDNSRecord,
			Exec:         DNSDiffExecutor{Svc: svc.DNS},
			RequiredPerm: rbac.PermDNSRead,
		},
		{
			Name: "dns.record.apply", Version: "1.0.0",
			// Upsert: aynı name+type'a ikinci apply yakınsar (idempotent).
			// TTL sağlayıcı capability sınırlarıyla executor'da yeniden
			// denetlenir; sonuç before/after anlık görüntüsü taşır (audit).
			Class: ClassIdempotent, Target: TargetControlPlane, RetrySafe: true,
			Timeout: 2 * time.Minute, MaxPayload: MaxDNSPayloadBytes,
			Validate:     ValidateDNSRecord,
			Exec:         DNSApplyExecutor{Svc: svc.DNS},
			RequiredPerm: rbac.PermDNSControl,
		},
		{
			Name: "dns.record.delete", Version: "1.0.0",
			// non_idempotent: ikinci delete "bulunamadı" ile biter.
			Class: ClassNonIdem, Target: TargetControlPlane, RetrySafe: false,
			Timeout: 2 * time.Minute, MaxPayload: 1 << 10,
			Validate:     ValidateDNSRecordDelete,
			Exec:         DNSDeleteExecutor{Svc: svc.DNS},
			RequiredPerm: rbac.PermDNSControl,
		},

		// ---- SSL sertifikaları (control-plane ACME; DNS-01) ----
		{
			Name: "cert.issue", Version: "1.0.0",
			// Yeni üretim: cert_id boş. CSR node'dan gelir (özel anahtar
			// node'da doğar — ADR-0013). Rate limit/challenge hataları
			// kayıt üzerinde sınıflandırılmış olarak saklanır.
			Class: ClassNonIdem, Target: TargetControlPlane, RetrySafe: false,
			Timeout: 5 * time.Minute, MaxPayload: MaxCertPayloadBytes,
			Validate:     ValidateCertIssue,
			Exec:         CertIssueExecutor{Svc: svc.Cert},
			RequiredPerm: rbac.PermCertCtrl,
		},
		{
			Name: "cert.renew", Version: "1.0.0",
			// Saklanan CSR ile yenileme; başarısızlık canlı sertifikayı
			// bozmaz (kriter 4: yalnız yeni materyal MarkIssued ile yazılır).
			Class: ClassNonIdem, Target: TargetControlPlane, RetrySafe: false,
			Timeout: 5 * time.Minute, MaxPayload: 1 << 10,
			Validate:     ValidateCertID,
			Exec:         CertRenewExecutor{Svc: svc.Cert},
			RequiredPerm: rbac.PermCertCtrl,
		},
		{
			Name: "cert.revoke", Version: "1.0.0",
			// Hesap anahtarıyla iptal (RFC 8555 §7.6); node anahtarı gerekmez.
			Class: ClassNonIdem, Target: TargetControlPlane, RetrySafe: false,
			Timeout: 2 * time.Minute, MaxPayload: 1 << 10,
			Validate:     ValidateCertID,
			Exec:         CertRevokeExecutor{Svc: svc.Cert},
			RequiredPerm: rbac.PermCertCtrl,
		},

		// ---- Node tarafı sertifika işlemleri (broker, --web-enable) ----
		{
			Name: "web.cert.request", Version: "1.0.0",
			// Anahtar+CSR üretimi (özel anahtar diskte 0600 kalır, CSR döner).
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 4 << 10,
			Validate:     ValidateWebCertRequest,
			RequiredPerm: rbac.PermWebControl,
		},
		{
			Name: "web.cert.install", Version: "1.0.0",
			// Yalnız public materyal: doğrulama (parse/pair/expiry) → stage
			// → nginx -t kapısı → atomik takas → reload; başarısızlıkta
			// rollback (canlı sertifika korunur — kriter 4).
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 2 * time.Minute, MaxPayload: MaxCertPayloadBytes,
			Validate:     ValidateWebCertInstall,
			RequiredPerm: rbac.PermWebControl,
		},
		{
			Name: "web.cert.status", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateWebCertStatus,
			RequiredPerm: rbac.PermWebRead,
		},
	}
}
