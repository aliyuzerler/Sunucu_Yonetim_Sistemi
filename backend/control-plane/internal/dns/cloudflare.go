package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Cloudflare provider — Faz 16. Cloudflare API v4 REST client, YALNIZ
// stdlib (net/http + encoding/json). Provider sözleşmesi ADR-0013'te
// sabittir: CloudflareProvider onu birebir uygular — mock ile aynı
// doğrulama katmanı (ValidateRecord), aynı diff önizleme, aynı executor
// yolları (dns.zone.list / dns.record.* / ACME DNS-01 köprüsü).
//
// Sınırlar (dürüst kapsam, ADR-0021):
//   - Bölge (zone) OLUŞTURMA bu arayüzde yok (Provider.Zones salt liste);
//     Cloudflare hesabında bölge zaten mevcut olmalıdır. Alan adı satın
//     alma ve bölge açma panel dışıdır — panel mevcut bölgeleri yönetir.
//   - Kayıt türleri panelin KAPALI kümesiyle sınırlıdır (A, AAAA, CNAME,
//     TXT, MX). Cloudflare'in desteklediği NS/SOA/SRV/CAA gibi türler
//     listelerde FİLTRELENİR ve panel üzerinden yazılmaz (tahmin yok).
//   - Cloudflare "auto" TTL'i (API'de ttl=1) 60 olarak raporlanır ve
//     güncellemede 60 gönderilir (panel TTL'i sağlayıcı sınırıyla
//     doğrular; otomatik-TTL ayrımı taşımak arayüzü değiştirirdi).
//   - Yeniden deneme (retry) YOK: 4xx/5xx yanıtları tipli hatayla dürüst
//     biçimde döner; operatör görevi yeniden çalıştırır. Sessiz yeniden
//     deneme sorunları maskeler.
//
// Güvenlik: api_token YALNIZ Authorization başlığında taşınır; asla hata
// mesajına, loga veya sonuç payload'ına yazılmaz. Kimlik bilgisi dinlenme
// halinde AES-256-GCM mühürlüdür (Service.CreateProvider sözleşmesi).

// ProviderKindCloudflare is the real-provider kind ("cloudflare").
const ProviderKindCloudflare = "cloudflare"

// CloudflareCredentials is the credential shape accepted for kind
// "cloudflare". Token: Cloudflare "API Token" (User API Tokens), önerilen
// kapsam: hedef bölgelerde Zone.DNS Edit + Zone.Read.
type CloudflareCredentials struct {
	APIToken string `json:"api_token"`
}

// CloudflareProvider implements Provider against Cloudflare API v4.
// A fresh instance is built per executor call from sealed credentials
// (CI-26 dersi: istemci durumu çağrılar arası taşınmaz).
type CloudflareProvider struct {
	token   string
	baseURL string
	http    *http.Client
}

// Default Cloudflare API v4 endpoint.
const cloudflareDefaultBaseURL = "https://api.cloudflare.com/client/v4"

// Cloudflare pagination safety caps (honest limits: panelin kendi
// MaxZones/MaxRecords sınırlarının ötesine geçen sayfalama tavanı).
const (
	cloudflarePerPage     = 50
	cloudflareMaxPages    = 40 // 40 * 50 = 2000
	cloudflareHTTPMaxTTL  = 86400
	cloudflareHTTPMinTTL  = 60
	cloudflareBodyCapByte = 4 << 20
)

// CloudflareCapabilities: panelin kapalı tür kümesi + Cloudflare TTL
// aralığı. DNS-01 ve wildcard desteklenir.
func CloudflareCapabilities() Capabilities {
	return Capabilities{
		RecordTypes:   append([]string(nil), RecordTypes...),
		TTLMin:        cloudflareHTTPMinTTL,
		TTLMax:        cloudflareHTTPMaxTTL,
		SupportsDNS01: true,
		Wildcard:      true,
		Note:          "Cloudflare API v4 (api_token); bölgeler Cloudflare hesabında açılır, ttl=1 (auto) 60 olarak raporlanır; NS/SOA/SRV/CAA panel listelerinde gösterilmez",
	}
}

// Capabilities returns the declared capability set.
func (p *CloudflareProvider) Capabilities() Capabilities { return CloudflareCapabilities() }

// BuildCloudflare validates credentials and returns a provider client.
func BuildCloudflare(creds []byte) (*CloudflareProvider, error) {
	c, err := decodeCloudflareCreds(creds)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.APIToken) == "" {
		return nil, fmt.Errorf("%w: cloudflare için api_token gerekli (boş olamaz)", ErrInvalidRecord)
	}
	if len(c.APIToken) > 512 {
		return nil, fmt.Errorf("%w: api_token 512 karakteri aşamaz", ErrInvalidRecord)
	}
	return &CloudflareProvider{
		token:   c.APIToken,
		baseURL: cloudflareDefaultBaseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// ---- Cloudflare API v4 shapes (only what we consume) ----

type cfEnvelope struct {
	Success bool            `json:"success"`
	Errors  []cfErr         `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfRecord struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"` // Cloudflare FQDN döner ("*.sub" wildcard öneki dahil)
	Type     string `json:"type"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Priority *int   `json:"priority,omitempty"` // MX için ayrı alan
}

// cfAPI executes one API call and decodes the envelope. Typed errors carry
// HTTP status and Cloudflare error code/message — NEVER the token.
func (p *CloudflareProvider) cfAPI(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%w: istek gövdesi kodlanamadı: %v", ErrAPI, err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, rdr)
	if err != nil {
		return fmt.Errorf("%w: istek kurulamadı: %v", ErrAPI, err)
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: ağ hatası: %v", ErrAPI, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, cloudflareBodyCapByte))
	if err != nil {
		return fmt.Errorf("%w: yanıt okunamadı: %v", ErrAPI, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%w: HTTP %d: %s", ErrAPI, resp.StatusCode, cfErrMessage(raw))
	}
	var env cfEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%w: yanıt şeması uyumsuz: %v", ErrAPI, err)
	}
	if !env.Success {
		return fmt.Errorf("%w: %s", ErrAPI, cfErrMessage(raw))
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("%w: sonuç alanı çözülemedi: %v", ErrAPI, err)
		}
	}
	return nil
}

// cfErrMessage extracts the first Cloudflare error message from a raw
// response body (best effort; falls back to a trimmed body snippet).
func cfErrMessage(raw []byte) string {
	var env struct {
		Errors []cfErr `json:"errors"`
		Error  string  `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err == nil {
		for _, e := range env.Errors {
			if e.Message != "" {
				return fmt.Sprintf("cloudflare %d: %s", e.Code, e.Message)
			}
		}
		if env.Error != "" {
			return env.Error
		}
	}
	snippet := string(raw)
	if len(snippet) > 200 {
		snippet = snippet[:200]
	}
	return strings.TrimSpace(snippet)
}

// Zones lists hosted zones in the Cloudflare account (paginated).
func (p *CloudflareProvider) Zones(ctx context.Context) ([]string, error) {
	out := []string{}
	for page := 1; page <= cloudflareMaxPages; page++ {
		var zones []cfZone
		err := p.cfAPI(ctx, http.MethodGet,
			fmt.Sprintf("/zones?per_page=%d&page=%d", cloudflarePerPage, page), nil, &zones)
		if err != nil {
			return nil, err
		}
		for _, z := range zones {
			if ValidZoneName(z.Name) {
				out = append(out, z.Name)
			}
		}
		if len(zones) < cloudflarePerPage {
			break
		}
	}
	return out, nil
}

// zoneID resolves the Cloudflare zone id for an exact zone name.
func (p *CloudflareProvider) zoneID(ctx context.Context, zone string) (string, error) {
	var zones []cfZone
	err := p.cfAPI(ctx, http.MethodGet,
		"/zones?name="+zoneEscape(zone)+"&per_page=1", nil, &zones)
	if err != nil {
		return "", err
	}
	for _, z := range zones {
		if z.Name == zone {
			return z.ID, nil
		}
	}
	return "", fmt.Errorf("%w: %q (Cloudflare hesabında bulunamadı)", ErrInvalidZone, zone)
}

// Records lists the manageable records of one zone (closed type set).
func (p *CloudflareProvider) Records(ctx context.Context, zone string, limit int) ([]Record, error) {
	if limit < 1 || limit > MaxRecords {
		return nil, fmt.Errorf("%w: limit 1..%d olmalı", ErrLimit, MaxRecords)
	}
	zid, err := p.zoneID(ctx, zone)
	if err != nil {
		return nil, err
	}
	out := []Record{}
	for page := 1; page <= cloudflareMaxPages; page++ {
		var recs []cfRecord
		err := p.cfAPI(ctx, http.MethodGet,
			fmt.Sprintf("/zones/%s/dns_records?per_page=%d&page=%d", zid, cloudflarePerPage, page), nil, &recs)
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			if rec, ok := cfToRecord(r, zone); ok {
				out = append(out, rec)
			} // kapalı tür kümesi dışı (NS/SOA/...) — dürüst filtre
		}
		if len(recs) < cloudflarePerPage || len(out) >= MaxRecords {
			break
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// cfToRecord maps a Cloudflare record onto the panel model. ok=false for
// types outside the panel's closed set (they are neither listed nor
// written through the panel).
func cfToRecord(r cfRecord, zone string) (Record, bool) {
	switch r.Type {
	case "A", "AAAA", "TXT":
	case "CNAME":
		r.Content = strings.TrimSuffix(r.Content, ".")
	case "MX":
		if r.Priority != nil {
			r.Content = fmt.Sprintf("%d %s", *r.Priority, strings.TrimSuffix(r.Content, "."))
		}
	default:
		return Record{}, false
	}
	return Record{
		Name:  cfRelativeName(r.Name, zone),
		Type:  r.Type,
		Value: r.Content,
		TTL:   cfTTL(r.TTL),
	}, true
}

// cfRelativeName converts Cloudflare's FQDN to the zone-relative name
// ("@" at apex, wildcard "*.sub" prefix preserved).
func cfRelativeName(fqdn, zone string) string {
	if zone == "" {
		return fqdn
	}
	name := strings.TrimSuffix(fqdn, "."+zone)
	if name == fqdn && fqdn != zone {
		// Bölgeyle eşleşmeyen ad — birebir bırak (bozuk veri tahmini yok).
		return fqdn
	}
	if name == zone || name == "" {
		return "@"
	}
	return name
}

// cfTTL normalizes Cloudflare TTL: auto (ttl=1, genel 1..59) → 60.
func cfTTL(ttl int) int {
	if ttl < cloudflareHTTPMinTTL {
		return cloudflareHTTPMinTTL
	}
	if ttl > cloudflareHTTPMaxTTL {
		return cloudflareHTTPMaxTTL
	}
	return ttl
}

// cfRequest converts a validated panel record into the create/update
// payload (MX "priority target" değer formatı → ayrı priority alanı).
func cfRequest(r Record) (cfRecord, error) {
	out := cfRecord{Name: r.Name, Type: r.Type, Content: r.Value, TTL: r.TTL}
	if r.Type == "MX" {
		fields := strings.Fields(r.Value)
		if len(fields) != 2 {
			return cfRecord{}, fmt.Errorf("%w: MX 'öncelik hedef' biçiminde olmalı", ErrInvalidRecord)
		}
		prio, err := strconv.Atoi(fields[0])
		if err != nil {
			return cfRecord{}, fmt.Errorf("%w: MX önceliği çözülemedi", ErrInvalidRecord)
		}
		out.Priority = &prio
		out.Content = fields[1]
	}
	return out, nil
}

// ApplyChange applies ONE typed change against the live zone.
func (p *CloudflareProvider) ApplyChange(ctx context.Context, zone string, ch Change) (Record, error) {
	zid, err := p.zoneID(ctx, zone)
	if err != nil {
		return Record{}, err
	}
	switch ch.Action {
	case "create", "update":
		if ch.After == nil {
			return Record{}, fmt.Errorf("%w: %s After kaydı gerektirir", ErrInvalidRecord, ch.Action)
		}
		if err := ValidateRecord(p.Capabilities(), *ch.After); err != nil {
			return Record{}, err
		}
		body, err := cfRequest(*ch.After)
		if err != nil {
			return Record{}, err
		}
		var res cfRecord
		if ch.Action == "create" {
			if err := p.cfAPI(ctx, http.MethodPost, "/zones/"+zid+"/dns_records", body, &res); err != nil {
				return Record{}, err
			}
		} else {
			recID, err := p.findRecordID(ctx, zone, zid, ch.After.Name, ch.After.Type)
			if err != nil {
				return Record{}, err
			}
			if err := p.cfAPI(ctx, http.MethodPut, "/zones/"+zid+"/dns_records/"+recID, body, &res); err != nil {
				return Record{}, err
			}
		}
		rec, ok := cfToRecord(res, zone)
		if !ok {
			return Record{}, fmt.Errorf("%w: yanıt türü beklenmedik: %q", ErrAPI, res.Type)
		}
		return rec, nil
	case "delete":
		if ch.Before == nil {
			return Record{}, fmt.Errorf("%w: delete Before kaydı gerektirir", ErrInvalidRecord)
		}
		recID, err := p.findRecordID(ctx, zone, zid, ch.Before.Name, ch.Before.Type)
		if err != nil {
			return Record{}, err
		}
		if err := p.cfAPI(ctx, http.MethodDelete, "/zones/"+zid+"/dns_records/"+recID, nil, nil); err != nil {
			return Record{}, err
		}
		return Record{}, nil
	default:
		return Record{}, fmt.Errorf("%w: action %q", ErrInvalidRecord, ch.Action)
	}
}

// findRecordID locates the Cloudflare record id for a relative name+type.
func (p *CloudflareProvider) findRecordID(ctx context.Context, zone, zid, name, typ string) (string, error) {
	for page := 1; page <= cloudflareMaxPages; page++ {
		var recs []cfRecord
		err := p.cfAPI(ctx, http.MethodGet,
			fmt.Sprintf("/zones/%s/dns_records?per_page=%d&page=%d", zid, cloudflarePerPage, page), nil, &recs)
		if err != nil {
			return "", err
		}
		for _, r := range recs {
			if r.Type == typ && cfRelativeName(r.Name, zone) == name {
				return r.ID, nil
			}
		}
		if len(recs) < cloudflarePerPage {
			break
		}
	}
	return "", fmt.Errorf("%w: %s/%s", ErrNotFound, name, typ)
}

// zoneEscape makes a zone name safe as a query value (names are validated
// upstream; this is defense in depth for URL construction).
func zoneEscape(zone string) string {
	var b strings.Builder
	for _, r := range zone {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "%%%02X", r)
		}
	}
	return b.String()
}

// decodeCloudflareCreds is a strict decoder for Cloudflare credentials
// (no unknown fields — typos surface as errors, not silent no-ops).
func decodeCloudflareCreds(raw []byte) (CloudflareCredentials, error) {
	var c CloudflareCredentials
	if len(raw) == 0 {
		return c, fmt.Errorf("%w: kimlik bilgisi boş", ErrUnknownKind)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("%w: kimlik bilgisi şeması uyumsuz: %v", ErrUnknownKind, err)
	}
	return c, nil
}
