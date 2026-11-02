// Package mailops implements the Faz 11 e-mail service management adapter
// (Postfix + Dovecot + Rspamd), executed ONLY in the privileged broker
// (server-privd, --mail-enable) with fixed argv — never a shell.
//
// Security model (ADR-0015, docs/architecture/mail.md):
//   - OPT-IN family (default OFF): the module never installs packages and
//     never enables itself; mail.preflight only REPORTS what is missing
//     (criterion 1). Mutations operate on a self-contained managed config
//     directory (/etc/server-control/mail) — postfix/dovecot map files and
//     a marker-delimited managed block in main.cf / conf.d.
//   - Every mutation follows WRITE-TEMP → SYNTAX-CHECK → ATOMIC-RENAME →
//     RELOAD(best-effort). A failed syntax check leaves the previous state
//     byte-identical (typed ErrSyntaxCheck; criterion 4). Reload failure is
//     reported honestly (reloaded=false) — a valid config is never rolled
//     back because a daemon was not running.
//   - Mailbox passwords are generated ON THE NODE (crypto/rand) and are
//     returned ONCE in the task result; the control plane seals them before
//     persistence (kind="mailbox", same one-time reveal discipline as db
//     users). They are never logged, never re-displayed.
//   - mail.queue.list exposes queue ID/sender/size/age/reason ONLY — message
//     bodies and full recipient lists NEVER cross the boundary (criterion 5).
//   - Deliverability (SPF/DKIM/DMARC/PTR) is an EXTERNAL DNS responsibility:
//     the adapter verifies nothing beyond the node, the UI explains the
//     required records (criterion 3) — no DNS calls are made on the hot path.
package mailops

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"math/big"
	"regexp"
	"strings"
)

// Operation names (mirror backend/internal/tasks ops_faz11.go).
const (
	OpMailPreflight    = "mail.preflight"
	OpMailStatus       = "mail.status"
	OpMailDomainList   = "mail.domain.list"
	OpMailDomainCreate = "mail.domain.create"
	OpMailDomainDelete = "mail.domain.delete"
	OpMailBoxList      = "mail.mailbox.list"
	OpMailBoxCreate    = "mail.mailbox.create"
	OpMailBoxDelete    = "mail.mailbox.delete"
	OpMailAliasList    = "mail.alias.list"
	OpMailAliasCreate  = "mail.alias.create"
	OpMailAliasDelete  = "mail.alias.delete"
	OpMailQuotaSet     = "mail.quota.set"
	OpMailConfigCheck  = "mail.config.check"
	OpMailQueueList    = "mail.queue.list"
	OpMailLimitsGet    = "mail.limits.get"
	OpMailLimitsSet    = "mail.limits.set"
)

// Bounds (mirrored in backend/internal/tasks ops_faz11.go).
const (
	MaxDomainLen   = 253
	MaxLocalLen    = 64
	MaxAddressLen  = MaxLocalLen + 1 + MaxDomainLen
	MaxList        = 500
	MaxQueue       = 200
	PasswordLength = 32

	// Quota bounds: 10 MiB .. 1 TiB per mailbox (dovecot quota_rule bytes).
	MinQuotaBytes = int64(10 << 20)
	MaxQuotaBytes = int64(1) << 40

	// Payload schema ceiling (tiny payloads; broker line cap is 1 MiB).
	MaxPayloadBytes = 8 << 10
)

// Managed paths (defaults; overridable per Runner for hermetic tests).
const (
	DefaultBaseDir        = "/etc/server-control/mail"
	DefaultPostfixMainCF  = "/etc/postfix/main.cf"
	DefaultDovecotConfDir = "/etc/dovecot/conf.d"
	DefaultVMailBase      = "/var/vmail"

	// MainCFBegin/MainCFEnd delimit the managed block inside main.cf.
	MainCFBegin = "# BEGIN server-control mail (managed) — ADR-0015"
	MainCFEnd   = "# END server-control mail (managed)"

	// DovecotConfName is the managed conf.d fragment file name.
	DovecotConfName = "99-server-control.conf"

	// File names inside BaseDir (postfix hash maps + dovecot userdb).
	DomainsFile  = "domains"
	VMailboxFile = "vmailbox"
	VAliasFile   = "valias"
	UsersFile    = "dovecot-users"
)

// Ports probed by preflight (criterion 1).
var PreflightPorts = []int{25, 465, 587, 143, 993}

// Stable errors (mapped to broker categories in exec_unix.go).
var (
	ErrInvalidDomain  = errors.New("geçersiz domain adı")
	ErrInvalidLocal   = errors.New("geçersiz posta kutusu yerel kısmı")
	ErrInvalidAddress = errors.New("geçersiz e-posta adresi")
	ErrInvalidQuota   = errors.New("geçersiz kota (10 MiB .. 1 TiB)")
	ErrInvalidLimit   = errors.New("geçersiz limit anahtarı/değeri")
	ErrNotFound       = errors.New("kayıt bulunamadı")
	ErrExists         = errors.New("kayıt zaten var")
	ErrConflict       = errors.New("mail durumu çelişkili")
	ErrSyntaxCheck    = errors.New("sözdizimi denetimi başarısız — değişiklik uygulanmadı")
	ErrNoVmailUser    = errors.New("vmail sistemi kullanıcısı yok (useradd vmail gerekli)")
	ErrUnavailable    = errors.New("posta bileşeni kullanılamıyor")
	ErrLimit          = errors.New("sınır aşıldı")
)

// domainRE: strict lowercase FQDN allowlist (labels 1..63, total ≤ 253).
// Anything it rejects never reaches a config file.
var domainRE = regexp.MustCompile(
	`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// localRE: strict local-part allowlist (the managed subset — no quoted
// forms, no leading/trailing dot, no consecutive dots).
var localRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,62}[a-z0-9])?$`)

// ValidDomain reports whether the domain passes the allowlist.
func ValidDomain(d string) bool {
	return len(d) <= MaxDomainLen && domainRE.MatchString(d)
}

// ValidLocal reports whether the local part passes the allowlist.
func ValidLocal(l string) bool {
	return len(l) <= MaxLocalLen && localRE.MatchString(l) && !strings.Contains(l, "..")
}

// ValidAddress reports whether the FULL address passes both allowlists.
func ValidAddress(a string) bool {
	at := strings.LastIndexByte(a, '@')
	if at <= 0 || at == len(a)-1 {
		return false
	}
	return ValidLocal(a[:at]) && ValidDomain(a[at+1:]) && len(a) <= MaxAddressLen
}

// ValidQuota reports whether the byte quota is in the closed range.
func ValidQuota(b int64) bool { return b >= MinQuotaBytes && b <= MaxQuotaBytes }

// limitSpec is one closed postfix rate/size tunable (criterion 5). Keys are
// a closed enum: unknown keys are typed rejections, never passed through.
type limitSpec struct {
	key string
	min int64
	max int64
}

// limitKeys lists the managed tunables in display order.
var limitKeys = []limitSpec{
	{"message_size_limit", 1 << 20, 100 << 20},       // 1 MiB .. 100 MiB
	{"mailbox_size_limit", 1 << 20, 1 << 30},         // 1 MiB .. 1 GiB
	{"smtpd_client_message_rate_limit", 0, 10000},    // 0 = unlimited
	{"smtpd_client_recipient_rate_limit", 0, 10000},  // 0 = unlimited
	{"smtpd_client_connection_count_limit", 0, 1000}, // 0 = unlimited
	{"anvil_rate_time_unit", 60, 86400},              // seconds
}

// ValidLimitKey reports whether the key is managed and the value in range.
func ValidLimitKey(key string, value int64) bool {
	for _, s := range limitKeys {
		if s.key == key {
			return value >= s.min && value <= s.max
		}
	}
	return false
}

// LimitKeyNames returns the closed key set (UI + CP mirror).
func LimitKeyNames() []string {
	out := make([]string, 0, len(limitKeys))
	for _, s := range limitKeys {
		out = append(out, s.key)
	}
	return out
}

// GeneratePassword returns a cryptographically random alphanumeric password
// (32 chars). Same discipline as dbops: charset is safe to hash and echo.
func GeneratePassword() (string, error) {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, PasswordLength)
	max := big.NewInt(int64(len(charset)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = charset[n.Int64()]
	}
	return string(out), nil
}

// HashSSHA renders a Dovecot-compatible {SSHA} password hash (RFC 2307):
// SHA1(password + salt), base64 of digest+salt. Salt is 16 random bytes.
// This avoids shelling out to `doveadm pw` and keeps password material in
// process memory only.
func HashSSHA(password string) (string, error) {
	salt := make([]byte, 16)
	for i := 0; i < len(salt); i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(256))
		if err != nil {
			return "", err
		}
		salt[i] = byte(n.Int64())
	}
	h := sha1.New() //nolint:gosec // {SSHA} is the scheme Dovecot's userdb declares; strength comes from the 32-char random password
	h.Write([]byte(password))
	h.Write(salt)
	sum := h.Sum(nil)
	return "{SSHA}" + base64.StdEncoding.EncodeToString(append(sum, salt...)), nil
}

// UserLine renders ONE dovecot passwd-file line in the DOCUMENTED format
// (docs: user:password:uid:gid:(gecos):home:(shell):extra_fields, extra
// fields are a SPACE-separated list of key=value pairs):
//
//	addr@domain:{SSHA}..:uid:gid::/home/path::userdb_quota_rule=*:bytes=N
//
// The quota rule value legitimately contains ':' (quota syntax), which is
// safe because extra fields are space-separated, not colon-separated.
func UserLine(address, hash string, uid, gid uint32, home string, quotaBytes int64) string {
	return address + ":" + hash + ":" + itoa(uint64(uid)) + ":" + itoa(uint64(gid)) +
		"::" + home + "::userdb_quota_rule=*:bytes=" + itoa(uint64(quotaBytes))
}

// ParseUserLine extracts (address, home, quotaBytes) from a stored line.
// Malformed lines are skipped by callers (never a panic).
func ParseUserLine(line string) (address, home string, quota int64, ok bool) {
	parts := strings.Split(line, ":")
	if len(parts) < 8 {
		return "", "", 0, false
	}
	address = parts[0]
	home = parts[5]
	// The quota VALUE legitimately contains ':' — re-join the extra-field
	// segments before matching (dovecot itself splits extra fields on
	// spaces, not colons).
	q := strings.Join(parts[7:], ":")
	if !strings.HasPrefix(q, "userdb_quota_rule=*:bytes=") {
		return "", "", 0, false
	}
	v, perr := parseUint(q[len("userdb_quota_rule=*:bytes="):])
	if perr != nil {
		return "", "", 0, false
	}
	return address, home, int64(v), true
}

// itoa is a tiny allocation-light formatter for the file renderer.
func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func parseUint(s string) (uint64, error) {
	if s == "" {
		return 0, errors.New("boş")
	}
	var v uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errors.New("rakam dışı")
		}
		v = v*10 + uint64(c-'0')
	}
	return v, nil
}
