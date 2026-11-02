// Package broker implements the Faz 04 local privileged broker protocol.
//
// Trust model (docs/architecture/security-boundaries.md):
//   - The broker (server-privd) is a SEPARATE binary and identity running as
//     root; the agent runs unprivileged.
//   - The broker listens ONLY on a mode-0660 Unix socket. There is no TCP.
//   - Peer identity is verified with SO_PEERCRED against an explicit UID
//     allowlist; default deny.
//   - Requests are typed JSON with allowlisted operations; parameters are
//     strictly validated. Inputs are NEVER merged into shell strings.
//   - Every accepted request (and every rejection) is audited.
package broker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/backupops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/composeops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/dbops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/dockerx"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fsops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fwops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/mailops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/pkgops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/tlsops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/vmops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/webops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/winops"
)

// Protocol version of the Unix-socket request/response format.
const ProtocolV1 = 1

// Allowlisted operations (the ONLY ones the broker will ever run). Linux
// and Windows families coexist in the protocol; the per-OS executor
// rejects the foreign family at dispatch (default deny).
const (
	OpServiceList    = "linux.systemd.service.list"
	OpServiceStatus  = "linux.systemd.service.status"
	OpServiceStart   = "linux.systemd.service.start"
	OpServiceStop    = "linux.systemd.service.stop"
	OpServiceRestart = "linux.systemd.service.restart"

	// Faz 06: read-only package inventory + firewall survey/preview.
	OpLinuxPkgList             = pkgops.OpLinuxPkgList
	OpLinuxFirewallStatus      = fwops.OpLinuxFirewallStatus
	OpLinuxFirewallRulesList   = fwops.OpLinuxFirewallRulesList
	OpLinuxFirewallRulePreview = fwops.OpLinuxFirewallRulePreview

	// Faz 07: Docker Engine ailesi. Daemon soketine erişim YALNIZ bu
	// broker'dadır (unix socket; TCP yok — ADR-0011). remove ailesi
	// PREVIEW-ONLY'dir: yürütme yolu hiç yoktur.
	OpDockerContainerList    = dockerx.OpDockerContainerList
	OpDockerContainerInspect = dockerx.OpDockerContainerInspect
	OpDockerContainerLogs    = dockerx.OpDockerContainerLogs
	OpDockerContainerStart   = dockerx.OpDockerContainerStart
	OpDockerContainerStop    = dockerx.OpDockerContainerStop
	OpDockerContainerRestart = dockerx.OpDockerContainerRestart
	OpDockerContainerRemove  = dockerx.OpDockerContainerRemove
	OpDockerImageList        = dockerx.OpDockerImageList
	OpDockerImageRemove      = dockerx.OpDockerImageRemove
	OpDockerNetworkList      = dockerx.OpDockerNetworkList
	OpDockerNetworkRemove    = dockerx.OpDockerNetworkRemove
	OpDockerVolumeList       = dockerx.OpDockerVolumeList
	OpDockerVolumeRemove     = dockerx.OpDockerVolumeRemove

	// Faz 07: Compose analizi — broker'dan geçmez (agent-local saf
	// analiz); ad sabitleri dışa açılır.
	OpDockerComposeValidate = composeops.OpDockerComposeValidate
	OpDockerComposeDeploy   = composeops.OpDockerComposeDeploy

	// Faz 08: Web hosting ailesi (nginx + PHP-FPM). Yürütme yüzü
	// YALNIZ bu broker'dadır (--web-enable ile seçilir; ADR-0012).
	OpWebSiteList    = webops.OpWebSiteList
	OpWebSiteStatus  = webops.OpWebSiteStatus
	OpWebSiteCreate  = webops.OpWebSiteCreate
	OpWebSiteUpdate  = webops.OpWebSiteUpdate
	OpWebSiteDisable = webops.OpWebSiteDisable
	OpWebSiteDelete  = webops.OpWebSiteDelete
	OpWebPHPList     = webops.OpWebPHPList

	// Faz 09: TLS sertifika ailesi (web ailesiyle aynı --web-enable
	// kapısı; ADR-0013). Özel anahtar ASLA broker dışına çıkmaz.
	OpWebCertRequest = tlsops.OpWebCertRequest
	OpWebCertInstall = tlsops.OpWebCertInstall
	OpWebCertStatus  = tlsops.OpWebCertStatus

	// Faz 10: veritabanı ailesi (node-local instance'lar; panel DB'sine
	// yapısal erişim yok — ADR-0014). Yürütme yüzü YALNIZ broker'dadır
	// (--db-enable ile seçilir; VARSAYILAN KAPALI).
	OpDBInstanceList   = dbops.OpDBInstanceList
	OpDBDatabaseList   = dbops.OpDBDatabaseList
	OpDBDatabaseCreate = dbops.OpDBDatabaseCreate
	OpDBDatabaseDelete = dbops.OpDBDatabaseDelete
	OpDBUserList       = dbops.OpDBUserList
	OpDBUserCreate     = dbops.OpDBUserCreate
	OpDBUserDelete     = dbops.OpDBUserDelete
	OpDBGrantSet       = dbops.OpDBGrantSet
	OpDBBackup         = dbops.OpDBBackup
	OpDBBackupList     = dbops.OpDBBackupList
	OpDBRestore        = dbops.OpDBRestore

	// Faz 10: scoped dosya yöneticisi (kökler yalnız webops site
	// docroot'ları; --fs-enable ile seçilir; VARSAYILAN KAPALI —
	// ADR-0014). Dolaşım openat+O_NOFOLLOW yarışsızdır.
	OpFSList     = fsops.OpFSList
	OpFSDownload = fsops.OpFSDownload
	OpFSUpload   = fsops.OpFSUpload
	OpFSChmod    = fsops.OpFSChmod
	OpFSChown    = fsops.OpFSChown

	// Faz 11: e-posta servisi ailesi (--mail-enable opt-in — ADR-0015).
	OpMailPreflight    = mailops.OpMailPreflight
	OpMailStatus       = mailops.OpMailStatus
	OpMailDomainList   = mailops.OpMailDomainList
	OpMailDomainCreate = mailops.OpMailDomainCreate
	OpMailDomainDelete = mailops.OpMailDomainDelete
	OpMailBoxList      = mailops.OpMailBoxList
	OpMailBoxCreate    = mailops.OpMailBoxCreate
	OpMailBoxDelete    = mailops.OpMailBoxDelete
	OpMailAliasList    = mailops.OpMailAliasList
	OpMailAliasCreate  = mailops.OpMailAliasCreate
	OpMailAliasDelete  = mailops.OpMailAliasDelete
	OpMailQuotaSet     = mailops.OpMailQuotaSet
	OpMailConfigCheck  = mailops.OpMailConfigCheck
	OpMailQueueList    = mailops.OpMailQueueList
	OpMailLimitsGet    = mailops.OpMailLimitsGet
	OpMailLimitsSet    = mailops.OpMailLimitsSet

	// Faz 12: KVM/QEMU/libvirt sanallaştırma ailesi (--vm-enable opt-in —
	// ADR-0016). Disk görüntü yolu payload'da ASLA yoktur (kriter 3).
	OpVMPreflight      = vmops.OpVMPreflight
	OpVMStatus         = vmops.OpVMStatus
	OpVMList           = vmops.OpVMList
	OpVMInfo           = vmops.OpVMInfo
	OpVMCreate         = vmops.OpVMCreate
	OpVMClone          = vmops.OpVMClone
	OpVMStart          = vmops.OpVMStart
	OpVMStop           = vmops.OpVMStop
	OpVMReboot         = vmops.OpVMReboot
	OpVMDelete         = vmops.OpVMDelete
	OpVMDiskList       = vmops.OpVMDiskList
	OpVMDiskAdd        = vmops.OpVMDiskAdd
	OpVMSnapshotList   = vmops.OpVMSnapshotList
	OpVMSnapshotCreate = vmops.OpVMSnapshotCreate
	OpVMSnapshotRevert = vmops.OpVMSnapshotRevert
	OpVMSnapshotDelete = vmops.OpVMSnapshotDelete

	// Faz 14: yedekleme ailesi (restic; --backup-enable ile opt-in — ADR-0018).
	OpBackupRepoInit     = backupops.OpBackupRepoInit
	OpBackupRun          = backupops.OpBackupRun
	OpBackupSnapshotList = backupops.OpBackupSnapshotList
	OpBackupVerify       = backupops.OpBackupVerify
	OpBackupRestore      = backupops.OpBackupRestore

	OpWinServiceList    = winops.OpWinServiceList
	OpWinServiceStatus  = winops.OpWinServiceStatus
	OpWinServiceStart   = winops.OpWinServiceStart
	OpWinServiceStop    = winops.OpWinServiceStop
	OpWinServiceRestart = winops.OpWinServiceRestart
)

// Result statuses.
const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusRejected  = "rejected"
)

// Error categories (bounded, stable).
const (
	CatInvalidRequest   = "invalid_request"
	CatPeerDenied       = "peer_denied"
	CatUnsupportedOp    = "unsupported_operation"
	CatInvalidParams    = "invalid_params"
	CatRateLimited      = "rate_limited"
	CatInternal         = "internal_error"
	CatExecutorFailed   = "executor_error"
	CatExecutorTimedOut = "executor_timeout"
)

// Request is one typed broker request (newline-delimited JSON, one per conn).
type Request struct {
	V         int             `json:"v"`                 // ProtocolV1
	Operation string          `json:"operation"`         // allowlisted op
	TaskID    string          `json:"task_id,omitempty"` // control-plane task
	RequestID string          `json:"request_id"`        // caller correlation
	CorrID    string          `json:"correlation_id,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
}

// Response is the broker's typed reply.
type Response struct {
	V             int             `json:"v"`
	Operation     string          `json:"operation"`
	TaskID        string          `json:"task_id,omitempty"`
	RequestID     string          `json:"request_id"`
	Status        string          `json:"status"`
	ErrorCategory string          `json:"error_category,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	Output        string          `json:"output,omitempty"`
}

// UnitParams is the schema for unit-targeted params (status/start/stop/restart).
type UnitParams struct {
	Unit string `json:"unit"`
}

// ServiceParams is the windows schema for name-targeted params (mirror of
// winops.ServiceParams).
type ServiceParams struct {
	Service string `json:"service"`
}

// ListParams is the schema for list params (empty object or with limit).
type ListParams struct {
	Limit int `json:"limit,omitempty"`
}

// maxRequestBytes bounds a single request line. Faz 10: 16 KiB → 1 MiB —
// fs.upload gövdesi (base64 ≤ 768 KiB) tek JSONL satırında taşınır. Sınır
// kasıtlıdır: peer çekirdek-kimlikli UID allowlist'li agent'tır, eşzamanlı
// yürütme semaforu (4) sınırlıdır; 1 MiB bellek tavanı görev motorundaki
// MaxPayload tavanıyla aynıdır (ADR-0014).
const maxRequestBytes = 1 << 20

// dockerCall carries the Faz 07 docker-family parameters extracted at
// validation time (ref/tail/timeout/remove-kind).
type dockerCall struct {
	kind    string // remove-preview kind: container|image|volume|network
	ref     string
	tail    int
	timeout int
	force   bool
	link    bool
	v       bool
	prune   bool
}

// webCall carries the Faz 08/09 web-family parameters extracted at
// validation time (site name, full spec, list bound, cert payloads).
type webCall struct {
	site  string
	spec  *webops.SiteSpec
	limit int
	// Faz 09: TLS sertifika parametreleri.
	certDomains []string
	certInstall *tlsInstallCall
}

// tlsInstallCall carries the public certificate material to stage.
type tlsInstallCall struct {
	domain   string
	certPEM  string
	chainPEM string
}

// dbCall carries the Faz 10 database-family parameters extracted at
// validation time (every field is RE-validated here — the broker trusts
// nothing).
type dbCall struct {
	kind     string
	database string
	owner    string
	user     string
	host     string
	level    string
	limit    int
	backup   string
}

// fsCall carries the Faz 10 file-manager parameters.
type fsCall struct {
	root      string
	path      string
	content   string
	mode      string
	overwrite bool
	limit     int
}

// mailCall carries the Faz 11 mail-family parameters extracted at
// validation time (every field is RE-validated — the broker trusts nothing).
type mailCall struct {
	domain  string
	local   string
	mailbox string // full address (user@domain)
	alias   string // full alias address
	target  string
	quota   int64
	limit   int
	limits  map[string]int64
}

// vmCall carries the Faz 12 VM-family parameters extracted at validation
// time. NOTE: there is deliberately NO disk-image PATH field anywhere —
// callers name pools/volumes/domains only; paths are derived on the node
// from libvirt's own output (ADR-0016 criterion 3).
type vmCall struct {
	name        string // domain name (also used for clone target)
	source      string // clone source
	pool        string
	bridge      string
	snapshot    string
	description string
	vcpu        int64
	memMiB      int64
	diskGiB     int64
	force       bool
	limit       int
}

// backupCall carries the Faz 14 backup-family parameters. NOTE: there is
// deliberately NO restore TARGET and NO repo path field — both are
// node-managed constants (ADR-0018 kriter 2/3).
type backupCall struct {
	paths    []string
	limit    int
	backupID string
	snapshot string
}

// validatedCall is the typed outcome of request validation.
type validatedCall struct {
	target    string              // unit/service name (service families)
	listLimit int                 // list family bound
	preview   fwops.PreviewParams // firewall preview payload
	docker    dockerCall          // docker family payload
	web       webCall             // web hosting family payload
	db        dbCall              // Faz 10 database family payload
	fs        fsCall              // Faz 10 file-manager payload
	mail      mailCall            // Faz 11 mail family payload
	vm        vmCall              // Faz 12 VM family payload
	backup    backupCall          // Faz 14 backup family payload
}

// validateRequest enforces the allowlist + payload schema for one request.
// It returns a validatedCall and an error with a stable category.
func validateRequest(req *Request) (validatedCall, error) {
	empty := validatedCall{}
	if req == nil {
		return empty, reject(CatInvalidRequest, "boş istek")
	}
	if req.V != ProtocolV1 {
		return empty, reject(CatInvalidRequest, "desteklenmeyen protokol sürümü")
	}
	switch req.Operation {
	case OpServiceStatus, OpServiceStart, OpServiceStop, OpServiceRestart:
		var p UnitParams
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "unit şeması uyumsuz")
		}
		if err := winops.ValidateLinuxUnit(p.Unit); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		return validatedCall{target: p.Unit}, nil
	case OpWinServiceStatus, OpWinServiceStart, OpWinServiceStop, OpWinServiceRestart:
		var p ServiceParams
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "service şeması uyumsuz")
		}
		if err := winops.ValidateServiceName(p.Service); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		return validatedCall{target: p.Service}, nil
	case OpServiceList, OpWinServiceList, OpLinuxPkgList, OpLinuxFirewallRulesList,
		OpDockerContainerList, OpDockerImageList, OpDockerNetworkList, OpDockerVolumeList:
		var p ListParams
		if len(req.Params) == 0 {
			req.Params = []byte("{}")
		}
		if err := strictDecode(req.Params, &p); err != nil || p.Limit < 0 || p.Limit > 500 {
			return empty, reject(CatInvalidParams, "list şeması uyumsuz")
		}
		if p.Limit == 0 {
			p.Limit = 200
		}
		return validatedCall{listLimit: p.Limit}, nil
	case OpLinuxFirewallStatus:
		var p struct{}
		if len(req.Params) == 0 {
			req.Params = []byte("{}")
		}
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "status şeması uyumsuz (boş obje beklenir)")
		}
		return empty, nil
	case OpLinuxFirewallRulePreview:
		var p fwops.PreviewParams
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "preview şeması uyumsuz")
		}
		if err := fwops.ValidatePreviewParams(p); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		return validatedCall{preview: p}, nil

	// ---- Faz 07: docker ailesi ----
	case OpDockerContainerInspect, OpDockerContainerStart:
		var p dockerx.RemoveParams
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "ref şeması uyumsuz")
		}
		if err := dockerx.ValidateRemoveParams("container", p.Ref); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		return validatedCall{docker: dockerCall{ref: p.Ref}}, nil
	case OpDockerContainerLogs:
		var p struct {
			Ref  string `json:"ref"`
			Tail int    `json:"tail,omitempty"`
		}
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "log şeması uyumsuz")
		}
		if err := dockerx.ValidateRemoveParams("container", p.Ref); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		if p.Tail == 0 {
			p.Tail = 100
		}
		if p.Tail < 1 || p.Tail > 500 {
			return empty, reject(CatInvalidParams, "tail 1..500 olmalı")
		}
		return validatedCall{docker: dockerCall{ref: p.Ref, tail: p.Tail}}, nil
	case OpDockerContainerStop, OpDockerContainerRestart:
		var p struct {
			Ref     string `json:"ref"`
			Timeout int    `json:"timeout,omitempty"`
		}
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "action şeması uyumsuz")
		}
		if err := dockerx.ValidateRemoveParams("container", p.Ref); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		if p.Timeout == 0 {
			p.Timeout = 10
		}
		if p.Timeout < 1 || p.Timeout > 120 {
			return empty, reject(CatInvalidParams, "timeout 1..120 olmalı")
		}
		return validatedCall{docker: dockerCall{ref: p.Ref, timeout: p.Timeout}}, nil
	case OpDockerContainerRemove, OpDockerImageRemove, OpDockerVolumeRemove, OpDockerNetworkRemove:
		kind := removeKind(req.Operation)
		var p dockerx.RemoveParams
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "remove şeması uyumsuz")
		}
		if err := dockerx.ValidateRemoveParams(kind, p.Ref); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		// PREVIEW-ONLY (ADR-0011): yalnız render edilecek veri taşınır; broker
		// bu aile için hiçbir DELETE yürütme yolu içermez.
		return validatedCall{docker: dockerCall{kind: kind, ref: p.Ref, force: p.Force, link: p.Link, v: p.V, prune: p.Prune}}, nil

	// ---- Faz 08: web hosting ailesi ----
	case OpWebSiteList:
		var p ListParams
		if len(req.Params) == 0 {
			req.Params = []byte("{}")
		}
		if err := strictDecode(req.Params, &p); err != nil || p.Limit < 0 || p.Limit > webops.MaxSiteList {
			return empty, reject(CatInvalidParams, "list şeması uyumsuz")
		}
		if p.Limit == 0 {
			p.Limit = 200
		}
		return validatedCall{web: webCall{limit: p.Limit}}, nil
	case OpWebSiteStatus, OpWebSiteDisable, OpWebSiteDelete:
		var p struct {
			Site string `json:"site"`
		}
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "site şeması uyumsuz")
		}
		if !webops.ValidSiteName(p.Site) {
			return empty, reject(CatInvalidParams, "site adı allowlist dışı")
		}
		return validatedCall{web: webCall{site: p.Site}, target: p.Site}, nil
	case OpWebSiteCreate, OpWebSiteUpdate:
		if len(req.Params) > webops.MaxPayloadBytes {
			return empty, reject(CatInvalidParams, "payload çok büyük")
		}
		var p webops.SiteSpec
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "site spec şeması uyumsuz")
		}
		if err := p.Validate(); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		spec := p
		return validatedCall{web: webCall{site: spec.Name, spec: &spec}, target: spec.Name}, nil
	case OpWebPHPList:
		var p struct{}
		if len(req.Params) == 0 {
			req.Params = []byte("{}")
		}
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "php.list şeması uyumsuz (boş obje beklenir)")
		}
		return validatedCall{}, nil

	// ---- Faz 09: TLS sertifika ailesi ----
	case OpWebCertRequest:
		if len(req.Params) > tlsops.MaxPayloadBytes {
			return empty, reject(CatInvalidParams, "payload çok büyük")
		}
		var p struct {
			Domain string   `json:"domain"`
			SANs   []string `json:"sans,omitempty"`
		}
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "cert request şeması uyumsuz")
		}
		domains, err := tlsops.NormalizeDomains(p.Domain, p.SANs)
		if err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		return validatedCall{web: webCall{certDomains: domains}, target: domains[0]}, nil
	case OpWebCertInstall:
		if len(req.Params) > tlsops.MaxPayloadBytes {
			return empty, reject(CatInvalidParams, "payload çok büyük")
		}
		var p struct {
			Domain   string `json:"domain"`
			CertPEM  string `json:"cert_pem"`
			ChainPEM string `json:"chain_pem,omitempty"`
		}
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "cert install şeması uyumsuz")
		}
		if !tlsops.ValidDomain(p.Domain) {
			return empty, reject(CatInvalidParams, "domain geçersiz")
		}
		if p.CertPEM == "" || len(p.CertPEM) > tlsops.MaxCertPEMLen || len(p.ChainPEM) > tlsops.MaxCertPEMLen {
			return empty, reject(CatInvalidParams, "PEM boyut sınırları")
		}
		if !strings.Contains(p.CertPEM, "BEGIN CERTIFICATE") {
			return empty, reject(CatInvalidParams, "cert_pem CERTIFICATE PEM olmalı")
		}
		return validatedCall{web: webCall{site: p.Domain, certInstall: &tlsInstallCall{domain: p.Domain, certPEM: p.CertPEM, chainPEM: p.ChainPEM}}, target: p.Domain}, nil
	case OpWebCertStatus:
		var p struct{}
		if len(req.Params) == 0 {
			req.Params = []byte("{}")
		}
		if err := strictDecode(req.Params, &p); err != nil {
			return empty, reject(CatInvalidParams, "cert status şeması uyumsuz (boş obje beklenir)")
		}
		return validatedCall{}, nil
	// ---- Faz 10: veritabanı ailesi ----
	case OpDBInstanceList:
		if err := emptyObject(req.Params); err != nil {
			return empty, reject(CatInvalidParams, "instance.list şeması uyumsuz (boş obje beklenir)")
		}
		return empty, nil
	case OpDBDatabaseList, OpDBUserList, OpDBBackupList:
		var p struct {
			Kind  string `json:"kind"`
			Limit int    `json:"limit,omitempty"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "db list şeması uyumsuz")
		}
		if !dbops.ValidKind(p.Kind) || !dbops.ValidLimit(p.Limit) {
			return empty, reject(CatInvalidParams, "kind/limit geçersiz")
		}
		return validatedCall{db: dbCall{kind: p.Kind, limit: p.Limit}}, nil
	case OpDBDatabaseCreate:
		var p struct {
			Kind  string `json:"kind"`
			Name  string `json:"name"`
			Owner string `json:"owner,omitempty"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "database.create şeması uyumsuz")
		}
		if !dbops.ValidKind(p.Kind) || !dbops.ValidName(p.Name) ||
			(p.Owner != "" && !dbops.ValidName(p.Owner)) {
			return empty, reject(CatInvalidParams, "kind/name/owner geçersiz")
		}
		if dbops.IsProtectedDatabase(p.Name) {
			return empty, reject(CatInvalidParams, "korumalı veritabanı adı")
		}
		return validatedCall{db: dbCall{kind: p.Kind, database: p.Name, owner: p.Owner}, target: p.Name}, nil
	case OpDBDatabaseDelete, OpDBBackup:
		var p struct {
			Kind     string `json:"kind"`
			Database string `json:"database"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "database şeması uyumsuz")
		}
		if !dbops.ValidKind(p.Kind) || !dbops.ValidName(p.Database) {
			return empty, reject(CatInvalidParams, "kind/database geçersiz")
		}
		if dbops.IsProtectedDatabase(p.Database) {
			return empty, reject(CatInvalidParams, "korumalı veritabanı adı")
		}
		return validatedCall{db: dbCall{kind: p.Kind, database: p.Database}, target: p.Database}, nil
	case OpDBUserCreate, OpDBUserDelete:
		var p struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
			Host string `json:"host,omitempty"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "user şeması uyumsuz")
		}
		if !dbops.ValidKind(p.Kind) || !dbops.ValidName(p.Name) {
			return empty, reject(CatInvalidParams, "kind/name geçersiz")
		}
		if dbops.IsProtectedUser(p.Name) {
			return empty, reject(CatInvalidParams, "korumalı kullanıcı adı")
		}
		if p.Kind == dbops.KindMySQL {
			if p.Host == "" {
				p.Host = "localhost"
			}
			if !dbops.ValidHost(p.Host) {
				return empty, reject(CatInvalidParams, "host geçersiz")
			}
		} else if p.Host != "" {
			return empty, reject(CatInvalidParams, "postgres user host almaz")
		}
		return validatedCall{db: dbCall{kind: p.Kind, user: p.Name, host: p.Host}, target: p.Name}, nil
	case OpDBGrantSet:
		var p struct {
			Kind     string `json:"kind"`
			User     string `json:"user"`
			Host     string `json:"host,omitempty"`
			Database string `json:"database"`
			Level    string `json:"level"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "grant şeması uyumsuz")
		}
		if !dbops.ValidKind(p.Kind) || !dbops.ValidName(p.User) ||
			!dbops.ValidName(p.Database) || !dbops.ValidLevel(p.Level) {
			return empty, reject(CatInvalidParams, "kind/user/database/level geçersiz")
		}
		if dbops.IsProtectedUser(p.User) {
			return empty, reject(CatInvalidParams, "korumalı kullanıcı adı")
		}
		if p.Kind == dbops.KindMySQL {
			if p.Host == "" {
				p.Host = "localhost"
			}
			if !dbops.ValidHost(p.Host) {
				return empty, reject(CatInvalidParams, "host geçersiz")
			}
		} else if p.Host != "" {
			return empty, reject(CatInvalidParams, "postgres grant host almaz")
		}
		return validatedCall{db: dbCall{kind: p.Kind, user: p.User, host: p.Host, database: p.Database, level: p.Level}, target: p.User}, nil
	case OpDBRestore:
		var p struct {
			Kind     string `json:"kind"`
			Database string `json:"database"`
			Backup   string `json:"backup"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "restore şeması uyumsuz")
		}
		if !dbops.ValidKind(p.Kind) || !dbops.ValidName(p.Database) || !dbops.ValidBackupName(p.Backup) {
			return empty, reject(CatInvalidParams, "kind/database/backup geçersiz")
		}
		if dbops.IsProtectedDatabase(p.Database) {
			return empty, reject(CatInvalidParams, "korumalı veritabanı adı")
		}
		return validatedCall{db: dbCall{kind: p.Kind, database: p.Database, backup: p.Backup}, target: p.Database}, nil

	// ---- Faz 10: scoped dosya yöneticisi ----
	case OpFSList:
		var p struct {
			Root  string `json:"root"`
			Path  string `json:"path,omitempty"`
			Limit int    `json:"limit,omitempty"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "fs.list şeması uyumsuz")
		}
		if !fsops.ValidRoot(p.Root) {
			return empty, reject(CatInvalidParams, "root geçersiz")
		}
		if _, err := fsops.ValidRelPath(p.Path); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		if p.Limit < 0 || p.Limit > fsops.MaxListEntries {
			return empty, reject(CatInvalidParams, "limit geçersiz")
		}
		return validatedCall{fs: fsCall{root: p.Root, path: p.Path, limit: p.Limit}, target: p.Root}, nil
	case OpFSDownload, OpFSChown:
		var p struct {
			Root string `json:"root"`
			Path string `json:"path"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "fs şeması uyumsuz")
		}
		if !fsops.ValidRoot(p.Root) {
			return empty, reject(CatInvalidParams, "root geçersiz")
		}
		if _, err := fsops.ValidRelPath(p.Path); err != nil || p.Path == "" {
			return empty, reject(CatInvalidParams, "path geçersiz")
		}
		return validatedCall{fs: fsCall{root: p.Root, path: p.Path}, target: p.Root}, nil
	case OpFSUpload:
		if len(req.Params) > fsops.MaxPayloadBytes {
			return empty, reject(CatInvalidParams, "payload çok büyük")
		}
		var p struct {
			Root      string `json:"root"`
			Path      string `json:"path"`
			Content   string `json:"content_b64"`
			Mode      string `json:"mode,omitempty"`
			Overwrite bool   `json:"overwrite,omitempty"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "fs.upload şeması uyumsuz")
		}
		if !fsops.ValidRoot(p.Root) {
			return empty, reject(CatInvalidParams, "root geçersiz")
		}
		if _, err := fsops.ValidRelPath(p.Path); err != nil || p.Path == "" {
			return empty, reject(CatInvalidParams, "path geçersiz")
		}
		if p.Mode != "" {
			if _, err := fsops.ParseMode(p.Mode); err != nil {
				return empty, reject(CatInvalidParams, err.Error())
			}
		}
		return validatedCall{fs: fsCall{root: p.Root, path: p.Path, content: p.Content, mode: p.Mode, overwrite: p.Overwrite}, target: p.Root}, nil
	case OpFSChmod:
		var p struct {
			Root string `json:"root"`
			Path string `json:"path"`
			Mode string `json:"mode"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "fs.chmod şeması uyumsuz")
		}
		if !fsops.ValidRoot(p.Root) {
			return empty, reject(CatInvalidParams, "root geçersiz")
		}
		if _, err := fsops.ValidRelPath(p.Path); err != nil || p.Path == "" {
			return empty, reject(CatInvalidParams, "path geçersiz")
		}
		if _, err := fsops.ParseMode(p.Mode); err != nil {
			return empty, reject(CatInvalidParams, err.Error())
		}
		return validatedCall{fs: fsCall{root: p.Root, path: p.Path, mode: p.Mode}, target: p.Root}, nil

	// ---- Faz 11: e-posta servisi ailesi (ADR-0015) ----
	case OpMailPreflight, OpMailStatus, OpMailConfigCheck, OpMailLimitsGet:
		if err := emptyObject(req.Params); err != nil {
			return empty, reject(CatInvalidParams, "mail şeması uyumsuz (boş obje beklenir)")
		}
		return empty, nil
	case OpMailDomainList:
		var p struct {
			Limit int `json:"limit,omitempty"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil || p.Limit < 0 || p.Limit > mailops.MaxList {
			return empty, reject(CatInvalidParams, "domain list şeması uyumsuz")
		}
		return validatedCall{mail: mailCall{limit: p.Limit}}, nil
	case OpMailDomainCreate, OpMailDomainDelete:
		var p struct {
			Domain string `json:"domain"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "domain şeması uyumsuz")
		}
		if !mailops.ValidDomain(p.Domain) {
			return empty, reject(CatInvalidParams, "domain allowlist dışı")
		}
		return validatedCall{mail: mailCall{domain: p.Domain}, target: p.Domain}, nil
	case OpMailBoxList, OpMailAliasList:
		var p struct {
			Domain string `json:"domain,omitempty"`
			Limit  int    `json:"limit,omitempty"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil ||
			p.Limit < 0 || p.Limit > mailops.MaxList {
			return empty, reject(CatInvalidParams, "mail list şeması uyumsuz")
		}
		if p.Domain != "" && !mailops.ValidDomain(p.Domain) {
			return empty, reject(CatInvalidParams, "domain allowlist dışı")
		}
		return validatedCall{mail: mailCall{domain: p.Domain, limit: p.Limit}, target: p.Domain}, nil
	case OpMailBoxCreate:
		var p struct {
			Domain string `json:"domain"`
			Local  string `json:"local"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "mailbox create şeması uyumsuz")
		}
		if !mailops.ValidDomain(p.Domain) || !mailops.ValidLocal(p.Local) {
			return empty, reject(CatInvalidParams, "domain/local allowlist dışı")
		}
		return validatedCall{mail: mailCall{domain: p.Domain, local: p.Local}, target: p.Local + "@" + p.Domain}, nil
	case OpMailBoxDelete:
		var p struct {
			Mailbox string `json:"mailbox"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "mailbox delete şeması uyumsuz")
		}
		if !mailops.ValidAddress(p.Mailbox) {
			return empty, reject(CatInvalidParams, "mailbox adresi allowlist dışı")
		}
		return validatedCall{mail: mailCall{mailbox: p.Mailbox}, target: p.Mailbox}, nil
	case OpMailAliasCreate:
		var p struct {
			Alias  string `json:"alias"`
			Target string `json:"target"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "alias create şeması uyumsuz")
		}
		if !mailops.ValidAddress(p.Alias) || !mailops.ValidAddress(p.Target) || p.Alias == p.Target {
			return empty, reject(CatInvalidParams, "alias/target allowlist dışı")
		}
		return validatedCall{mail: mailCall{alias: p.Alias, target: p.Target}, target: p.Alias}, nil
	case OpMailAliasDelete:
		var p struct {
			Alias string `json:"alias"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "alias delete şeması uyumsuz")
		}
		if !mailops.ValidAddress(p.Alias) {
			return empty, reject(CatInvalidParams, "alias adresi allowlist dışı")
		}
		return validatedCall{mail: mailCall{alias: p.Alias}, target: p.Alias}, nil
	case OpMailQuotaSet:
		var p struct {
			Mailbox string `json:"mailbox"`
			Bytes   int64  `json:"bytes"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "quota şeması uyumsuz")
		}
		if !mailops.ValidAddress(p.Mailbox) || !mailops.ValidQuota(p.Bytes) {
			return empty, reject(CatInvalidParams, "mailbox/quota geçersiz")
		}
		return validatedCall{mail: mailCall{mailbox: p.Mailbox, quota: p.Bytes}, target: p.Mailbox}, nil
	case OpMailQueueList:
		var p struct {
			Limit int `json:"limit,omitempty"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil || p.Limit < 0 || p.Limit > mailops.MaxQueue {
			return empty, reject(CatInvalidParams, "queue list şeması uyumsuz")
		}
		return validatedCall{mail: mailCall{limit: p.Limit}}, nil
	case OpMailLimitsSet:
		if len(req.Params) > mailops.MaxPayloadBytes {
			return empty, reject(CatInvalidParams, "payload çok büyük")
		}
		var p struct {
			Limits map[string]int64 `json:"limits"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "limits şeması uyumsuz")
		}
		if len(p.Limits) == 0 || len(p.Limits) > len(mailops.LimitKeyNames()) {
			return empty, reject(CatInvalidParams, "limits kümesi boş veya aşırı")
		}
		for k, v := range p.Limits {
			if !mailops.ValidLimitKey(k, v) {
				return empty, reject(CatInvalidParams, "limit anahtarı/değeri allowlist dışı: "+k)
			}
		}
		return validatedCall{mail: mailCall{limits: p.Limits}}, nil

	// ---- Faz 12: VM ailesi (ADR-0016) ----
	case OpVMPreflight, OpVMStatus:
		if err := emptyObject(req.Params); err != nil {
			return empty, reject(CatInvalidParams, "vm şeması uyumsuz (boş obje beklenir)")
		}
		return empty, nil
	case OpVMList:
		var p struct {
			Limit int `json:"limit,omitempty"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil || p.Limit < 0 || p.Limit > vmops.MaxList {
			return empty, reject(CatInvalidParams, "vm list şeması uyumsuz")
		}
		return validatedCall{vm: vmCall{limit: p.Limit}}, nil
	case OpVMInfo, OpVMStart, OpVMReboot, OpVMDelete, OpVMDiskList, OpVMSnapshotList:
		var p struct {
			Name string `json:"name"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "vm ad şeması uyumsuz")
		}
		if !vmops.ValidVMName(p.Name) {
			return empty, reject(CatInvalidParams, "VM adı allowlist dışı")
		}
		return validatedCall{vm: vmCall{name: p.Name}, target: p.Name}, nil
	case OpVMCreate:
		var p struct {
			Name    string `json:"name"`
			VCPU    int64  `json:"vcpu"`
			MemMiB  int64  `json:"mem_mib"`
			DiskGiB int64  `json:"disk_gib"`
			Pool    string `json:"pool"`
			Bridge  string `json:"bridge"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "vm create şeması uyumsuz")
		}
		if !vmops.ValidVMName(p.Name) || !vmops.ValidPoolName(p.Pool) || !vmops.ValidBridge(p.Bridge) {
			return empty, reject(CatInvalidParams, "vm create adları allowlist dışı")
		}
		if !vmops.ValidVCPU(p.VCPU) || !vmops.ValidMemMiB(p.MemMiB) || !vmops.ValidDiskGiB(p.DiskGiB) {
			return empty, reject(CatInvalidParams, "vm kaynak limitleri aralık dışı (vCPU 1..64, RAM 128..262144 MiB, disk 1..4096 GiB)")
		}
		return validatedCall{vm: vmCall{name: p.Name, vcpu: p.VCPU, memMiB: p.MemMiB, diskGiB: p.DiskGiB, pool: p.Pool, bridge: p.Bridge}, target: p.Name}, nil
	case OpVMClone:
		var p struct {
			Source string `json:"source"`
			Name   string `json:"name"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "vm clone şeması uyumsuz")
		}
		if !vmops.ValidVMName(p.Source) || !vmops.ValidVMName(p.Name) || p.Source == p.Name {
			return empty, reject(CatInvalidParams, "klon adları allowlist dışı")
		}
		return validatedCall{vm: vmCall{source: p.Source, name: p.Name}, target: p.Source + "->" + p.Name}, nil
	case OpVMStop:
		var p struct {
			Name  string `json:"name"`
			Force bool   `json:"force,omitempty"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "vm stop şeması uyumsuz")
		}
		if !vmops.ValidVMName(p.Name) {
			return empty, reject(CatInvalidParams, "VM adı allowlist dışı")
		}
		return validatedCall{vm: vmCall{name: p.Name, force: p.Force}, target: p.Name}, nil
	case OpVMDiskAdd:
		var p struct {
			Name    string `json:"name"`
			Pool    string `json:"pool"`
			SizeGiB int64  `json:"size_gib"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "disk add şeması uyumsuz")
		}
		if !vmops.ValidVMName(p.Name) || !vmops.ValidPoolName(p.Pool) || !vmops.ValidDiskGiB(p.SizeGiB) {
			return empty, reject(CatInvalidParams, "disk add adları/boyutu allowlist dışı")
		}
		return validatedCall{vm: vmCall{name: p.Name, pool: p.Pool, diskGiB: p.SizeGiB}, target: p.Name}, nil
	case OpVMSnapshotCreate:
		var p struct {
			Name        string `json:"name"`
			Snapshot    string `json:"snapshot"`
			Description string `json:"description,omitempty"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "snapshot create şeması uyumsuz")
		}
		if !vmops.ValidVMName(p.Name) || !vmops.ValidVMName(p.Snapshot) || !vmops.ValidDescription(p.Description) {
			return empty, reject(CatInvalidParams, "snapshot adı/açıklaması allowlist dışı")
		}
		return validatedCall{vm: vmCall{name: p.Name, snapshot: p.Snapshot, description: p.Description}, target: p.Name + "/" + p.Snapshot}, nil
	case OpVMSnapshotRevert, OpVMSnapshotDelete:
		var p struct {
			Name     string `json:"name"`
			Snapshot string `json:"snapshot"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "snapshot şeması uyumsuz")
		}
		if !vmops.ValidVMName(p.Name) || !vmops.ValidVMName(p.Snapshot) {
			return empty, reject(CatInvalidParams, "snapshot adları allowlist dışı")
		}
		return validatedCall{vm: vmCall{name: p.Name, snapshot: p.Snapshot}, target: p.Name + "/" + p.Snapshot}, nil

	// ---- Faz 14: yedekleme ailesi (ADR-0018) ----
	case OpBackupRepoInit:
		if len(req.Params) > 0 {
			var p map[string]json.RawMessage
			if err := strictDecode(req.Params, &p); err != nil || len(p) != 0 {
				return empty, reject(CatInvalidParams, "repo.init şeması uyumsuz (boş obje beklenir)")
			}
		}
		return empty, nil
	case OpBackupRun:
		var p struct {
			Paths []string `json:"paths,omitempty"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "backup run şeması uyumsuz")
		}
		if err := backupops.ValidateRun(backupops.RunPayload{Paths: p.Paths}); err != nil {
			return empty, reject(CatInvalidParams, "yedek kaynak yolları allowlist dışı")
		}
		return validatedCall{backup: backupCall{paths: p.Paths}}, nil
	case OpBackupSnapshotList:
		var p struct {
			Limit int `json:"limit,omitempty"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "snapshot list şeması uyumsuz")
		}
		if err := backupops.ValidateSnapshotList(backupops.SnapshotListPayload{Limit: p.Limit}); err != nil {
			return empty, reject(CatInvalidParams, "snapshot list limiti allowlist dışı")
		}
		return validatedCall{backup: backupCall{limit: p.Limit}}, nil
	case OpBackupVerify:
		var p struct {
			BackupID string `json:"backup_id"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "verify şeması uyumsuz")
		}
		if err := backupops.ValidateVerify(backupops.VerifyPayload{BackupID: p.BackupID}); err != nil {
			return empty, reject(CatInvalidParams, "backup_id allowlist dışı")
		}
		return validatedCall{backup: backupCall{backupID: p.BackupID}}, nil
	case OpBackupRestore:
		var p struct {
			BackupID string `json:"backup_id"`
			Snapshot string `json:"snapshot"`
		}
		if err := strictDecode(orEmpty(req.Params), &p); err != nil {
			return empty, reject(CatInvalidParams, "restore şeması uyumsuz")
		}
		if err := backupops.ValidateRestore(backupops.RestorePayload{BackupID: p.BackupID, Snapshot: p.Snapshot}); err != nil {
			return empty, reject(CatInvalidParams, "restore kimlikleri allowlist dışı")
		}
		return validatedCall{backup: backupCall{backupID: p.BackupID, snapshot: p.Snapshot}}, nil

	case "":
		return empty, reject(CatInvalidRequest, "operasyon boş")
	default:
		return empty, reject(CatUnsupportedOp, "operasyon allowlist dışı")
	}
}

// orEmpty normalizes a missing params field to the empty JSON object.
func orEmpty(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}

// emptyObject enforces the params to be exactly {}.
func emptyObject(raw json.RawMessage) error {
	var p struct{}
	return strictDecode(orEmpty(raw), &p)
}

// removeKind maps a preview-only remove op to its resource kind.
func removeKind(op string) string {
	switch op {
	case OpDockerContainerRemove:
		return "container"
	case OpDockerImageRemove:
		return "image"
	case OpDockerVolumeRemove:
		return "volume"
	case OpDockerNetworkRemove:
		return "network"
	}
	return ""
}

// strictDecode decodes JSON disallowing unknown fields and trailing data.
func strictDecode(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("sonrası veri")
	}
	return nil
}

type rejectError struct {
	category string
	msg      string
}

func reject(category, msg string) error { return rejectError{category, msg} }

func (e rejectError) Error() string { return e.msg }

// Category extracts a stable error category from an error.
func Category(err error) string {
	var re rejectError
	if errors.As(err, &re) {
		return re.category
	}
	return CatInternal
}

// requestIDRE bounds identifiers echoed into audit records.
var requestIDRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// sanitizeID clamps and validates echo fields for audit safety.
func sanitizeID(s string) string {
	if requestIDRE.MatchString(s) {
		return s
	}
	return fmt.Sprintf("invalid:%d", len(s))
}
