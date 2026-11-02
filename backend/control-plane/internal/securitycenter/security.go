// Package securitycenter renders the Faz 13 security posture report. The
// GOLDEN RULE (phase 13 criterion 5): ONLY provable controls are reported,
// and anything uncertain is shown as state "unknown" — never as safe/clean.
// Every control carries its evidence string and CheckedAt timestamp.
package securitycenter

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// State is the honest outcome of a control (closed enum). "unknown" is a
// first-class state: missing evidence is NEVER rendered as ok.
type State string

const (
	StateOK      State = "ok"
	StateWarn    State = "warn"
	StateAlert   State = "alert"
	StateUnknown State = "unknown"
)

// Control IDs (stable API contract).
const (
	CtrlAdminBootstrapped = "admin_bootstrapped"
	CtrlSecretsKey        = "secrets_key_configured"
	CtrlAgentCA           = "agent_ca_configured"
	CtrlNodeCertExpiry    = "agent_cert_expiry"
	CtrlTelemetryFresh    = "node_telemetry_freshness"
	CtrlFailedLogins24h   = "failed_logins_24h"
	CtrlAuditRecording    = "audit_recording_24h"
	CtrlBrokerGates       = "broker_optin_gates"
)

// Control is one reported security check.
type Control struct {
	ID        string    `json:"id"`
	State     State     `json:"state"`
	Evidence  string    `json:"evidence"`
	CheckedAt time.Time `json:"checked_at"`
}

// Report is the full posture snapshot.
type Report struct {
	GeneratedAt time.Time `json:"generated_at"`
	Controls    []Control `json:"controls"`
}

// Config carries the process settings the controls read (NO secrets — only
// their configured/not-configured booleans).
type Config struct {
	SecretsKeySet bool
	AgentCASet    bool
	CookieSecure  bool
}

// Thresholds for the failed-login heuristic (documented constants, not
// config — keeps the report semantics stable and reviewable).
const (
	FailedLoginWarn = 10
	FailedLoginAler = 50

	// CertExpiryWarnDays / CertExpiryAlertDays for node agent certificates.
	CertExpiryWarnDays  = 14
	CertExpiryAlertDays = 3

	// FreshnessStaleSeconds: telemetry older than this on EVERY node is
	// "unknown" (we cannot see the nodes) rather than "ok".
	FreshnessStaleSeconds = 600.0
)

// Service computes the security report on demand (no background state).
type Service struct {
	pool *pgxpool.Pool
	cfg  Config
	now  func() time.Time
}

// NewService builds the security center.
func NewService(pool *pgxpool.Pool, cfg Config) *Service {
	return &Service{pool: pool, cfg: cfg, now: time.Now}
}

// Report computes all controls. A database failure degrades the affected
// DB-backed controls to StateUnknown (honest), never to ok.
func (s *Service) Report(ctx context.Context) (Report, error) {
	now := s.now()
	rep := Report{GeneratedAt: now, Controls: []Control{}}

	rep.Controls = append(rep.Controls,
		s.controlAdminBootstrapped(ctx, now),
		s.controlSecretsKey(now),
		s.controlAgentCA(now),
		s.controlCertExpiry(ctx, now),
		s.controlTelemetryFreshness(ctx, now),
		s.controlFailedLogins(ctx, now),
		s.controlAuditRecording(ctx, now),
		s.controlBrokerGates(ctx, now),
	)
	return rep, nil
}

func (s *Service) controlAdminBootstrapped(ctx context.Context, now time.Time) Control {
	id := CtrlAdminBootstrapped
	var admins int
	err := s.pool.QueryRow(ctx, `
SELECT count(*) FROM users u
JOIN user_role_bindings b ON b.user_id = u.id
JOIN roles r ON r.id = b.role_id
WHERE r.name = 'admin' AND u.status = 'active'`).Scan(&admins)
	if err != nil {
		return Control{ID: id, State: StateUnknown, Evidence: "veritabanı hatası", CheckedAt: now}
	}
	if admins == 0 {
		return Control{ID: id, State: StateAlert, Evidence: "aktif yönetici yok (bootstrap tamamlanmamış)", CheckedAt: now}
	}
	return Control{ID: id, State: StateOK, Evidence: fmt.Sprintf("%d aktif yönetici", admins), CheckedAt: now}
}

func (s *Service) controlSecretsKey(now time.Time) Control {
	id := CtrlSecretsKey
	if !s.cfg.SecretsKeySet {
		// Degrade: db/mailbox şifreleri saklanamaz (redaction kazanır) —
		// fail-closed tasarım; çalışmayı engellemez ama uyarı dürüst verilir.
		return Control{ID: id, State: StateWarn, Evidence: "CP_SECRETS_KEY ayarlı değil: şifreler saklanamaz (redaksiyon kazanır)", CheckedAt: now}
	}
	return Control{ID: id, State: StateOK, Evidence: "CP_SECRETS_KEY ayarlı (AES-256-GCM)", CheckedAt: now}
}

func (s *Service) controlAgentCA(now time.Time) Control {
	id := CtrlAgentCA
	if !s.cfg.AgentCASet {
		return Control{ID: id, State: StateWarn, Evidence: "agent CA yapılandırılmamış: agent bağlantıları kapalı (mTLS kanalı yok)", CheckedAt: now}
	}
	return Control{ID: id, State: StateOK, Evidence: "agent CA yüklü (mTLS gRPC)", CheckedAt: now}
}

func (s *Service) controlCertExpiry(ctx context.Context, now time.Time) Control {
	id := CtrlNodeCertExpiry
	var expired, soon int
	err := s.pool.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE expires_at <= now()),
       count(*) FILTER (WHERE expires_at > now() AND expires_at <= now() + make_interval(days => $1))
FROM certificates WHERE revoked_at IS NULL`, CertExpiryWarnDays).Scan(&expired, &soon)
	if err != nil {
		return Control{ID: id, State: StateUnknown, Evidence: "veritabanı hatası", CheckedAt: now}
	}
	switch {
	case expired > 0:
		return Control{ID: id, State: StateAlert, Evidence: fmt.Sprintf("%d süresi dolmuş node sertifikası", expired), CheckedAt: now}
	case soon > 0:
		return Control{ID: id, State: StateWarn, Evidence: fmt.Sprintf("%d sertifika %d gün içinde doluyor", soon, CertExpiryWarnDays), CheckedAt: now}
	default:
		return Control{ID: id, State: StateOK, Evidence: "tüm node sertifikaları geçerli", CheckedAt: now}
	}
}

// controlTelemetryFreshness reports per-node telemetry staleness. Honest
// semantics: if NO node has fresh data (or none have data at all), the
// control is UNKNOWN — absence of telemetry is not evidence of safety.
func (s *Service) controlTelemetryFreshness(ctx context.Context, now time.Time) Control {
	id := CtrlTelemetryFresh
	rows, err := s.pool.Query(ctx, `
SELECT n.id::text,
       EXTRACT(EPOCH FROM (now() - lm.collected_at))
FROM nodes n
LEFT JOIN LATERAL (
    SELECT max(collected_at) AS collected_at FROM node_metrics WHERE node_id = n.id
) lm ON true`)
	if err != nil {
		return Control{ID: id, State: StateUnknown, Evidence: "veritabanı hatası", CheckedAt: now}
	}
	defer rows.Close()
	var total, fresh, stale, nodata int
	for rows.Next() {
		var idStr string
		var age *float64
		if err := rows.Scan(&idStr, &age); err != nil {
			return Control{ID: id, State: StateUnknown, Evidence: "satır okuma hatası", CheckedAt: now}
		}
		total++
		switch {
		case age == nil:
			nodata++
		case *age <= FreshnessStaleSeconds:
			fresh++
		default:
			stale++
		}
	}
	if err := rows.Err(); err != nil {
		return Control{ID: id, State: StateUnknown, Evidence: "veritabanı hatası", CheckedAt: now}
	}
	if total == 0 {
		return Control{ID: id, State: StateUnknown, Evidence: "kayıtlı node yok (kanıt yok)", CheckedAt: now}
	}
	if fresh == 0 {
		return Control{ID: id, State: StateUnknown, Evidence: fmt.Sprintf("%d node'un telemetrisi yok/tarihli (%d stale, %d veri yok) — güvenlik durumu görülemiyor", total, stale, nodata), CheckedAt: now}
	}
	if stale+nodata > 0 {
		return Control{ID: id, State: StateWarn, Evidence: fmt.Sprintf("%d/%d node güncel (%d stale, %d veri yok)", fresh, total, stale, nodata), CheckedAt: now}
	}
	return Control{ID: id, State: StateOK, Evidence: fmt.Sprintf("%d/%d node telemetrisi güncel", fresh, total), CheckedAt: now}
}

func (s *Service) controlFailedLogins(ctx context.Context, now time.Time) Control {
	id := CtrlFailedLogins24h
	var failures, denied int
	err := s.pool.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE action='auth.login' AND result='failure'),
       count(*) FILTER (WHERE action='auth.denied')
FROM audit_events
WHERE created_at > now() - interval '24 hours'`).Scan(&failures, &denied)
	if err != nil {
		return Control{ID: id, State: StateUnknown, Evidence: "veritabanı hatası", CheckedAt: now}
	}
	total := failures + denied
	switch {
	case total >= FailedLoginAler:
		return Control{ID: id, State: StateAlert, Evidence: fmt.Sprintf("24 saatte %d başarısız giriş/reddedilen yetki (eşik %d)", total, FailedLoginAler), CheckedAt: now}
	case total >= FailedLoginWarn:
		return Control{ID: id, State: StateWarn, Evidence: fmt.Sprintf("24 saatte %d başarısız giriş/reddedilen yetki (eşik %d)", total, FailedLoginWarn), CheckedAt: now}
	default:
		return Control{ID: id, State: StateOK, Evidence: fmt.Sprintf("24 saatte %d başarısız giriş/reddedilen yetki", total), CheckedAt: now}
	}
}

// controlAuditRecording verifies the audit trail is actually receiving
// events. Zero events in 24h is NOT proof of a healthy trail — it is either
// an idle system or a broken recorder; both are "unknown" unless there is
// evidence of recent activity.
func (s *Service) controlAuditRecording(ctx context.Context, now time.Time) Control {
	id := CtrlAuditRecording
	var events24h, events7d int
	err := s.pool.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE created_at > now() - interval '24 hours'),
       count(*) FILTER (WHERE created_at > now() - interval '7 days')
FROM audit_events`).Scan(&events24h, &events7d)
	if err != nil {
		return Control{ID: id, State: StateUnknown, Evidence: "veritabanı hatası", CheckedAt: now}
	}
	switch {
	case events24h > 0:
		return Control{ID: id, State: StateOK, Evidence: fmt.Sprintf("24 saatte %d denetim kaydı", events24h), CheckedAt: now}
	case events7d > 0:
		return Control{ID: id, State: StateUnknown, Evidence: fmt.Sprintf("24 saatte kayıt yok, 7 günde %d — sistem boşta veya akış kesildi (kanıt yetersiz)", events7d), CheckedAt: now}
	default:
		return Control{ID: id, State: StateUnknown, Evidence: "denetim kaydı bulunamadı (kanıt yok)", CheckedAt: now}
	}
}

// controlBrokerGates inventories the OPTIONAL privileged broker families
// that are actually announced by at least one node capability report. This
// is evidence-based (capability registry), not assumed from config.
func (s *Service) controlBrokerGates(ctx context.Context, now time.Time) Control {
	id := CtrlBrokerGates
	rows, err := s.pool.Query(ctx, `SELECT capability_id FROM node_capabilities`)
	if err != nil {
		return Control{ID: id, State: StateUnknown, Evidence: "veritabanı hatası", CheckedAt: now}
	}
	defer rows.Close()
	open := map[string]bool{}
	for rows.Next() {
		var capID string
		if err := rows.Scan(&capID); err != nil {
			return Control{ID: id, State: StateUnknown, Evidence: "satır okuma hatası", CheckedAt: now}
		}
		familyOf(capID, open)
	}
	if err := rows.Err(); err != nil {
		return Control{ID: id, State: StateUnknown, Evidence: "veritabanı hatası", CheckedAt: now}
	}
	if len(open) == 0 {
		return Control{ID: id, State: StateOK, Evidence: "isteğe bağlı broker ailesi açık değil (db/fs/mail/vm/web/docker yok) — varsayılan-deny", CheckedAt: now}
	}
	// Deterministic order.
	order := []string{"docker", "web", "db", "fs", "mail", "vm"}
	out := ""
	for _, f := range order {
		if open[f] {
			if out != "" {
				out += ", "
			}
			out += f
		}
	}
	return Control{ID: id, State: StateOK, Evidence: "açık opt-in aileler: " + out, CheckedAt: now}
}

// familyOf classifies a capability id into its opt-in family.
func familyOf(capID string, open map[string]bool) {
	switch {
	case hasPrefix(capID, "docker."):
		open["docker"] = true
	case hasPrefix(capID, "web."):
		open["web"] = true
	case hasPrefix(capID, "db."):
		open["db"] = true
	case hasPrefix(capID, "fs."):
		open["fs"] = true
	case hasPrefix(capID, "mail."):
		open["mail"] = true
	case hasPrefix(capID, "vm."):
		open["vm"] = true
	}
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}
