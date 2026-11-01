// Server is an in-process RFC 8555 test double. It implements the minimal
// directory/account/order/authorization/challenge/finalize/revoke surface
// the x/crypto client exercises and verifies DNS-01 through a test hook
// (wired to dns.MockProvider in integration tests). JWS signatures are NOT
// verified — this is a test double, NOT a CA; production trust belongs to
// the real ACME server configured via DirectoryURL.
//
// Behavior knobs:
//   - LimitAfter: after N new-orders, return 429 + Retry-After (rate limit).
//   - FailDNS01:  mark the dns-01 challenge invalid (challenge failure).
//   - VerifyDNS01(domain, value): happy-path TXT verification hook.
package fakeca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Challenge struct {
	token    string
	url      string
	valid    bool
	verified bool
	errType  string
}

type Authz struct {
	domain string
	status string
	chal   *Challenge
}

type Order struct {
	id          string
	status      string
	authzURLs   []string
	finalizeURL string
	certURL     string
	identifiers []string
	certPEM     []byte
}

type Server struct {
	srv *httptest.Server

	mu        sync.Mutex
	nonces    int
	authzs    map[string]*Authz
	authzSeq  int
	orders    map[string]*Order
	orderSeq  int
	orderHits int
	caCert    *x509.Certificate
	caKey     *ecdsa.PrivateKey

	// LimitAfter: <0 = sınırsız (varsayılan); N>=0 ise N. siparişten
	// sonra her new-order 429 döner (rate limit davranışı).
	LimitAfter  int
	RetryAfter  string
	FailDNS01   bool
	VerifyDNS01 func(domain, value string) error

	RevokedCert []byte
	RevokeCount int
	LastRevoke  string
}

func New() *Server {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fake CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	cert, _ := x509.ParseCertificate(der)
	return &Server{
		authzs:     map[string]*Authz{},
		orders:     map[string]*Order{},
		caCert:     cert,
		caKey:      key,
		LimitAfter: -1, // varsayılan: sınırsız
	}
}

// Start launches the HTTP test server and returns the server.
func (f *Server) Start() *Server {
	f.srv = httptest.NewServer(http.HandlerFunc(f.route))
	return f
}

// Close shuts the server down.
func (f *Server) Close() { f.srv.Close() }

// DirURL returns the ACME directory URL.
func (f *Server) DirURL() string { return f.srv.URL + "/directory" }

func (f *Server) nonceHeader(w http.ResponseWriter) {
	f.mu.Lock()
	f.nonces++
	n := f.nonces
	f.mu.Unlock()
	w.Header().Set("Replay-Nonce", "nonce-"+strconv.Itoa(n))
}

func (f *Server) route(w http.ResponseWriter, r *http.Request) {
	f.nonceHeader(w)
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/directory":
		f.handleDirectory(w)
	case r.Method == http.MethodGet && path == "/new-nonce":
		w.WriteHeader(http.StatusNoContent)
	case path == "/new-account":
		f.handleNewAccount(w, r)
	case path == "/new-order":
		f.handleNewOrder(w, r)
	case path == "/revoke-cert":
		f.handleRevoke(w, r)
	case strings.HasPrefix(path, "/authz/"):
		f.handleAuthz(w, strings.TrimPrefix(path, "/authz/"))
	case strings.HasPrefix(path, "/chal/"):
		f.handleChallenge(w, strings.TrimPrefix(path, "/chal/"))
	case strings.HasPrefix(path, "/finalize/"):
		f.handleFinalize(w, r, strings.TrimPrefix(path, "/finalize/"))
	case strings.HasPrefix(path, "/order/"):
		f.handleGetOrder(w, strings.TrimPrefix(path, "/order/"))
	case strings.HasPrefix(path, "/cert/"):
		f.handleCert(w, strings.TrimPrefix(path, "/cert/"))
	default:
		http.NotFound(w, r)
	}
}

func (f *Server) handleDirectory(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	base := f.srv.URL
	_ = json.NewEncoder(w).Encode(map[string]any{
		"newNonce":   base + "/new-nonce",
		"newAccount": base + "/new-account",
		"newOrder":   base + "/new-order",
		"revokeCert": base + "/revoke-cert",
		"meta":       map[string]any{"termsOfService": base + "/terms"},
	})
}

// jwsEnv is the flattened JWS we decode (signature unverified — test double).
type jwsEnv struct {
	Payload   string `json:"payload"`
	Protected string `json:"protected"`
}

// decodePayload decodes the JWS payload JSON into v.
func decodePayload(r *http.Request, v any) error {
	var env jwsEnv
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		return err
	}
	raw, err := base64.RawURLEncoding.DecodeString(env.Payload)
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(raw, v)
}

func (f *Server) handleNewAccount(w http.ResponseWriter, r *http.Request) {
	if err := decodePayload(r, nil); err != nil {
		f.problem(w, http.StatusBadRequest, "malformed", "JWS çözülemedi")
		return
	}
	kid := f.srv.URL + "/account/" + strconv.Itoa(f.nonces)
	w.Header().Set("Location", kid)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "valid"})
}

func (f *Server) handleNewOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Identifiers []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"identifiers"`
	}
	if err := decodePayload(r, &body); err != nil || len(body.Identifiers) == 0 {
		f.problem(w, http.StatusBadRequest, "malformed", "identifiers eksik")
		return
	}
	f.mu.Lock()
	f.orderHits++
	if f.LimitAfter >= 0 && f.orderHits > f.LimitAfter {
		f.mu.Unlock()
		if f.RetryAfter != "" {
			w.Header().Set("Retry-After", f.RetryAfter)
		}
		f.problem(w, http.StatusTooManyRequests, "rateLimited", "fake rate limit")
		return
	}
	f.orderSeq++
	oid := strconv.Itoa(f.orderSeq)
	order := &Order{
		id:          oid,
		status:      "pending",
		finalizeURL: f.srv.URL + "/finalize/" + oid,
	}
	for _, ident := range body.Identifiers {
		f.authzSeq++
		aid := "a" + strconv.Itoa(f.authzSeq)
		authz := &Authz{
			domain: ident.Value,
			status: "pending",
			chal: &Challenge{
				token: "token-" + aid,
				url:   f.srv.URL + "/chal/" + aid,
			},
		}
		f.authzs[aid] = authz
		order.authzURLs = append(order.authzURLs, f.srv.URL+"/authz/"+aid)
		order.identifiers = append(order.identifiers, ident.Value)
	}
	f.orders[oid] = order
	resp := f.orderJSONLocked(order)
	f.mu.Unlock()

	w.Header().Set("Location", f.srv.URL+"/order/"+oid)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(resp)
}

func (f *Server) orderStatusLocked(o *Order) string {
	if o.status == "valid" {
		return "valid"
	}
	// Tüm authz'lar valid ise sipariş READY'dir (RFC 8555 §7.4).
	allValid := len(o.authzURLs) > 0
	for _, u := range o.authzURLs {
		aid := strings.TrimPrefix(u, f.srv.URL+"/authz/")
		a, ok := f.authzs[aid]
		if !ok || a.status != "valid" {
			allValid = false
			break
		}
		if a.chal.errType != "" {
			return "invalid"
		}
	}
	if allValid {
		return "ready"
	}
	return o.status
}

func (f *Server) orderJSONLocked(o *Order) []byte {
	ids := make([]map[string]string, 0, len(o.identifiers))
	for _, d := range o.identifiers {
		ids = append(ids, map[string]string{"type": "dns", "value": d})
	}
	m := map[string]any{
		"status":         f.orderStatusLocked(o),
		"identifiers":    ids,
		"authorizations": o.authzURLs,
		"finalize":       o.finalizeURL,
	}
	if o.certURL != "" {
		m["certificate"] = o.certURL
	}
	b, _ := json.Marshal(m)
	return b
}

func (f *Server) authzJSON(a *Authz) []byte {
	chalStatus := "pending"
	if a.chal.valid {
		chalStatus = "valid"
	} else if a.chal.errType != "" {
		chalStatus = "invalid"
	}
	m := map[string]any{
		"status":     a.status,
		"identifier": map[string]string{"type": "dns", "value": a.domain},
		"challenges": []map[string]any{{
			"type":   "dns-01",
			"url":    a.chal.url,
			"token":  a.chal.token,
			"status": chalStatus,
		}},
	}
	if a.chal.errType != "" {
		m["challenges"].([]map[string]any)[0]["error"] = map[string]any{
			"type":   a.chal.errType,
			"detail": "fake dns-01 doğrulaması başarısız",
		}
	}
	b, _ := json.Marshal(m)
	return b
}

func (f *Server) chalJSON(a *Authz) []byte {
	chalStatus := "pending"
	if a.chal.valid {
		chalStatus = "valid"
	} else if a.chal.errType != "" {
		chalStatus = "invalid"
	}
	m := map[string]any{
		"type":   "dns-01",
		"url":    a.chal.url,
		"token":  a.chal.token,
		"status": chalStatus,
	}
	if a.chal.errType != "" {
		m["error"] = map[string]any{
			"type":   a.chal.errType,
			"detail": "fake dns-01 doğrulaması başarısız",
		}
	}
	b, _ := json.Marshal(m)
	return b
}

func (f *Server) handleAuthz(w http.ResponseWriter, aid string) {
	f.mu.Lock()
	a, ok := f.authzs[aid]
	if !ok {
		f.mu.Unlock()
		http.NotFound(w, nil)
		return
	}
	body := f.authzJSON(a)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (f *Server) handleChallenge(w http.ResponseWriter, cid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.authzs[cid]
	if !ok {
		http.NotFound(w, nil)
		return
	}
	if a.status == "pending" {
		switch {
		case f.FailDNS01:
			a.chal.errType = "urn:ietf:params:acme:error:dns"
			a.status = "invalid"
		default:
			err := error(nil)
			if f.VerifyDNS01 != nil {
				err = f.VerifyDNS01(a.domain, a.chal.token)
			}
			if err != nil {
				a.chal.errType = "urn:ietf:params:acme:error:dns"
				a.status = "invalid"
			} else {
				a.chal.valid = true
				a.chal.verified = true
				a.status = "valid"
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(f.chalJSON(a))
}

func (f *Server) handleFinalize(w http.ResponseWriter, r *http.Request, oid string) {
	var body struct {
		CSR string `json:"csr"`
	}
	if err := decodePayload(r, &body); err != nil {
		f.problem(w, http.StatusBadRequest, "malformed", "csr eksik")
		return
	}
	csrDER, err := base64.RawURLEncoding.DecodeString(body.CSR)
	if err != nil {
		f.problem(w, http.StatusBadRequest, "malformed", "csr base64")
		return
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		f.problem(w, http.StatusBadRequest, "malformed", "csr ayrıştırılamadı")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[oid]
	if !ok {
		http.NotFound(w, nil)
		return
	}
	if o.status == "pending" {
		serial := time.Now().UnixNano()
		tpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: o.identifiers[0]},
			DNSNames:     o.identifiers,
			NotBefore:    time.Now().Add(-time.Minute),
			NotAfter:     time.Now().Add(90 * 24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		leafDER, err := x509.CreateCertificate(rand.Reader, tpl, f.caCert, csr.PublicKey, f.caKey)
		if err != nil {
			f.problem(w, http.StatusInternalServerError, "serverInternal", err.Error())
			return
		}
		o.certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
		o.status = "valid"
		o.certURL = f.srv.URL + "/cert/" + oid
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(f.orderJSONLocked(o))
}

func (f *Server) handleGetOrder(w http.ResponseWriter, oid string) {
	f.mu.Lock()
	o, ok := f.orders[oid]
	if !ok {
		f.mu.Unlock()
		http.NotFound(w, nil)
		return
	}
	body := f.orderJSONLocked(o)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (f *Server) handleCert(w http.ResponseWriter, oid string) {
	f.mu.Lock()
	o, ok := f.orders[oid]
	f.mu.Unlock()
	if !ok || o.certPEM == nil {
		http.NotFound(w, nil)
		return
	}
	// Zincir: yaprak + CA (PEM, RFC 8555 application/pem-certificate-chain).
	chain := append([]byte(nil), o.certPEM...)
	chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.caCert.Raw})...)
	w.Header().Set("Content-Type", "application/pem-certificate-chain")
	_, _ = w.Write(chain)
}

func (f *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Certificate string `json:"certificate"`
		Reason      int    `json:"reason"`
	}
	if err := decodePayload(r, &body); err != nil {
		f.problem(w, http.StatusBadRequest, "malformed", "payload çözülemedi")
		return
	}
	der, err := base64.RawURLEncoding.DecodeString(body.Certificate)
	if err != nil {
		f.problem(w, http.StatusBadRequest, "malformed", "sertifika base64")
		return
	}
	if _, err := x509.ParseCertificate(der); err != nil {
		f.problem(w, http.StatusBadRequest, "malformed", "sertifika DER değil")
		return
	}
	f.mu.Lock()
	f.RevokedCert = der
	f.RevokeCount++
	f.LastRevoke = fmt.Sprintf("reason=%d", body.Reason)
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (f *Server) problem(w http.ResponseWriter, status int, typ, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"type":"urn:ietf:params:acme:error:` + typ + `","detail":"` + detail + `"}`))
}

// DNS01Digest is exported for tests: the RFC 8555 key authorization
// digest the client publishes as the TXT value.
func DNS01Digest(token, jwkThumbprint string) string {
	keyAuthz := token + "." + jwkThumbprint
	digest := sha256.Sum256([]byte(keyAuthz))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
