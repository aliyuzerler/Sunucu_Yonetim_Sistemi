package cloud

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// S3Provider is the first cloud provider adapter: S3-compatible object
// storage over path-style addressing with AWS Signature Version 4 (stdlib
// only). Works with AWS S3, Hetzner Object Storage, MinIO and compatible
// services; each deployment configures its own endpoint + scoped credential.
type S3Provider struct {
	// Client is overridable for tests (httptest servers).
	Client *http.Client
	// Now is overridable for deterministic signatures.
	Now NowFunc
}

// compile-time interface guard.
var _ Provider = (*S3Provider)(nil)

// Kind reports the provider kind.
func (p *S3Provider) Kind() Kind { return KindS3 }

// PermissionMatrix declares the S3 permission model (kriter 5: per-provider
// matrix; delete is destructive + confirmation-gated).
func (p *S3Provider) PermissionMatrix() PermissionMatrix {
	return PermissionMatrix{
		Provider: KindS3,
		Entries: []MatrixEntry{
			{Action: ActionList, ProviderPermission: "s3:ListAllMyBuckets", Risk: RiskRead},
			{Action: ActionRead, ProviderPermission: "s3:ListBucket", Risk: RiskRead},
			{Action: ActionDelete, ProviderPermission: "s3:DeleteBucket", Risk: RiskDestructive, ConfirmRequired: true},
		},
	}
}

// RequiredCredentialKeys lists the s3 credential fields.
func (p *S3Provider) RequiredCredentialKeys() []string {
	return []string{"access_key_id", "secret_access_key"}
}

// httpClient returns the configured client or a bounded default.
func (p *S3Provider) httpClient() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (p *S3Provider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now().UTC()
}

// validateEndpoint enforces the transport policy: https required; plain http
// only for loopback hosts (tests/dev). Returns the parsed URL.
func validateEndpoint(endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidEndpoint, endpoint)
	}
	host := u.Hostname()
	switch u.Scheme {
	case "https":
		return u, nil
	case "http":
		if host == "127.0.0.1" || host == "localhost" || host == "::1" || host == "[::1]" {
			return u, nil
		}
		return nil, fmt.Errorf("%w: http yalnız döngü adreslerinde", ErrInvalidEndpoint)
	default:
		return nil, fmt.Errorf("%w: şema http/https olmalı", ErrInvalidEndpoint)
	}
}

// bucketNameRE: S3 bucket naming (v2 style, conservative subset).
var bucketNameShape = func(name string) bool {
	if len(name) < 3 || len(name) > 63 {
		return false
	}
	for i, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (r == '-' && i > 0 && i < len(name)-1) || r == '.'
		if !ok {
			return false
		}
	}
	return true
}

// ValidateBucketName exposes the bucket shape check for the service layer.
func ValidateBucketName(name string) error {
	if !bucketNameShape(name) {
		return fmt.Errorf("%w: %q (3..63, küçük harf/rakam/nokta/tire)", ErrResourceNameInvalid, name)
	}
	return nil
}

// ---- SigV4 ----

const (
	s3Algorithm  = "AWS4-HMAC-SHA256"
	s3Service    = "s3"
	s3EmptySHA   = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	s3TimeLayout = "20060102T150405Z"
	s3DateLayout = "20060102"
)

// signReq applies AWS SigV4 to the request (path-style, unsigned-empty body).
func signReq(req *http.Request, ak, sk, region string, now time.Time) {
	amzDate := now.UTC().Format(s3TimeLayout)
	dateStamp := now.UTC().Format(s3DateLayout)

	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", s3EmptySHA)
	req.Header.Set("host", req.URL.Host)

	// Canonical headers (must be sorted, lowercase).
	headers := map[string]string{
		"host":                 req.URL.Host,
		"x-amz-content-sha256": s3EmptySHA,
		"x-amz-date":           amzDate,
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var canonHeaders strings.Builder
	for _, k := range keys {
		canonHeaders.WriteString(k)
		canonHeaders.WriteString(":")
		canonHeaders.WriteString(strings.TrimSpace(headers[k]))
		canonHeaders.WriteString("\n")
	}
	signedHeaders := strings.Join(keys, ";")

	// Canonical URI: path-style, each segment URI-encoded once.
	canonURI := req.URL.EscapedPath()
	if canonURI == "" {
		canonURI = "/"
	}
	// Canonical query (sorted).
	q := req.URL.Query()
	qkeys := make([]string, 0, len(q))
	for k := range q {
		qkeys = append(qkeys, k)
	}
	sort.Strings(qkeys)
	var canonQuery strings.Builder
	for i, k := range qkeys {
		vals := q[k]
		sort.Strings(vals)
		for _, v := range vals {
			if i > 0 || canonQuery.Len() > 0 {
				canonQuery.WriteString("&")
			}
			canonQuery.WriteString(uriEncode(k))
			canonQuery.WriteString("=")
			canonQuery.WriteString(uriEncode(v))
		}
	}

	canonicalRequest := strings.Join([]string{
		req.Method, canonURI, canonQuery.String(),
		canonHeaders.String(), signedHeaders, s3EmptySHA,
	}, "\n")

	crHash := sha256.Sum256([]byte(canonicalRequest))
	scope := strings.Join([]string{dateStamp, region, s3Service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		s3Algorithm, amzDate, scope, hex.EncodeToString(crHash[:]),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+sk), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, s3Service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s3Algorithm, ak, scope, signedHeaders, signature))
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// uriEncode implements AWS-style percent encoding.
func uriEncode(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		}
	}
	return b.String()
}

// ---- S3 calls ----

// call performs one signed request and returns the status + body (bounded).
func (p *S3Provider) call(ctx context.Context, c Credentials, method, path string) (int, []byte, error) {
	u, err := validateEndpoint(c.Endpoint)
	if err != nil {
		return 0, nil, err
	}
	u.Path = path
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return 0, nil, fmt.Errorf("cloud s3: istek: %w", err)
	}
	region := c.Region
	if region == "" {
		region = "us-east-1"
	}
	signReq(req, c.AccessKeyID, c.SecretAccessKey, region, p.now())

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrProviderUnreachable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("cloud s3: gövde: %w", err)
	}
	return resp.StatusCode, body, nil
}

// mapStatus maps provider HTTP status to typed errors (honest, no guessing).
func mapStatus(status int, body []byte, notFound, conflict error) error {
	switch {
	case status == 404:
		return notFound
	case status == 409:
		if msg := s3ErrorCode(body); msg != "" {
			return fmt.Errorf("%w: %s", conflict, msg)
		}
		return conflict
	case status == 401 || status == 403:
		return ErrPermission
	case status >= 500:
		return fmt.Errorf("%w: sağlayıcı %d döndürdü", ErrProviderUnreachable, status)
	case status >= 400:
		return fmt.Errorf("%w: sağlayıcı %d döndürdü: %s", ErrProviderUnreachable, status, truncateStr(s3ErrorCode(body), 120))
	}
	return nil
}

// s3ErrorCode extracts <Code>/<Message> from an S3 error body (plain text).
func s3ErrorCode(body []byte) string {
	var x struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if err := xml.Unmarshal(body, &x); err == nil && x.Code != "" {
		if x.Message != "" {
			return x.Code + ": " + x.Message
		}
		return x.Code
	}
	return strings.TrimSpace(string(body))
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// VerifyCredentials probes ListBuckets (read-only) and reports the matrix.
func (p *S3Provider) VerifyCredentials(ctx context.Context, c Credentials) (VerifyReport, error) {
	report := VerifyReport{
		Provider:  KindS3,
		Endpoint:  c.Endpoint,
		Region:    c.Region,
		Matrix:    p.PermissionMatrix(),
		CheckedAt: p.now().UTC().Format(time.RFC3339),
	}
	status, body, err := p.call(ctx, c, http.MethodGet, "/")
	if err != nil {
		report.Probes = append(report.Probes, ProbeResult{Action: ActionList, OK: false, Reason: err.Error()})
		return report, err
	}
	if status == http.StatusOK {
		var list struct {
			Owner struct {
				ID          string `xml:"ID"`
				DisplayName string `xml:"DisplayName"`
			} `xml:"Owner"`
		}
		_ = xml.Unmarshal(body, &list)
		report.Probes = append(report.Probes, ProbeResult{Action: ActionList, OK: true,
			Reason: "s3:ListAllMyBuckets kanıtlandı"})
	} else {
		err = mapStatus(status, body, ErrResourceNotFound, ErrResourceNotEmpty)
		report.Probes = append(report.Probes, ProbeResult{Action: ActionList, OK: false, Reason: err.Error()})
	}
	return report, nil
}

// ListResources lists buckets visible to the credential (bounded).
func (p *S3Provider) ListResources(ctx context.Context, c Credentials, limit int) ([]Resource, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	status, body, err := p.call(ctx, c, http.MethodGet, "/")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, mapStatus(status, body, ErrResourceNotFound, ErrResourceNotEmpty)
	}
	var list struct {
		Buckets []struct {
			Name         string `xml:"Name"`
			CreationDate string `xml:"CreationDate"`
		} `xml:"Buckets>Bucket"`
	}
	if err := xml.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("cloud s3: yanıt çözülemedi: %w", err)
	}
	out := make([]Resource, 0, len(list.Buckets))
	for _, b := range list.Buckets {
		if len(out) >= limit {
			break
		}
		out = append(out, Resource{
			Name:  b.Name,
			Kind:  "bucket",
			Extra: map[string]string{"created_at": b.CreationDate},
		})
	}
	return out, nil
}

// DeleteResource deletes one bucket. S3 refuses non-empty buckets (409 →
// ErrResourceNotEmpty): the control plane never force-deletes provider data.
func (p *S3Provider) DeleteResource(ctx context.Context, c Credentials, name string) error {
	if err := ValidateBucketName(name); err != nil {
		return err
	}
	status, body, err := p.call(ctx, c, http.MethodDelete, "/"+name)
	if err != nil {
		return err
	}
	if status == http.StatusNoContent || status == http.StatusOK {
		return nil
	}
	return mapStatus(status, body, ErrResourceNotFound, ErrResourceNotEmpty)
}
