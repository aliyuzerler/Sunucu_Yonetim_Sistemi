package tasks

// Faz 11: E-posta servisi yönetimi (Postfix + Dovecot + Rspamd).
//
// Şemalar agent/internal/mailops ile AYNA şeklinde tutulur (payload'lar
// iki tarafta da strict doğrulanır — ADR-0015):
//   - Aile node'da --mail-enable ile OPT-IN'dir; modül kurulum YAPMAZ,
//     mail.preflight yalnız RAPOR verir (kriter 1).
//   - Domain/mailbox/alias/quota işlemleri tipli + auditlidir (kriter 2 —
//     görev motoru her task'i auditler; broker her isteği auditler).
//   - Deliverability (SPF/DKIM/DMARC/PTR) DIŞ DNS bağımlılığıdır; backend
//     burada DNS çağrısı yapmaz, UI açıklar (kriter 3).
//   - Config mutasyonları node tarafında syntax check + rollback kapısıyla
//     çalışır (kriter 4); mail.mailbox.create şifresi NODE'da üretilir ve
//     ResultScrubber kalıcılaşmadan ÖNCE seal eder (dbcred kind=mailbox).
//   - mail.queue.list alıcı adreslerini ve gövdeyi ASLA taşımaz (kriter 5);
//     spam/abuse limitleri kapalı-enum limit anahtarlarıyla yönetilir.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
)

// Mirrors of the agent-side bounds (mailops).
const (
	MaxMailDomainLen  = 253
	MaxMailLocalLen   = 64
	MaxMailList       = 500
	MaxMailQueue      = 200
	MaxMailLimitKeys  = 6
	MinMailQuotaBytes = int64(10 << 20)
	MaxMailQuotaBytes = int64(1) << 40
)

// mailValidDomain mirrors mailops.ValidDomain (lowercase FQDN allowlist).
func mailValidDomain(d string) bool {
	if len(d) == 0 || len(d) > MaxMailDomainLen {
		return false
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if len(l) == 0 || len(l) > 63 {
			return false
		}
		if l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-'
			if !ok {
				return false
			}
		}
	}
	return true
}

// mailValidLocal mirrors mailops.ValidLocal (first char alnum, no
// leading/trailing dot, no consecutive dots — exact mirror of the agent RE).
func mailValidLocal(l string) bool {
	if len(l) == 0 || len(l) > MaxMailLocalLen {
		return false
	}
	if l[0] < 'a' || l[0] > 'z' {
		if l[0] < '0' || l[0] > '9' {
			return false
		}
	}
	if l[len(l)-1] == '.' || strings.Contains(l, "..") {
		return false
	}
	for i := 0; i < len(l); i++ {
		c := l[i]
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// mailValidAddress mirrors mailops.ValidAddress.
func mailValidAddress(a string) bool {
	at := strings.LastIndexByte(a, '@')
	if at <= 0 || at == len(a)-1 {
		return false
	}
	return mailValidLocal(a[:at]) && mailValidDomain(a[at+1:]) && len(a) <= MaxMailDomainLen+MaxMailLocalLen+1
}

// mailValidQuota mirrors mailops.ValidQuota.
func mailValidQuota(b int64) bool { return b >= MinMailQuotaBytes && b <= MaxMailQuotaBytes }

// mailLimitKeys mirrors mailops.LimitKeyNames (closed enum with ranges).
var mailLimitKeys = map[string][2]int64{
	"message_size_limit":                  {1 << 20, 100 << 20},
	"mailbox_size_limit":                  {1 << 20, 1 << 30},
	"smtpd_client_message_rate_limit":     {0, 10000},
	"smtpd_client_recipient_rate_limit":   {0, 10000},
	"smtpd_client_connection_count_limit": {0, 1000},
	"anvil_rate_time_unit":                {60, 86400},
}

// mailValidLimitKey mirrors mailops.ValidLimitKey.
func mailValidLimitKey(key string, value int64) bool {
	r, ok := mailLimitKeys[key]
	if !ok {
		return false
	}
	return value >= r[0] && value <= r[1]
}

// mailDomainPayload is {domain} (create/delete).
func validateMailDomain(payload json.RawMessage) error {
	var p struct {
		Domain string `json:"domain"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !mailValidDomain(p.Domain) {
		return errors.New("domain allowlist dışı")
	}
	return nil
}

// mailDomainListPayload is {limit?}.
func validateMailList(payload json.RawMessage) error {
	var p struct {
		Limit int `json:"limit,omitempty"`
	}
	if len(payload) == 0 {
		return nil
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if p.Limit < 0 || p.Limit > MaxMailList {
		return errors.New("limit 0..500 olmalı")
	}
	return nil
}

// mailBoxListPayload is {domain?, limit?}.
func validateMailBoxList(payload json.RawMessage) error {
	var p struct {
		Domain string `json:"domain,omitempty"`
		Limit  int    `json:"limit,omitempty"`
	}
	if len(payload) == 0 {
		return nil
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if p.Domain != "" && !mailValidDomain(p.Domain) {
		return errors.New("domain allowlist dışı")
	}
	if p.Limit < 0 || p.Limit > MaxMailList {
		return errors.New("limit 0..500 olmalı")
	}
	return nil
}

// mailBoxCreatePayload is {domain, local}.
func validateMailBoxCreate(payload json.RawMessage) error {
	var p struct {
		Domain string `json:"domain"`
		Local  string `json:"local"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !mailValidDomain(p.Domain) || !mailValidLocal(p.Local) {
		return errors.New("domain/local allowlist dışı")
	}
	return nil
}

// mailBoxDeletePayload is {mailbox}.
func validateMailBoxDelete(payload json.RawMessage) error {
	var p struct {
		Mailbox string `json:"mailbox"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !mailValidAddress(p.Mailbox) {
		return errors.New("mailbox adresi allowlist dışı")
	}
	return nil
}

// mailAliasCreatePayload is {alias, target}.
func validateMailAliasCreate(payload json.RawMessage) error {
	var p struct {
		Alias  string `json:"alias"`
		Target string `json:"target"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !mailValidAddress(p.Alias) || !mailValidAddress(p.Target) || p.Alias == p.Target {
		return errors.New("alias/target allowlist dışı")
	}
	return nil
}

// mailAliasDeletePayload is {alias}.
func validateMailAliasDelete(payload json.RawMessage) error {
	var p struct {
		Alias string `json:"alias"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !mailValidAddress(p.Alias) {
		return errors.New("alias adresi allowlist dışı")
	}
	return nil
}

// mailQuotaPayload is {mailbox, bytes}.
func validateMailQuota(payload json.RawMessage) error {
	var p struct {
		Mailbox string `json:"mailbox"`
		Bytes   int64  `json:"bytes"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !mailValidAddress(p.Mailbox) {
		return errors.New("mailbox adresi allowlist dışı")
	}
	if !mailValidQuota(p.Bytes) {
		return errors.New("kota 10 MiB .. 1 TiB aralığında olmalı")
	}
	return nil
}

// mailQueuePayload is {limit?} (tavan 200 — kriter 5 sınırlı görüntü).
func validateMailQueue(payload json.RawMessage) error {
	var p struct {
		Limit int `json:"limit,omitempty"`
	}
	if len(payload) == 0 {
		return nil
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if p.Limit < 0 || p.Limit > MaxMailQueue {
		return errors.New("limit 0..200 olmalı")
	}
	return nil
}

// mailLimitsPayload is {limits: {key: value}} — kapalı enum + aralık.
func validateMailLimits(payload json.RawMessage) error {
	var p struct {
		Limits map[string]int64 `json:"limits"`
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if len(p.Limits) == 0 || len(p.Limits) > MaxMailLimitKeys {
		return errors.New("limits 1..6 anahtar olmalı")
	}
	for k, v := range p.Limits {
		if !mailValidLimitKey(k, v) {
			return fmt.Errorf("limit anahtarı/değeri allowlist dışı: %s", k)
		}
	}
	return nil
}

// buildFaz11Registry returns the Faz 11 operation specs (16 mail ops).
func buildFaz11Registry() []*OperationSpec {
	emptyOnly := func(p json.RawMessage) error {
		var v struct{}
		if len(p) == 0 {
			return nil
		}
		if err := decodeStrict(p, &v); err != nil {
			return fmt.Errorf("şema uyumsuz: %w", err)
		}
		return nil
	}
	node := func(name, version string, class Class, retry bool, timeout time.Duration, maxPayload int, validate func(json.RawMessage) error, perm string) *OperationSpec {
		return &OperationSpec{
			Name: name, Version: version, Class: class, Target: TargetNode,
			RetrySafe: retry, Timeout: timeout, MaxPayload: maxPayload,
			Validate: validate, RequiredPerm: perm,
		}
	}
	return []*OperationSpec{
		// preflight/status/config-check/limits.get: salt okuma.
		node("mail.preflight", "1.0.0", ClassReadOnly, true, 60*time.Second, 1<<10, emptyOnly, rbac.PermMailRead),
		node("mail.status", "1.0.0", ClassReadOnly, true, 60*time.Second, 1<<10, emptyOnly, rbac.PermMailRead),
		node("mail.config.check", "1.0.0", ClassReadOnly, true, 60*time.Second, 1<<10, emptyOnly, rbac.PermMailRead),
		node("mail.limits.get", "1.0.0", ClassReadOnly, true, 60*time.Second, 1<<10, emptyOnly, rbac.PermMailRead),
		node("mail.domain.list", "1.0.0", ClassReadOnly, true, 60*time.Second, 1<<10, validateMailList, rbac.PermMailRead),
		node("mail.mailbox.list", "1.0.0", ClassReadOnly, true, 60*time.Second, 1<<10, validateMailBoxList, rbac.PermMailRead),
		node("mail.alias.list", "1.0.0", ClassReadOnly, true, 60*time.Second, 1<<10, validateMailBoxList, rbac.PermMailRead),
		// queue.list: salt okuma; sonuç alıcı adresleri/gövde TAŞIMAZ (kriter 5).
		node("mail.queue.list", "1.0.0", ClassReadOnly, true, 60*time.Second, 1<<10, validateMailQueue, rbac.PermMailRead),

		// domain/create: istenen son durum "domain listede" → retry yakınsar;
		// zaten varsa tipli ErrExists no-op değil, dürüst çelişki bildirimi.
		node("mail.domain.create", "1.0.0", ClassIdempotent, true, 60*time.Second, 1<<10, validateMailDomain, rbac.PermMailCtrl),
		// domain/delete: destructive + retry-güvensiz (ikinci delete bulunamadı).
		node("mail.domain.delete", "1.0.0", ClassDestructive, false, 60*time.Second, 1<<10, validateMailDomain, rbac.PermMailCtrl),
		// mailbox/create: şifre NODE'da üretilir; ResultScrubber seal eder
		// (dbcred kind=mailbox — db.user.create disiplininin aynısı, kriter 2).
		node("mail.mailbox.create", "1.0.0", ClassIdempotent, true, 2*time.Minute, 1<<10, validateMailBoxCreate, rbac.PermMailCtrl),
		// mailbox/delete: Maildir ağacını da kaldırır — destructive.
		node("mail.mailbox.delete", "1.0.0", ClassDestructive, false, 2*time.Minute, 1<<10, validateMailBoxDelete, rbac.PermMailCtrl),
		node("mail.alias.create", "1.0.0", ClassIdempotent, true, 60*time.Second, 1<<10, validateMailAliasCreate, rbac.PermMailCtrl),
		node("mail.alias.delete", "1.0.0", ClassDestructive, false, 60*time.Second, 1<<10, validateMailAliasDelete, rbac.PermMailCtrl),
		// quota/set: istenen son durum "bu kota bu kutuda" → idempotent.
		node("mail.quota.set", "1.0.0", ClassIdempotent, true, 60*time.Second, 1<<10, validateMailQuota, rbac.PermMailCtrl),
		// limits/set: syntax check + rollback kapısı node'dadır (kriter 4).
		node("mail.limits.set", "1.0.0", ClassIdempotent, true, 2*time.Minute, 8<<10, validateMailLimits, rbac.PermMailCtrl),
	}
}
