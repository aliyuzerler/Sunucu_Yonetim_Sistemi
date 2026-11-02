//go:build linux

// Mutation flows for the Faz 11 mail adapter. Every mutation obeys the
// ADR-0015 invariant (criterion 4): apply → syntax check → ROLLBACK on
// failure; reload is best-effort and honestly reported.
package mailops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---- status ----

// Status probes service activity (systemctl, optional), the queue summary
// and the managed-record counts. Nothing here is destructive.
func (r *Runner) Status(ctx context.Context) (StatusResult, error) {
	ctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	res := StatusResult{Services: map[string]string{}}
	for _, svc := range []string{"postfix", "dovecot", "rspamd"} {
		res.Services[svc] = r.serviceState(ctx, svc)
	}
	res.Queue = r.queueSummary(ctx)
	res.Domains = len(r.readDomainNames())
	res.Mailboxes = len(r.readMapLines(r.vmailboxPath()))
	res.Aliases = len(r.readMapLines(r.valiasPath()))
	mainBytes, err := os.ReadFile(r.mainCF()) //nolint:gosec // operator-managed path constant
	res.PostfixWired = err == nil && strings.Contains(string(mainBytes), MainCFBegin)
	confBytes, err := os.ReadFile(filepath.Join(r.confDir(), DovecotConfName))
	res.DovecotWired = err == nil && strings.Contains(string(confBytes), MainCFBegin)
	return res, nil
}

func (r *Runner) serviceState(ctx context.Context, unit string) string {
	bin, ok := resolve(r.Systemctl, "systemctl")
	if !ok {
		return "unknown"
	}
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := runArgv(sctx, bin, "is-active", unit)
	if err != nil {
		return "inactive"
	}
	st := strings.TrimSpace(string(out))
	if st == "active" || st == "inactive" || st == "failed" {
		return st
	}
	return "unknown"
}

// ---- domains (criterion 2: typed + audited via the task engine) ----

func (r *Runner) domainPath() string   { return filepath.Join(r.baseDir(), DomainsFile) }
func (r *Runner) vmailboxPath() string { return filepath.Join(r.baseDir(), VMailboxFile) }
func (r *Runner) valiasPath() string   { return filepath.Join(r.baseDir(), VAliasFile) }
func (r *Runner) usersPath() string    { return filepath.Join(r.baseDir(), UsersFile) }

// DomainList returns the managed virtual domains (key-only parse — the
// file stores postmap-compatible "domain OK" lines, CI-38).
func (r *Runner) DomainList(_ context.Context, limit int) ([]DomainInfo, error) {
	names := r.readDomainNames()
	if limit > 0 && len(names) > limit {
		names = names[:limit]
	}
	out := make([]DomainInfo, 0, len(names))
	for _, n := range names {
		out = append(out, DomainInfo{Name: n})
	}
	return out, nil
}

// readDomainNames parses the domains map as KEY-ONLY names: the stored
// format is "domain OK" (postmap refuses key-only lines — the compiled
// hash map SKIPS them, which silently breaks virtual_mailbox_domains;
// CI-38). Malformed lines are skipped defensively.
func (r *Runner) readDomainNames() []string {
	var out []string
	for _, l := range r.readMapLines(r.domainPath()) {
		fields := strings.Fields(l)
		if len(fields) == 0 {
			continue
		}
		out = append(out, fields[0])
	}
	return out
}

// DomainCreate appends the domain to the managed map, compiles it and runs
// the postfix syntax gate. Any failure restores the previous file bytes.
func (r *Runner) DomainCreate(ctx context.Context, domain string) (MutateResult, error) {
	if !ValidDomain(domain) {
		return MutateResult{}, ErrInvalidDomain
	}
	if err := r.ensurePostfixWired(ctx); err != nil {
		return MutateResult{}, err
	}
	names := r.readDomainNames()
	for _, n := range names {
		if n == domain {
			return MutateResult{}, ErrExists
		}
	}
	lines := r.readMapLines(r.domainPath())
	newLines := append(append([]string{}, lines...), domain+" OK")
	if _, err := r.applyMap(ctx, r.domainPath(), newLines); err != nil {
		return MutateResult{}, err
	}
	return r.reloadBestEffort(ctx), nil
}

// DomainDelete refuses when mailboxes or aliases still reference the domain
// (protective order: children first). Rollback discipline identical.
func (r *Runner) DomainDelete(ctx context.Context, domain string) (MutateResult, error) {
	if !ValidDomain(domain) {
		return MutateResult{}, ErrInvalidDomain
	}
	suffix := "@" + domain
	for _, l := range r.readMapLines(r.vmailboxPath()) {
		if fields := strings.Fields(l); len(fields) >= 1 && strings.HasSuffix(fields[0], suffix) {
			return MutateResult{}, fmt.Errorf("%w: domain altında mailbox var: %s", ErrConflict, fields[0])
		}
	}
	for _, l := range r.readMapLines(r.valiasPath()) {
		if fields := strings.Fields(l); len(fields) >= 1 && strings.HasSuffix(fields[0], suffix) {
			return MutateResult{}, fmt.Errorf("%w: domain altında alias var: %s", ErrConflict, fields[0])
		}
	}
	lines := r.readMapLines(r.domainPath())
	found := false
	newLines := make([]string, 0, len(lines))
	for _, l := range lines {
		if fields := strings.Fields(l); len(fields) > 0 && fields[0] == domain {
			found = true
			continue
		}
		newLines = append(newLines, l)
	}
	if !found {
		return MutateResult{}, ErrNotFound
	}
	if _, err := r.applyMap(ctx, r.domainPath(), newLines); err != nil {
		return MutateResult{}, err
	}
	return r.reloadBestEffort(ctx), nil
}

// ---- mailboxes ----

// MailboxList returns managed mailboxes (parsed from the dovecot userdb —
// the quota field lives there). NO password material is ever returned.
func (r *Runner) MailboxList(_ context.Context, domain string, limit int) ([]MailboxInfo, error) {
	if domain != "" && !ValidDomain(domain) {
		return nil, ErrInvalidDomain
	}
	lines := r.readMapLines(r.usersPath())
	out := make([]MailboxInfo, 0, len(lines))
	for _, l := range lines {
		addr, home, quota, ok := ParseUserLine(l)
		if !ok {
			continue
		}
		if domain != "" && !strings.HasSuffix(addr, "@"+domain) {
			continue
		}
		out = append(out, MailboxInfo{Address: addr, Maildir: home, QuotaBytes: quota})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// MailboxCreate generates the password ON THE NODE, writes BOTH the postfix
// virtual map and the dovecot userdb entry, syntax-gates BOTH sides and only
// then creates the Maildir. The password appears ONCE in the result; the
// control-plane scrubber seals it before persistence (ADR-0015).
func (r *Runner) MailboxCreate(ctx context.Context, domain, local string) (map[string]any, error) {
	if !ValidDomain(domain) {
		return nil, ErrInvalidDomain
	}
	if !ValidLocal(local) {
		return nil, ErrInvalidLocal
	}
	addr := local + "@" + domain
	if err := r.ensurePostfixWired(ctx); err != nil {
		return nil, err
	}
	if err := r.ensureDovecotWired(ctx); err != nil {
		return nil, err
	}
	if !containsLine(r.readDomainNames(), domain) {
		return nil, fmt.Errorf("%w: domain yönetilen listede yok: %s", ErrNotFound, domain)
	}
	vbLines := r.readMapLines(r.vmailboxPath())
	for _, l := range vbLines {
		if fields := strings.Fields(l); len(fields) >= 1 && fields[0] == addr {
			return nil, ErrExists
		}
	}
	uid, gid, err := r.vmail()
	if err != nil {
		return nil, err
	}
	pw, err := GeneratePassword()
	if err != nil {
		return nil, err
	}
	hash, err := HashSSHA(pw)
	if err != nil {
		return nil, err
	}
	relMaildir := domain + "/" + local + "/Maildir/"
	absMaildir := filepath.Join(r.vmailBase(), domain, local, "Maildir")
	userLines := r.readMapLines(r.usersPath())
	for _, l := range userLines {
		if a, _, _, ok := ParseUserLine(l); ok && a == addr {
			return nil, ErrExists
		}
	}

	newVB := append(append([]string{}, vbLines...), addr+" "+relMaildir)
	vbRes, err := r.applyMap(ctx, r.vmailboxPath(), newVB)
	if err != nil {
		return nil, err
	}
	newUsers := append(append([]string{}, userLines...),
		UserLine(addr, hash, uid, gid, absMaildir, DefaultQuotaBytes))
	if err := r.applyUsersFile(ctx, newUsers); err != nil {
		_ = r.restoreMap(ctx, r.vmailboxPath(), vbRes.backup)
		return nil, err
	}
	// Maildir yalnız SONDA, tüm kapılar geçtikten sonra oluşturulur.
	if err := r.ensureMaildir(absMaildir, uid, gid); err != nil {
		_ = r.restoreUsers(ctx, userLines)
		_ = r.restoreMap(ctx, r.vmailboxPath(), vbRes.backup)
		return nil, err
	}
	rl := r.reloadBestEffort(ctx)
	return map[string]any{
		"applied":  true,
		"reloaded": rl.Reloaded,
		"domain":   domain,
		"mailbox": map[string]any{
			"address":     addr,
			"maildir":     absMaildir,
			"quota_bytes": DefaultQuotaBytes,
		},
		"credentials": map[string]any{
			"username": addr,
			"password": pw, // TEK SEFERLİK — ResultScrubber kalıcılaşmadan ÖNCE seal eder
		},
	}, nil
}

// MailboxDelete removes the postfix map entry, the dovecot userdb entry AND
// the Maildir tree (destructive). Credentials rows on the control plane are
// dropped by the ResultScrubber (dbcred, kind="mailbox").
func (r *Runner) MailboxDelete(ctx context.Context, address string) (map[string]any, error) {
	if !ValidAddress(address) {
		return nil, ErrInvalidAddress
	}
	if err := r.ensurePostfixWired(ctx); err != nil {
		return nil, err
	}
	vbLines := r.readMapLines(r.vmailboxPath())
	var maildirRel string
	newVB := make([]string, 0, len(vbLines))
	foundVB := false
	for _, l := range vbLines {
		if fields := strings.Fields(l); len(fields) >= 2 && fields[0] == address {
			maildirRel = fields[1]
			foundVB = true
			continue
		}
		newVB = append(newVB, l)
	}
	userLines := r.readMapLines(r.usersPath())
	var absMaildir string
	newUsers := make([]string, 0, len(userLines))
	foundUser := false
	for _, l := range userLines {
		if a, home, _, ok := ParseUserLine(l); ok && a == address {
			absMaildir = home
			foundUser = true
			continue
		}
		newUsers = append(newUsers, l)
	}
	if !foundVB && !foundUser {
		return nil, ErrNotFound
	}
	vbRes, err := r.applyMap(ctx, r.vmailboxPath(), newVB)
	if err != nil {
		return nil, err
	}
	if err := r.applyUsersFile(ctx, newUsers); err != nil {
		_ = r.restoreMap(ctx, r.vmailboxPath(), vbRes.backup)
		return nil, err
	}
	// Kaldırım hedefi mailbox DİZİNİDİR (Maildir'in ebeveyni — .../domain/
	// localpart ağacı tamamen gider). Her iki dalda da yol vmailBase altında
	// kalmak zorundadır (scope kanıtı).
	removed := false
	if absMaildir != "" && filepath.IsAbs(absMaildir) {
		boxDir := filepath.Dir(absMaildir)
		if strings.HasPrefix(boxDir, r.vmailBase()+string(os.PathSeparator)) {
			if rmErr := os.RemoveAll(boxDir); rmErr == nil {
				removed = true
			}
		}
	} else if maildirRel != "" {
		rel := strings.TrimSuffix(maildirRel, "/")
		full := filepath.Join(r.vmailBase(), filepath.Clean("/"+rel))
		boxDir := filepath.Dir(full)
		if strings.HasPrefix(boxDir, r.vmailBase()+string(os.PathSeparator)) {
			if rmErr := os.RemoveAll(boxDir); rmErr == nil {
				removed = true
			}
		}
	}
	rl := r.reloadBestEffort(ctx)
	return map[string]any{
		"applied":         true,
		"reloaded":        rl.Reloaded,
		"mailbox":         map[string]any{"address": address},
		"maildir_removed": removed,
	}, nil
}

// ---- aliases ----

// AliasList returns the managed virtual alias mappings.
func (r *Runner) AliasList(_ context.Context, domain string, limit int) ([]AliasInfo, error) {
	if domain != "" && !ValidDomain(domain) {
		return nil, ErrInvalidDomain
	}
	lines := r.readMapLines(r.valiasPath())
	out := make([]AliasInfo, 0, len(lines))
	for _, l := range lines {
		fields := strings.Fields(l)
		if len(fields) != 2 {
			continue
		}
		if domain != "" && !strings.HasSuffix(fields[0], "@"+domain) {
			continue
		}
		out = append(out, AliasInfo{Alias: fields[0], Target: fields[1]})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// AliasCreate adds alias→target. A target INSIDE a managed domain must be
// an existing mailbox (honest config); external targets pass the RE gate.
func (r *Runner) AliasCreate(ctx context.Context, alias, target string) (MutateResult, error) {
	if !ValidAddress(alias) {
		return MutateResult{}, ErrInvalidAddress
	}
	if !ValidAddress(target) {
		return MutateResult{}, ErrInvalidAddress
	}
	if alias == target {
		return MutateResult{}, ErrInvalidAddress
	}
	if err := r.ensurePostfixWired(ctx); err != nil {
		return MutateResult{}, err
	}
	lines := r.readMapLines(r.valiasPath())
	for _, l := range lines {
		if fields := strings.Fields(l); len(fields) >= 1 && fields[0] == alias {
			return MutateResult{}, ErrExists
		}
	}
	// Yerel hedef disiplin: @yönetilen-domain hedefleri gerçek mailbox olmalı.
	if at := strings.LastIndexByte(target, '@'); at >= 0 {
		td := target[at+1:]
		if containsLine(r.readDomainNames(), td) {
			exists := false
			for _, l := range r.readMapLines(r.vmailboxPath()) {
				if fields := strings.Fields(l); len(fields) >= 1 && fields[0] == target {
					exists = true
					break
				}
			}
			if !exists {
				return MutateResult{}, fmt.Errorf("%w: hedef mailbox yok: %s", ErrNotFound, target)
			}
		}
	}
	newLines := append(append([]string{}, lines...), alias+" "+target)
	if _, err := r.applyMap(ctx, r.valiasPath(), newLines); err != nil {
		return MutateResult{}, err
	}
	return r.reloadBestEffort(ctx), nil
}

// AliasDelete removes one alias mapping.
func (r *Runner) AliasDelete(ctx context.Context, alias string) (MutateResult, error) {
	if !ValidAddress(alias) {
		return MutateResult{}, ErrInvalidAddress
	}
	if err := r.ensurePostfixWired(ctx); err != nil {
		return MutateResult{}, err
	}
	lines := r.readMapLines(r.valiasPath())
	found := false
	newLines := make([]string, 0, len(lines))
	for _, l := range lines {
		if fields := strings.Fields(l); len(fields) >= 1 && fields[0] == alias {
			found = true
			continue
		}
		newLines = append(newLines, l)
	}
	if !found {
		return MutateResult{}, ErrNotFound
	}
	if _, err := r.applyMap(ctx, r.valiasPath(), newLines); err != nil {
		return MutateResult{}, err
	}
	return r.reloadBestEffort(ctx), nil
}

// ---- quota (criterion 2) ----

// QuotaSet rewrites the quota_rule extra field of ONE mailbox in the dovecot
// userdb. The postfix map is untouched (delivery size limit is a separate,
// documented operator concern).
func (r *Runner) QuotaSet(ctx context.Context, address string, quotaBytes int64) (MutateResult, error) {
	if !ValidAddress(address) {
		return MutateResult{}, ErrInvalidAddress
	}
	if !ValidQuota(quotaBytes) {
		return MutateResult{}, ErrInvalidQuota
	}
	if err := r.ensureDovecotWired(ctx); err != nil {
		return MutateResult{}, err
	}
	lines := r.readMapLines(r.usersPath())
	found := false
	newLines := make([]string, 0, len(lines))
	for _, l := range lines {
		if a, hash, uid, gid, home, ok := parseUserLineFull(l); ok && a == address {
			found = true
			newLines = append(newLines, UserLine(a, hash, uid, gid, home, quotaBytes))
			continue
		}
		newLines = append(newLines, l)
	}
	if !found {
		return MutateResult{}, ErrNotFound
	}
	if err := r.applyUsersFile(ctx, newLines); err != nil {
		return MutateResult{}, err
	}
	return r.reloadBestEffort(ctx), nil
}

// parseUserLineFull is ParseUserLine plus the hash/uid/gid fields QuotaSet
// needs to re-render the line (same documented 8-field format).
func parseUserLineFull(line string) (address, hash string, uid, gid uint32, home string, ok bool) {
	parts := strings.Split(line, ":")
	if len(parts) < 8 {
		return "", "", 0, 0, "", false
	}
	uidv, err1 := strconvParseUint32(parts[2])
	gidv, err2 := strconvParseUint32(parts[3])
	if err1 != nil || err2 != nil {
		return "", "", 0, 0, "", false
	}
	return parts[0], parts[1], uidv, gidv, parts[5], true
}

// ---- config check (criterion 4, read-only view) ----

// ConfigCheck runs the three syntax gates WITHOUT changing anything.
// rspamd is probed only when its binary exists (absent = honest state).
func (r *Runner) ConfigCheck(ctx context.Context) (ConfigCheckResult, error) {
	ctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	res := ConfigCheckResult{}
	if bin, err := r.lookup(r.Postfix, "postfix"); err == nil {
		if _, rerr := runArgv(ctx, bin, "check"); rerr != nil {
			res.Postfix = CheckOutcome{State: "fail", Detail: "postfix check reddetti"}
		} else {
			res.Postfix = CheckOutcome{State: "pass"}
		}
	} else {
		res.Postfix = CheckOutcome{State: "absent", Detail: "postfix kurulu değil"}
	}
	if bin, err := r.lookup(r.Doveconf, "doveconf"); err == nil {
		if _, rerr := runArgv(ctx, bin, "-n"); rerr != nil {
			res.Dovecot = CheckOutcome{State: "fail", Detail: "doveconf -n reddetti"}
		} else {
			res.Dovecot = CheckOutcome{State: "pass"}
		}
	} else {
		res.Dovecot = CheckOutcome{State: "absent", Detail: "doveconf kurulu değil"}
	}
	if bin, err := r.lookup(r.Rspamadm, "rspamadm"); err == nil {
		if _, rerr := runArgv(ctx, bin, "configtest"); rerr != nil {
			res.Rspamd = CheckOutcome{State: "fail", Detail: "rspamadm configtest reddetti"}
		} else {
			res.Rspamd = CheckOutcome{State: "pass"}
		}
	} else {
		res.Rspamd = CheckOutcome{State: "absent", Detail: "rspamadm kurulu değil"}
	}
	return res, nil
}

// ---- queue (criterion 5) ----

// queueJSONLine is the subset of `postqueue -j` we parse. Bodies and
// recipient addresses are DROPPED at parse time.
type queueJSONLine struct {
	QueueName   string `json:"queue_name"`
	QueueID     string `json:"queue_id"`
	ArrivalTime int64  `json:"arrival_time"`
	MessageSize int64  `json:"message_size"`
	Sender      string `json:"sender"`
	Recipients  []struct {
		Address     string `json:"address"`
		DelayReason string `json:"delay_reason"`
	} `json:"recipients"`
}

// QueueList renders the bounded queue view (no content, criterion 5).
func (r *Runner) QueueList(ctx context.Context, limit int) ([]QueueEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	if limit <= 0 || limit > MaxQueue {
		limit = MaxQueue
	}
	bin, err := r.lookup(r.Postqueue, "postqueue")
	if err != nil {
		return nil, err
	}
	out, err := runArgv(ctx, bin, "-j")
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	var entries []QueueEntry
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var q queueJSONLine
		if json.Unmarshal([]byte(line), &q) != nil || q.QueueID == "" {
			continue
		}
		e := QueueEntry{
			QueueID:    q.QueueID,
			QueueName:  q.QueueName,
			SizeBytes:  q.MessageSize,
			AgeSeconds: now - q.ArrivalTime,
			Sender:     truncate(q.Sender, 200),
		}
		if len(q.Recipients) > 0 {
			e.Reason = truncate(strings.TrimSpace(q.Recipients[0].DelayReason), 200)
		}
		entries = append(entries, e)
		if len(entries) >= limit {
			break
		}
	}
	return entries, nil
}

func (r *Runner) queueSummary(ctx context.Context) QueueSummary {
	bin, ok := resolve(r.Postqueue, "postqueue")
	if !ok {
		return QueueSummary{ProbeMode: "unavailable"}
	}
	qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := runArgv(qctx, bin, "-j")
	if err != nil {
		return QueueSummary{ProbeMode: "unavailable"}
	}
	sum := QueueSummary{ProbeMode: "postqueue"}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var q queueJSONLine
		if json.Unmarshal([]byte(line), &q) != nil || q.QueueID == "" {
			continue
		}
		sum.Total++
		sum.Bytes += q.MessageSize
		if q.QueueName == "deferred" {
			sum.Deferred++
		}
	}
	return sum
}

// ---- limits (criterion 5) ----

// LimitsGet reads the current values of the closed tunable set via
// `postconf -h`. Missing binary → ErrUnavailable.
func (r *Runner) LimitsGet(ctx context.Context) (LimitsGetResult, error) {
	ctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	bin, err := r.lookup(r.Postconf, "postconf")
	if err != nil {
		return LimitsGetResult{}, err
	}
	out := LimitsGetResult{Limits: map[string]string{}}
	for _, k := range LimitKeyNames() {
		v, rerr := runArgv(ctx, bin, "-h", k)
		if rerr != nil {
			out.Limits[k] = ""
			continue
		}
		out.Limits[k] = strings.TrimSpace(string(v))
	}
	return out, nil
}

// LimitsSet applies the closed-enum tunables with the FULL rollback
// discipline: main.cf is backed up in memory, postconf -e writes the keys,
// `postfix check` gates the result — on ANY failure the previous main.cf is
// restored byte-identically (criterion 4). Reload is best-effort.
func (r *Runner) LimitsSet(ctx context.Context, limits map[string]int64) (LimitsSetResult, error) {
	if len(limits) == 0 {
		return LimitsSetResult{}, ErrInvalidLimit
	}
	if len(limits) > len(limitKeys) {
		return LimitsSetResult{}, ErrLimit
	}
	for k, v := range limits {
		if !ValidLimitKey(k, v) {
			return LimitsSetResult{}, fmt.Errorf("%w: %s", ErrInvalidLimit, k)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	bin, err := r.lookup(r.Postconf, "postconf")
	if err != nil {
		return LimitsSetResult{}, err
	}
	fxBin, err := r.lookup(r.Postfix, "postfix")
	if err != nil {
		return LimitsSetResult{}, err
	}
	backup, err := os.ReadFile(r.mainCF()) //nolint:gosec // operator-managed path constant
	if err != nil {
		return LimitsSetResult{}, fmt.Errorf("main.cf okunamadı: %w", err)
	}
	keys := make([]string, 0, len(limits))
	for k, v := range limits {
		if _, rerr := runArgv(ctx, bin, "-e", k+"="+itoaSigned(v)); rerr != nil {
			_ = r.restoreMainCF(backup)
			return LimitsSetResult{}, rerr
		}
		keys = append(keys, k)
	}
	if _, cerr := runArgv(ctx, fxBin, "check"); cerr != nil {
		_ = r.restoreMainCF(backup)
		return LimitsSetResult{}, ErrSyntaxCheck
	}
	res := LimitsSetResult{Applied: true, Keys: keys}
	res.Reloaded = r.reloadPostfix(ctx) == nil
	return res, nil
}

func itoaSigned(v int64) string {
	if v < 0 {
		return "-" + itoa(uint64(-v))
	}
	return itoa(uint64(v))
}

// ---- managed wiring + file plumbing ----

// mainCFBlock renders the managed main.cf block for the given vmail ids.
func mainCFBlock(baseDir, vmailBase string, uid, gid uint32) string {
	return "\n" + MainCFBegin + "\n" +
		"virtual_mailbox_domains = hash:" + baseDir + "/" + DomainsFile + "\n" +
		"virtual_mailbox_maps = hash:" + baseDir + "/" + VMailboxFile + "\n" +
		"virtual_alias_maps = hash:" + baseDir + "/" + VAliasFile + "\n" +
		"virtual_mailbox_base = " + vmailBase + "\n" +
		"virtual_uid_maps = static:" + itoa(uint64(uid)) + "\n" +
		"virtual_gid_maps = static:" + itoa(uint64(gid)) + "\n" +
		"virtual_transport = virtual\n" +
		MainCFEnd + "\n"
}

// dovecotBlock renders the managed conf.d fragment.
func dovecotBlock(baseDir string) string {
	return MainCFBegin + "\n" +
		"passdb {\n  driver = passwd-file\n  args = scheme=SSHA username_format=%u " +
		baseDir + "/" + UsersFile + "\n}\n" +
		"userdb {\n  driver = passwd-file\n  args = username_format=%u " +
		baseDir + "/" + UsersFile + "\n}\n" +
		"mail_location = maildir:~/Maildir\n" +
		"# " + MainCFEnd + "\n"
}

// ensurePostfixWired idempotently prepares the managed directory, the three
// empty maps and the marker block in main.cf (with vmail uid/gid). Runs the
// postfix check gate after any change; restores on failure.
func (r *Runner) ensurePostfixWired(ctx context.Context) error {
	fx, err := r.lookup(r.Postfix, "postfix")
	if err != nil {
		return err
	}
	pm, err := r.lookup(r.Postmap, "postmap")
	if err != nil {
		return err
	}
	uid, gid, err := r.vmail()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(r.baseDir(), 0o750); err != nil {
		return fmt.Errorf("yönetilen dizin oluşturulamadı: %w", err)
	}
	changed := false
	for _, f := range []string{DomainsFile, VMailboxFile, VAliasFile} {
		p := filepath.Join(r.baseDir(), f)
		if _, serr := os.Stat(p); errors.Is(serr, os.ErrNotExist) {
			if werr := atomicWrite(p, []byte(""), 0o644); werr != nil {
				return werr
			}
			changed = true
		}
	}
	mainPath := r.mainCF()
	mainBytes, rerr := os.ReadFile(mainPath) //nolint:gosec // operator-managed path constant
	if rerr != nil {
		return fmt.Errorf("main.cf okunamadı: %w", rerr)
	}
	if !strings.Contains(string(mainBytes), MainCFBegin) {
		block := mainCFBlock(r.baseDir(), r.vmailBase(), uid, gid)
		backup := append([]byte{}, mainBytes...)
		if werr := atomicWrite(mainPath, append(mainBytes, []byte(block)...), 0o644); werr != nil {
			return werr
		}
		changed = true
		if _, cerr := runArgv(ctx, fx, "check"); cerr != nil {
			_ = r.restoreMainCF(backup)
			return ErrSyntaxCheck
		}
	}
	if changed {
		for _, f := range []string{DomainsFile, VMailboxFile, VAliasFile} {
			if _, merr := runArgv(ctx, pm, filepath.Join(r.baseDir(), f)); merr != nil {
				return merr
			}
		}
		if _, cerr := runArgv(ctx, fx, "check"); cerr != nil {
			return ErrSyntaxCheck
		}
	}
	return nil
}

// ensureDovecotWired idempotently writes the managed conf.d fragment and
// gates it with doveconf -n (restore on failure).
func (r *Runner) ensureDovecotWired(ctx context.Context) error {
	dc, err := r.lookup(r.Doveconf, "doveconf")
	if err != nil {
		return err
	}
	confPath := filepath.Join(r.confDir(), DovecotConfName)
	if _, serr := os.Stat(confPath); serr == nil {
		return nil // wired already
	}
	if err := os.MkdirAll(r.confDir(), 0o755); err != nil {
		return fmt.Errorf("dovecot conf.d hazırlanamadı: %w", err)
	}
	if werr := atomicWrite(confPath, []byte(dovecotBlock(r.baseDir())), 0o644); werr != nil {
		return werr
	}
	if _, cerr := runArgv(ctx, dc, "-n"); cerr != nil {
		_ = os.Remove(confPath)
		return ErrSyntaxCheck
	}
	return nil
}

// mapApply is the internal outcome of applyMap: the previous bytes are kept
// so a later step can restore them.
type mapApply struct {
	backup []byte
}

// applyMap replaces a postfix map file with newLines, recompiles it with
// postmap and runs the postfix check gate. On ANY failure the previous
// bytes (and .db) are restored before returning ErrSyntaxCheck.
func (r *Runner) applyMap(ctx context.Context, path string, newLines []string) (mapApply, error) {
	fx, err := r.lookup(r.Postfix, "postfix")
	if err != nil {
		return mapApply{}, err
	}
	pm, err := r.lookup(r.Postmap, "postmap")
	if err != nil {
		return mapApply{}, err
	}
	backup, rerr := os.ReadFile(path) //nolint:gosec // managed map path
	if rerr != nil {
		return mapApply{}, fmt.Errorf("map okunamadı: %w", rerr)
	}
	content := strings.Join(newLines, "\n")
	if len(newLines) > 0 {
		content += "\n"
	}
	if werr := atomicWrite(path, []byte(content), 0o644); werr != nil {
		return mapApply{}, werr
	}
	if _, merr := runArgv(ctx, pm, path); merr != nil {
		_ = r.restoreMap(ctx, path, backup)
		return mapApply{}, merr
	}
	if _, cerr := runArgv(ctx, fx, "check"); cerr != nil {
		_ = r.restoreMap(ctx, path, backup)
		return mapApply{backup: backup}, ErrSyntaxCheck
	}
	return mapApply{backup: backup}, nil
}

// restoreMap rewrites the previous map bytes and recompiles (best-effort —
// the error that caused the rollback is the one reported).
func (r *Runner) restoreMap(ctx context.Context, path string, backup []byte) error {
	if werr := atomicWrite(path, backup, 0o644); werr != nil {
		return werr
	}
	if pm, ok := resolve(r.Postmap, "postmap"); ok {
		rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = runArgv(rctx, pm, path)
	}
	return nil
}

// restoreUsers rewrites the dovecot userdb to the previous bytes.
func (r *Runner) restoreUsers(_ context.Context, backup []string) error {
	content := strings.Join(backup, "\n")
	if len(backup) > 0 {
		content += "\n"
	}
	return atomicWrite(r.usersPath(), []byte(content), 0o600)
}

// applyUsersFile replaces the dovecot userdb atomically and gates with
// doveconf -n (restore on failure). The file carries password HASHES, hence
// 0600 — readable only by the root broker.
func (r *Runner) applyUsersFile(ctx context.Context, newLines []string) error {
	dc, err := r.lookup(r.Doveconf, "doveconf")
	if err != nil {
		return err
	}
	path := r.usersPath()
	var backup []byte
	backup, rerr := os.ReadFile(path) //nolint:gosec // managed userdb path
	if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return fmt.Errorf("userdb okunamadı: %w", rerr)
	}
	content := strings.Join(newLines, "\n")
	if len(newLines) > 0 {
		content += "\n"
	}
	if werr := atomicWrite(path, []byte(content), 0o600); werr != nil {
		return werr
	}
	if _, cerr := runArgv(ctx, dc, "-n"); cerr != nil {
		_ = atomicWrite(path, backup, 0o600)
		return ErrSyntaxCheck
	}
	return nil
}

// restoreMainCF restores the pre-mutation main.cf (limits rollback).
func (r *Runner) restoreMainCF(backup []byte) error {
	return atomicWrite(r.mainCF(), backup, 0o644)
}

// ensureMaildir creates the Maildir hierarchy owned by vmail AFTER all
// syntax gates passed.
func (r *Runner) ensureMaildir(absMaildir string, uid, gid uint32) error {
	for _, sub := range []string{"", "cur", "new", "tmp"} {
		p := filepath.Join(absMaildir, sub)
		if err := os.MkdirAll(p, 0o700); err != nil {
			return fmt.Errorf("Maildir oluşturulamadı: %w", err)
		}
		if err := r.chownTo(p, uid, gid); err != nil {
			return fmt.Errorf("Maildir sahibi ayarlanamadı: %w", err)
		}
	}
	return nil
}

// reloadBestEffort reloads postfix; the flag is honest (not an error).
func (r *Runner) reloadBestEffort(ctx context.Context) MutateResult {
	return MutateResult{Applied: true, Reloaded: r.reloadPostfix(ctx) == nil}
}

func (r *Runner) reloadPostfix(ctx context.Context) error {
	fx, ok := resolve(r.Postfix, "postfix")
	if !ok {
		return ErrUnavailable
	}
	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := runArgv(rctx, fx, "reload")
	return err
}

// readMapLines returns non-empty, non-comment lines (defensive: files we
// did not create may carry comments).
func (r *Runner) readMapLines(path string) []string {
	b, err := os.ReadFile(path) //nolint:gosec // managed map path
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return out
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// atomicWrite writes via a temp file in the SAME directory + rename (no
// partial files, no cross-device rename surprises).
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".srvctl-mail-*")
	if err != nil {
		return fmt.Errorf("temp dosya: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// truncate bounds echoed strings (log/result safety).
func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// strconvParseUint32 is a small indirection to keep the parse helpers local.
func strconvParseUint32(s string) (uint32, error) {
	v, err := parseUint(s)
	if err != nil || v > 0xFFFFFFFF {
		return 0, errors.New("geçersiz uid/gid")
	}
	return uint32(v), nil
}
