package dns

// CloudflareProvider hermetik süit: sahte Cloudflare API v4 sunucusu
// (httptest) ile — ağ erişimi YOK. Kapsam: sayfalama, FQDN→göreli ad
// eşlemesi (apex/wildcard), MX priority birleşimi, CNAME nokta temizliği,
// kapalı tür kümesi filtresi, auto-TTL normalizasyonu, create/update/delete
// yolları ve tipli hata yolları (403, success=false, şema uyumsuz,
// bölge yok, kayıt yok) + token sızma negatifi.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const testCFToken = "cf-TEST-token-0001"

// fakeCF is a minimal in-memory Cloudflare API v4.
type fakeCF struct {
	mu      sync.Mutex
	zones   map[string]string              // name -> id
	records map[string]map[string]cfRecord // zoneID -> id -> record
	seq     int
	paths   []string
}

func newFakeCF() *fakeCF {
	return &fakeCF{
		zones:   map[string]string{},
		records: map[string]map[string]cfRecord{},
	}
}

func (f *fakeCF) addZone(name, id string) {
	f.zones[name] = id
	f.records[id] = map[string]cfRecord{}
}

func (f *fakeCF) addRecord(zoneID, id string, r cfRecord) {
	r.ID = id
	f.records[zoneID][id] = r
}

// zoneName reverses the zone id (fake has no direct index).
func (f *fakeCF) zoneName(zid string) string {
	for n, id := range f.zones {
		if id == zid {
			return n
		}
	}
	return ""
}

// toFQDN mimics Cloudflare: panel gönderir görece adı ("@", "www",
// "*.app") — API tam adla saklar ve yanıtlarda FQDN döner.
func toFQDN(name, zone string) string {
	if name == "@" || name == "" {
		return zone
	}
	if name == zone || strings.HasSuffix(name, "."+zone) {
		return name
	}
	return name + "." + zone
}

func (f *fakeCF) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/zones", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.paths = append(f.paths, r.Method+" "+r.URL.RequestURI())
		if !f.authOK(r) {
			cfWrite(w, http.StatusForbidden, cfEnvelope{Success: false, Errors: []cfErr{{Code: 9103, Message: "kimlik doğrulama başarısız"}}})
			return
		}
		if name := r.URL.Query().Get("name"); name != "" {
			id, ok := f.zones[name]
			var out []cfZone
			if ok {
				out = []cfZone{{ID: id, Name: name}}
			} else {
				out = []cfZone{}
			}
			cfWrite(w, http.StatusOK, cfEnvelope{Success: true, Result: mustJSON(out)})
			return
		}
		page := atoiDefault(r.URL.Query().Get("page"), 1)
		all := []cfZone{}
		for n, id := range f.zones {
			all = append(all, cfZone{ID: id, Name: n})
		}
		start := (page - 1) * cloudflarePerPage
		end := start + cloudflarePerPage
		if start > len(all) {
			start = len(all)
		}
		if end > len(all) {
			end = len(all)
		}
		cfWrite(w, http.StatusOK, cfEnvelope{Success: true, Result: mustJSON(all[start:end])})
	})
	mux.HandleFunc("/zones/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.paths = append(f.paths, r.Method+" "+r.URL.RequestURI())
		if !f.authOK(r) {
			cfWrite(w, http.StatusForbidden, cfEnvelope{Success: false, Errors: []cfErr{{Code: 9103, Message: "kimlik doğrulama başarısız"}}})
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/zones/"), "/")
		if len(parts) < 2 || parts[1] != "dns_records" {
			cfWrite(w, http.StatusNotFound, cfEnvelope{Success: false})
			return
		}
		zid := parts[0]
		recs, ok := f.records[zid]
		if !ok {
			cfWrite(w, http.StatusNotFound, cfEnvelope{Success: false, Errors: []cfErr{{Code: 7003, Message: "bölge yok"}}})
			return
		}
		switch r.Method {
		case http.MethodGet:
			page := atoiDefault(r.URL.Query().Get("page"), 1)
			all := []cfRecord{}
			for _, rec := range recs {
				all = append(all, rec)
			}
			start := (page - 1) * cloudflarePerPage
			end := start + cloudflarePerPage
			if start > len(all) {
				start = len(all)
			}
			if end > len(all) {
				end = len(all)
			}
			cfWrite(w, http.StatusOK, cfEnvelope{Success: true, Result: mustJSON(all[start:end])})
		case http.MethodPost:
			var body cfRecord
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				cfWrite(w, http.StatusBadRequest, cfEnvelope{Success: false})
				return
			}
			f.seq++
			id := fmt.Sprintf("rec-%04d", f.seq)
			body.ID = id
			body.Name = toFQDN(body.Name, f.zoneName(zid))
			recs[id] = body
			cfWrite(w, http.StatusOK, cfEnvelope{Success: true, Result: mustJSON(body)})
		case http.MethodPut:
			id := parts[2]
			if _, ok := recs[id]; !ok {
				cfWrite(w, http.StatusNotFound, cfEnvelope{Success: false, Errors: []cfErr{{Code: 81044, Message: "kayıt yok"}}})
				return
			}
			var body cfRecord
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				cfWrite(w, http.StatusBadRequest, cfEnvelope{Success: false})
				return
			}
			body.ID = id
			body.Name = toFQDN(body.Name, f.zoneName(zid))
			recs[id] = body
			cfWrite(w, http.StatusOK, cfEnvelope{Success: true, Result: mustJSON(body)})
		case http.MethodDelete:
			id := parts[2]
			if _, ok := recs[id]; !ok {
				cfWrite(w, http.StatusNotFound, cfEnvelope{Success: false, Errors: []cfErr{{Code: 81044, Message: "kayıt yok"}}})
				return
			}
			delete(recs, id)
			cfWrite(w, http.StatusOK, cfEnvelope{Success: true, Result: mustJSON(map[string]string{"id": id})})
		default:
			cfWrite(w, http.StatusMethodNotAllowed, cfEnvelope{Success: false})
		}
	})
	return mux
}

func (f *fakeCF) authOK(r *http.Request) bool {
	return r.Header.Get("Authorization") == "Bearer "+testCFToken
}

func cfWrite(w http.ResponseWriter, status int, env cfEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(env)
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func atoiDefault(s string, def int) int {
	n := 0
	if s == "" {
		return def
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func newTestCFProvider(t *testing.T, f *fakeCF) *CloudflareProvider {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	creds, err := json.Marshal(CloudflareCredentials{APIToken: testCFToken})
	if err != nil {
		t.Fatal(err)
	}
	p, err := BuildCloudflare(creds)
	if err != nil {
		t.Fatalf("cloudflare üretimi: %v", err)
	}
	p.baseURL = srv.URL
	p.http = srv.Client()
	return p
}

func TestBuildCloudflareCreds(t *testing.T) {
	if _, err := BuildCloudflare([]byte(`{"api_token":"tok"}`)); err != nil {
		t.Fatalf("geçerli token reddedildi: %v", err)
	}
	if _, err := BuildCloudflare([]byte(`{"api_token":"  "}`)); err == nil {
		t.Fatal("boş token reddedilmeli")
	}
	if _, err := BuildCloudflare([]byte(`{"api_key":"tok"}`)); err == nil {
		t.Fatal("bilinmeyen alan reddedilmeli (sözleşme: api_token)")
	}
	long := strings.Repeat("x", 513)
	if _, err := BuildCloudflare([]byte(`{"api_token":"` + long + `"}`)); err == nil {
		t.Fatal("512 karakteri aşan token reddedilmeli")
	}
	if _, err := BuildCloudflare(nil); err == nil {
		t.Fatal("boş kimlik bilgisi reddedilmeli")
	}
}

func TestCloudflareCapabilities(t *testing.T) {
	caps := CloudflareCapabilities()
	if caps.TTLMin != 60 || caps.TTLMax != 86400 {
		t.Fatalf("TTL sınırları: %+v", caps)
	}
	if !caps.SupportsDNS01 || !caps.Wildcard {
		t.Fatalf("DNS-01/wildcard: %+v", caps)
	}
	if len(caps.RecordTypes) != len(RecordTypes) {
		t.Fatalf("tür kümesi: %+v", caps.RecordTypes)
	}
}

func TestCloudflareZonesPaginationAndFilter(t *testing.T) {
	f := newFakeCF()
	for i := 0; i < 52; i++ {
		f.addZone(fmt.Sprintf("zone%02d.example.com", i), fmt.Sprintf("zid%02d", i))
	}
	f.addZone("bad_name.example.com", "zid-bad") // geçersiz etiket — filtrelenmeli
	p := newTestCFProvider(t, f)

	zs, err := p.Zones(context.Background())
	if err != nil {
		t.Fatalf("bölgeler: %v", err)
	}
	if len(zs) != 52 {
		t.Fatalf("52 bölge beklenir, gelen: %d", len(zs))
	}
	// İki sayfa istenmiş olmalı (50 + 2).
	sayfa := 0
	for _, path := range f.paths {
		if strings.HasPrefix(path, "GET /zones?") && !strings.Contains(path, "name=") {
			sayfa++
		}
	}
	if sayfa != 2 {
		t.Fatalf("2 sayfa isteği beklenir, gelen: %d", sayfa)
	}
}

func TestCloudflareRecordsMapping(t *testing.T) {
	f := newFakeCF()
	f.addZone("example.com", "zid1")
	prio := 10
	f.addRecord("zid1", "r1", cfRecord{ID: "r1", Name: "example.com", Type: "A", Content: "1.2.3.4", TTL: 3600})
	f.addRecord("zid1", "r2", cfRecord{ID: "r2", Name: "www.example.com", Type: "A", Content: "1.2.3.5", TTL: 300})
	f.addRecord("zid1", "r3", cfRecord{ID: "r3", Name: "docs.example.com", Type: "CNAME", Content: "target.example.com.", TTL: 300})
	f.addRecord("zid1", "r4", cfRecord{ID: "r4", Name: "example.com", Type: "MX", Content: "mail.example.com", TTL: 3600, Priority: &prio})
	f.addRecord("zid1", "r5", cfRecord{ID: "r5", Name: "_acme-challenge.example.com", Type: "TXT", Content: "digest", TTL: 60})
	f.addRecord("zid1", "r6", cfRecord{ID: "r6", Name: "example.com", Type: "NS", Content: "anna.ns.cloudflare.com", TTL: 86400}) // filtre
	f.addRecord("zid1", "r7", cfRecord{ID: "r7", Name: "auto.example.com", Type: "A", Content: "1.2.3.6", TTL: 1})                // auto → 60
	p := newTestCFProvider(t, f)

	recs, err := p.Records(context.Background(), "example.com", MaxRecords)
	if err != nil {
		t.Fatalf("kayıtlar: %v", err)
	}
	byNameType := map[string]Record{}
	for _, r := range recs {
		byNameType[r.Name+"/"+r.Type] = r
	}
	if r := byNameType["@/A"]; r.Value != "1.2.3.4" || r.TTL != 3600 {
		t.Fatalf("apex A: %+v", r)
	}
	if r := byNameType["www/A"]; r.Value != "1.2.3.5" {
		t.Fatalf("alt A: %+v", r)
	}
	if r := byNameType["docs/CNAME"]; r.Value != "target.example.com" {
		t.Fatalf("CNAME nokta temizliği: %+v", r)
	}
	if r := byNameType["@/MX"]; r.Value != "10 mail.example.com" {
		t.Fatalf("MX birleşimi: %+v", r)
	}
	if r := byNameType["_acme-challenge/TXT"]; r.Value != "digest" {
		t.Fatalf("TXT: %+v", r)
	}
	if r := byNameType["auto/A"]; r.TTL != 60 {
		t.Fatalf("auto TTL → 60: %+v", r)
	}
	for _, r := range recs {
		if r.Type == "NS" {
			t.Fatalf("NS filtrelenmeli: %+v", r)
		}
	}
	if len(recs) != 6 {
		t.Fatalf("6 kayıt beklenir (NS hariç), gelen: %d", len(recs))
	}
}

func TestCloudflareRecordsZoneMissing(t *testing.T) {
	f := newFakeCF()
	p := newTestCFProvider(t, f)
	if _, err := p.Records(context.Background(), "nowhere.example.com", MaxRecords); err == nil || !strings.Contains(err.Error(), "bulunamadı") {
		t.Fatalf("bölgesiz red: %v", err)
	}
}

func TestCloudflareApplyCreateUpdateDelete(t *testing.T) {
	f := newFakeCF()
	f.addZone("example.com", "zid1")
	p := newTestCFProvider(t, f)
	ctx := context.Background()

	// CREATE — panel adı "@" gövdede "@" olarak gider, sunucu FQDN saklar
	// ve yanıttaki FQDN geri "@" olarak eşlenir (gerçek API davranışı).
	created, err := p.ApplyChange(ctx, "example.com", Change{
		Action: "create", After: &Record{Name: "@", Type: "A", Value: "5.6.7.8", TTL: 300},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Name != "@" || created.Value != "5.6.7.8" {
		t.Fatalf("create yanıtı: %+v", created)
	}
	postVar := false
	for _, path := range f.paths {
		if strings.HasPrefix(path, "POST /zones/zid1/dns_records") {
			postVar = true
			break
		}
	}
	if !postVar {
		t.Fatalf("POST yolu logda yok: %v", f.paths)
	}

	// UPDATE — aynı name+type bulunmalı (id çözümü) ve PUT atılmalı.
	updated, err := p.ApplyChange(ctx, "example.com", Change{
		Action: "update",
		Before: &Record{Name: "@", Type: "A", Value: "5.6.7.8", TTL: 300},
		After:  &Record{Name: "@", Type: "A", Value: "9.9.9.9", TTL: 600},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Value != "9.9.9.9" || updated.TTL != 600 {
		t.Fatalf("update yanıtı: %+v", updated)
	}

	// DELETE.
	if _, err := p.ApplyChange(ctx, "example.com", Change{
		Action: "delete", Before: &Record{Name: "@", Type: "A", Value: "9.9.9.9", TTL: 600},
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := p.ApplyChange(ctx, "example.com", Change{
		Action: "delete", Before: &Record{Name: "@", Type: "A", Value: "9.9.9.9", TTL: 600},
	}); err == nil || !strings.Contains(err.Error(), "bulunamadı") {
		t.Fatalf("ikinci delete dürüst bulunamadı dönmeli: %v", err)
	}
}

func TestCloudflareApplyMXBody(t *testing.T) {
	f := newFakeCF()
	f.addZone("example.com", "zid1")
	p := newTestCFProvider(t, f)
	// Sahte sunucu durumu doğrudan doğrulanır: panelin "10 hedef" değeri
	// ayrı priority + content alanlarına ayrıştırılmış olmalı.
	if _, err := p.ApplyChange(context.Background(), "example.com", Change{
		Action: "create", After: &Record{Name: "@", Type: "MX", Value: "10 mail.example.com", TTL: 3600},
	}); err != nil {
		t.Fatalf("MX create: %v", err)
	}
	var mx cfRecord
	for _, rec := range f.records["zid1"] {
		if rec.Type == "MX" {
			mx = rec
		}
	}
	if mx.Priority == nil || *mx.Priority != 10 || mx.Content != "mail.example.com" {
		t.Fatalf("MX gövde ayrıştırması: %+v", mx)
	}
}

func TestCloudflareAPIErrors(t *testing.T) {
	f := newFakeCF()
	f.addZone("example.com", "zid1")
	ctx := context.Background()

	// Yanlış token → 403 + tipli hata; token değeri MESAJDA YOK.
	p2 := newTestCFProvider(t, f)
	p2.token = "cf-LEAKED-secret"
	_, err := p2.Records(ctx, "example.com", MaxRecords)
	if err == nil || !strings.Contains(err.Error(), "9103") {
		t.Fatalf("403 tipli hata: %v", err)
	}
	if strings.Contains(err.Error(), "LEAKED") {
		t.Fatalf("token hata mesajına sızmış: %v", err)
	}

	// success=false (HTTP 200) zarfı.
	f2 := newFakeCF()
	f2.addZone("example.com", "zid1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfWrite(w, http.StatusOK, cfEnvelope{Success: false, Errors: []cfErr{{Code: 1000, Message: "hesap kotası dolu"}}})
	}))
	defer srv.Close()
	p3, err := BuildCloudflare([]byte(`{"api_token":"tok"}`))
	if err != nil {
		t.Fatal(err)
	}
	p3.baseURL = srv.URL
	p3.http = srv.Client()
	if _, err := p3.Zones(ctx); err == nil || !strings.Contains(err.Error(), "kotası") {
		t.Fatalf("success=false zarfı: %v", err)
	}

	// Şema uyumsuz yanıt.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv2.Close()
	p4, _ := BuildCloudflare([]byte(`{"api_token":"tok"}`))
	p4.baseURL = srv2.URL
	p4.http = srv2.Client()
	if _, err := p4.Zones(ctx); err == nil || !strings.Contains(err.Error(), ErrAPI.Error()) {
		t.Fatalf("şema uyumsuz: %v", err)
	}
}

func TestCloudflareWildcardRoundtrip(t *testing.T) {
	f := newFakeCF()
	f.addZone("example.com", "zid1")
	p := newTestCFProvider(t, f)
	ctx := context.Background()

	// Wildcard create → sunucuya "*.app" adıyla gider, listede "*.app" döner.
	if _, err := p.ApplyChange(ctx, "example.com", Change{
		Action: "create", After: &Record{Name: "*.app", Type: "CNAME", Value: "target.example.com", TTL: 300},
	}); err != nil {
		t.Fatalf("wildcard create: %v", err)
	}
	recs, err := p.Records(ctx, "example.com", MaxRecords)
	if err != nil {
		t.Fatal(err)
	}
	buldu := false
	for _, r := range recs {
		if r.Name == "*.app" && r.Type == "CNAME" && r.Value == "target.example.com" {
			buldu = true
		}
	}
	if !buldu {
		t.Fatalf("wildcard kayıt listelenmedi: %+v", recs)
	}
}

func TestCfRelativeNameAndTTL(t *testing.T) {
	if n := cfRelativeName("example.com", "example.com"); n != "@" {
		t.Fatalf("apex: %q", n)
	}
	if n := cfRelativeName("a.b.example.com", "example.com"); n != "a.b" {
		t.Fatalf("çoklu etiket: %q", n)
	}
	if n := cfRelativeName("*.x.example.com", "example.com"); n != "*.x" {
		t.Fatalf("wildcard: %q", n)
	}
	if n := cfRelativeName("other.org", "example.com"); n != "other.org" {
		t.Fatalf("bölgesiz: %q", n)
	}
	if cfTTL(1) != 60 || cfTTL(59) != 60 || cfTTL(60) != 60 || cfTTL(86400) != 86400 || cfTTL(100000) != 86400 {
		t.Fatalf("TTL normalizasyonu: %d/%d", cfTTL(1), cfTTL(100000))
	}
}
