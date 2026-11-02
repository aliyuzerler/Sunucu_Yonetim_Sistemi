package cloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeS3 is a hermetic S3-compatible server: GET / lists buckets,
// DELETE /{bucket} honors emptiness. It records request signatures for the
// SigV4 shape assertions and counts DELETE calls (confirmation-gate proof).
type fakeS3 struct {
	srv      *httptest.Server
	buckets  map[string]bool
	deletes  atomic.Int64
	lists    atomic.Int64
	sigSeen  atomic.Int64
	authFail bool
}

func newFakeS3(t *testing.T, buckets ...string) *fakeS3 {
	t.Helper()
	f := &fakeS3{buckets: map[string]bool{}}
	for _, b := range buckets {
		f.buckets[b] = true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			f.sigSeen.Add(1)
		}
		if f.authFail {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>AccessDenied</Code><Message>nope</Message></Error>`))
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		switch r.Method {
		case http.MethodGet:
			f.lists.Add(1)
			type bucket struct {
				Name string `xml:"Name"`
			}
			type result struct {
				XMLName xml.Name `xml:"ListAllMyBucketsResult"`
				Owner   struct {
					DisplayName string `xml:"DisplayName"`
				} `xml:"Owner"`
				Buckets struct {
					B []bucket `xml:"Bucket"`
				} `xml:"Buckets"`
			}
			var res result
			res.Owner.DisplayName = "citest"
			for b := range f.buckets {
				res.Buckets.B = append(res.Buckets.B, bucket{Name: b})
			}
			out, _ := xml.Marshal(res)
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(xml.Header))
			_, _ = w.Write(out)
		case http.MethodDelete:
			f.deletes.Add(1)
			if name == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			// Simulate the provider's empty-bucket invariant: deleting a
			// bucket that still holds objects is a typed conflict.
			if strings.HasSuffix(name, "-full") {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>BucketNotEmpty</Code><Message>has objects</Message></Error>`))
				return
			}
			if !f.buckets[name] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchBucket</Code><Message>gone</Message></Error>`))
				return
			}
			delete(f.buckets, name)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func testCreds(endpoint string) Credentials {
	return Credentials{
		Endpoint:        endpoint,
		Region:          "test-1",
		AccessKeyID:     "AKIAfake000000000000",
		SecretAccessKey: strings.Repeat("s", 40), // fake material, runtime-built (CI-49 dersi)
	}
}

func TestS3PermissionMatrixAndShape(t *testing.T) {
	p := &S3Provider{}
	m := p.PermissionMatrix()
	if m.Provider != KindS3 || len(m.Entries) != 3 {
		t.Fatalf("matris: %+v", m)
	}
	var del *MatrixEntry
	for i := range m.Entries {
		if m.Entries[i].Action == ActionDelete {
			del = &m.Entries[i]
		}
	}
	if del == nil || !del.ConfirmRequired || del.Risk != RiskDestructive {
		t.Fatalf("delete girişi destructive + confirm olmalı: %+v", del)
	}
	if p.Kind() != KindS3 {
		t.Fatalf("kind: %s", p.Kind())
	}
}

func TestS3SigV4ShapeAndList(t *testing.T) {
	f := newFakeS3(t, "alpha", "beta")
	p := &S3Provider{Client: f.srv.Client()}
	res, err := p.ListResources(context.Background(), testCreds(f.srv.URL), 10)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("2 bucket bekleniyordu: %+v", res)
	}
	if f.sigSeen.Load() == 0 {
		t.Fatal("istekler SigV4 Authorization taşımalı")
	}
	names := map[string]bool{}
	for _, r := range res {
		names[r.Name] = true
		if r.Kind != "bucket" {
			t.Fatalf("kind bucket olmalı: %+v", r)
		}
	}
	if !names["alpha"] || !names["beta"] {
		t.Fatalf("bucket isimleri: %+v", names)
	}
}

func TestS3VerifyCredentialsProbe(t *testing.T) {
	f := newFakeS3(t, "one")
	p := &S3Provider{Client: f.srv.Client()}
	report, err := p.VerifyCredentials(context.Background(), testCreds(f.srv.URL))
	if err != nil {
		t.Fatalf("VerifyCredentials: %v", err)
	}
	if len(report.Probes) != 1 || !report.Probes[0].OK {
		t.Fatalf("probe raporu: %+v", report.Probes)
	}
	if report.Matrix.Provider != KindS3 {
		t.Fatalf("rapor matrisi: %+v", report.Matrix)
	}
	// Kimlik reddi: rapor dürüst başarısız probe taşır (hata yalnız
	// Erişim-tamamen-yok durumlarında döner; probe raporu kanıttır).
	f.authFail = true
	report2, verr := p.VerifyCredentials(context.Background(), testCreds(f.srv.URL))
	if verr != nil {
		t.Fatalf("probe hatası beklenmedik: %v", verr)
	}
	if len(report2.Probes) != 1 || report2.Probes[0].OK {
		t.Fatalf("başarısız probe bekleniyordu: %+v", report2.Probes)
	}
	if report2.Probes[0].Reason == "" {
		t.Fatal("başarısız probe nedeni raporlanmalı")
	}
}

func TestS3DeleteResourceGates(t *testing.T) {
	f := newFakeS3(t, "empty-bucket", "full-bucket-full")
	p := &S3Provider{Client: f.srv.Client()}
	ctx := context.Background()
	creds := testCreds(f.srv.URL)

	// Boş bucket silinir.
	if err := p.DeleteResource(ctx, creds, "empty-bucket"); err != nil {
		t.Fatalf("DeleteResource: %v", err)
	}
	if f.deletes.Load() != 1 {
		t.Fatalf("1 DELETE bekleniyordu: %d", f.deletes.Load())
	}
	// Dolu bucket → tipli çatışma (panel zorla silmez).
	err := p.DeleteResource(ctx, creds, "full-bucket-full")
	if err == nil || !strings.Contains(err.Error(), "BucketNotEmpty") {
		t.Fatalf("dolu bucket tipli çatışma vermeli: %v", err)
	}
	// Olmayan bucket → typed not found.
	if err := p.DeleteResource(ctx, creds, "missing-bucket"); !errorsIs(err, ErrResourceNotFound) {
		t.Fatalf("olmayan bucket: %v", err)
	}
	// Geçersiz ad hiç HTTP çağrısı yapmadan reddedilir.
	before := f.deletes.Load()
	if err := p.DeleteResource(ctx, creds, "INVALID NAME"); !errorsIs(err, ErrResourceNameInvalid) {
		t.Fatalf("geçersiz ad tipli red: %v", err)
	}
	if f.deletes.Load() != before {
		t.Fatal("geçersiz ad HTTP çağrısı yapmamalı")
	}
}

func TestValidateEndpointPolicy(t *testing.T) {
	if _, err := validateEndpoint("https://s3.example.com"); err != nil {
		t.Fatalf("https kabul: %v", err)
	}
	if _, err := validateEndpoint("http://127.0.0.1:9000"); err != nil {
		t.Fatalf("döngü http kabul: %v", err)
	}
	if _, err := validateEndpoint("http://s3.example.com"); !errorsIs(err, ErrInvalidEndpoint) {
		t.Fatalf("döngü dışı http red: %v", err)
	}
	if _, err := validateEndpoint("ftp://x"); !errorsIs(err, ErrInvalidEndpoint) {
		t.Fatalf("ftp red: %v", err)
	}
	if _, err := validateEndpoint("not a url"); !errorsIs(err, ErrInvalidEndpoint) {
		t.Fatalf("bozuk url red: %v", err)
	}
}

func TestUnreachableProvider(t *testing.T) {
	// Kapalı port → tipli ulaşılamazlık (tahmin yok).
	p := &S3Provider{}
	_, err := p.ListResources(context.Background(), testCreds("http://127.0.0.1:1"), 5)
	if !errorsIs(err, ErrProviderUnreachable) {
		t.Fatalf("ErrProviderUnreachable bekleniyordu: %v", err)
	}
}

func TestFakeCredentialShape(t *testing.T) {
	c := testCreds("http://127.0.0.1:9000")
	// Sahte materyal kaynakta gerçekçi biçim taşıyamaz (CI-49 dersi):
	// access key fake önekiyle kurulur ve secret runtime'da tekrarlanır.
	if !strings.HasPrefix(c.AccessKeyID, "AKIAfake") {
		t.Fatalf("sahte access key bekleniyordu: %q", c.AccessKeyID)
	}
	sum := sha256.Sum256([]byte(c.SecretAccessKey))
	if len(hex.EncodeToString(sum[:])) != 64 {
		t.Fatal("sahte secret beklenmedik")
	}
	_ = fmt.Sprint("noop")
}

// errorsIs is a tiny alias to keep the test file free of an errors import.
func errorsIs(err, target error) bool { return errors.Is(err, target) }
