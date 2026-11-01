//go:build unix

package broker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"syscall"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/dockerx"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fwops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/pkgops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/svcops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/webops"
)

// PeerIdentity is the kernel-verified caller identity.
type PeerIdentity struct {
	Unix bool
	UID  uint32
	GID  uint32
	PID  int32
	SID  string // unused on unix
}

// identifyPeer extracts SO_PEERCRED credentials from a Unix connection.
// Any OTHER transport is rejected (no test fallback in production code).
func identifyPeer(conn net.Conn) (PeerIdentity, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return PeerIdentity{}, errors.New("unix socket dışı taşıyıcı reddedilir")
	}
	uid, gid, pid, err := peerCreds(uc)
	if err != nil {
		return PeerIdentity{}, err
	}
	return PeerIdentity{Unix: true, UID: uid, GID: gid, PID: pid}, nil
}

// peerAllowed checks the UID allowlist (default deny).
func (s *Server) peerAllowed(ident PeerIdentity) bool {
	for _, u := range s.cfg.AllowedUIDs {
		if u == ident.UID {
			return true
		}
	}
	return false
}

func peerCreds(conn *net.UnixConn) (uid, gid uint32, pid int32, err error) {
	sc, serr := conn.SyscallConn()
	if serr != nil {
		return 0, 0, 0, serr
	}
	var ucred *syscall.Ucred
	var ctlErr error
	if ctlErr = sc.Control(func(fd uintptr) {
		ucred, ctlErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); ctlErr != nil {
		return 0, 0, 0, ctlErr
	}
	if ctlErr != nil {
		return 0, 0, 0, ctlErr
	}
	if ucred == nil {
		return 0, 0, 0, errors.New("peer kimliği alınamadı")
	}
	return ucred.Uid, ucred.Gid, ucred.Pid, nil
}

// execute runs the validated linux operation with bounded concurrency +
// timeout.
func (s *Server) execute(req *Request, call validatedCall) *Response {
	unit, listLimit, preview, dx, wb := call.target, call.listLimit, call.preview, call.docker, call.web
	res := &Response{Status: StatusFailed}
	// Faz 08: web ailesi --web-enable olmadan TİPLİ olarak reddedilir
	// (kapalı = sessiz kalabalık değil, dürüst unsupported — ADR-0012).
	if isWebOp(req.Operation) && s.cfg.Web == nil {
		res.Status = StatusRejected
		res.ErrorCategory = CatUnsupportedOp
		res.Output = "web hosting ailesi kapalı (--web-enable)"
		return res
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-time.After(5 * time.Second):
		res.Status = StatusRejected
		res.ErrorCategory = CatRateLimited
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ExecTimeout)
	defer cancel()
	sysd := s.cfg.Systemd
	if sysd == nil {
		sysd = &svcops.Manager{}
	}

	var result any
	var err error
	switch req.Operation {
	case OpDockerContainerList:
		var rows []dockerx.ContainerSummary
		rows, err = s.dockerRunner().ContainersList(ctx, listLimit)
		result = map[string]any{"containers": rows}
	case OpDockerContainerInspect:
		var d dockerx.ContainerDetail
		d, err = s.dockerRunner().ContainerInspect(ctx, dx.ref)
		result = map[string]any{"container": d}
	case OpDockerContainerLogs:
		var lc dockerx.LogChunk
		lc, err = s.dockerRunner().ContainerLogs(ctx, dx.ref, dx.tail)
		result = map[string]any{"logs": lc}
	case OpDockerContainerStart:
		err = s.dockerRunner().ContainerStart(ctx, dx.ref)
		result = map[string]any{"container": dx.ref, "action": "start"}
	case OpDockerContainerStop:
		err = s.dockerRunner().ContainerStop(ctx, dx.ref, dx.timeout)
		result = map[string]any{"container": dx.ref, "action": "stop"}
	case OpDockerContainerRestart:
		err = s.dockerRunner().ContainerRestart(ctx, dx.ref, dx.timeout)
		result = map[string]any{"container": dx.ref, "action": "restart"}
	case OpDockerImageList:
		var imgs []dockerx.ImageSummary
		imgs, err = s.dockerRunner().ImagesList(ctx, listLimit)
		result = map[string]any{"images": imgs}
	case OpDockerNetworkList:
		var nets []dockerx.NetworkSummary
		nets, err = s.dockerRunner().NetworksList(ctx, listLimit)
		result = map[string]any{"networks": nets}
	case OpDockerVolumeList:
		var vols []dockerx.VolumeInfo
		vols, err = s.dockerRunner().VolumesList(ctx, listLimit)
		result = map[string]any{"volumes": vols}
	case OpDockerContainerRemove, OpDockerImageRemove, OpDockerVolumeRemove, OpDockerNetworkRemove:
		// PREVIEW-ONLY (ADR-0011): komut/hedef DATA olarak döner; DELETE
		// ASLA yapılmaz (runner arayüzünde remove metodu bile yoktur).
		p := dockerx.RemoveParams{Ref: dx.ref, Force: dx.force, Link: dx.link, V: dx.v, Prune: dx.prune}
		result = map[string]any{"preview": dockerx.RenderRemovePreview(dx.kind, dx.ref, p)}
	case OpLinuxPkgList:
		pkg := s.cfg.Pkg
		if pkg == nil {
			pkg = &pkgops.Runner{}
		}
		var pkgs []pkgops.PackageInfo
		pkgs, err = pkg.List(ctx, listLimit)
		result = map[string]any{"packages": pkgs}
	case OpLinuxFirewallStatus:
		fw := s.fwRunner()
		st := fw.Probe(ctx)
		result = map[string]any{"firewall": st}
	case OpLinuxFirewallRulesList:
		fw := s.fwRunner()
		var rs fwops.Ruleset
		rs, err = fw.Rules(ctx, listLimit)
		result = map[string]any{"ruleset": rs}
	case OpLinuxFirewallRulePreview:
		// PREVIEW-ONLY (ADR-0010): render commands as data; NEVER
		// execute. The active backend decides the syntax.
		fw := s.fwRunner()
		st := fw.Probe(ctx)
		if st.Backend == fwops.BackendNone {
			res.Status = StatusFailed
			res.ErrorCategory = CatExecutorFailed
			res.Output = "tanınan firewall backend yok"
			return res
		}
		var pv fwops.Preview
		pv, err = fwops.RenderPreview(st.Backend, preview)
		if err == nil {
			result = map[string]any{"preview": pv}
		}

	// ---- Faz 08: web hosting ailesi ----
	case OpWebSiteList:
		var rows []webops.SiteInfo
		rows, err = s.cfg.Web.List(ctx, wb.limit)
		result = map[string]any{"sites": rows}
	case OpWebSiteStatus:
		var st webops.SiteStatus
		st, err = s.cfg.Web.Status(ctx, wb.site)
		result = map[string]any{"status": st}
	case OpWebSiteCreate:
		var sr webops.SiteResult
		sr, err = s.cfg.Web.Create(ctx, *wb.spec)
		result = map[string]any{"result": sr}
	case OpWebSiteUpdate:
		var sr webops.SiteResult
		sr, err = s.cfg.Web.Update(ctx, *wb.spec)
		result = map[string]any{"result": sr}
	case OpWebSiteDisable:
		var sr webops.SiteResult
		sr, err = s.cfg.Web.Disable(ctx, wb.site)
		result = map[string]any{"result": sr}
	case OpWebSiteDelete:
		var sr webops.SiteResult
		sr, err = s.cfg.Web.Delete(ctx, wb.site)
		result = map[string]any{"result": sr}
	case OpWebPHPList:
		var m webops.PHPMatrix
		m, err = s.cfg.Web.PHPVersions(ctx)
		result = map[string]any{"php": m}
	case OpServiceList:
		var units []svcops.UnitInfo
		units, err = sysd.List(ctx)
		if units != nil && len(units) > listLimit {
			units = units[:listLimit]
		}
		result = map[string]any{"units": units}
	case OpServiceStatus:
		var u svcops.UnitInfo
		u, err = sysd.Status(ctx, unit)
		result = map[string]any{"unit": u}
	case OpServiceStart:
		err = sysd.Start(ctx, unit)
		result = map[string]any{"unit": unit, "action": "start"}
	case OpServiceStop:
		err = sysd.Stop(ctx, unit)
		result = map[string]any{"unit": unit, "action": "stop"}
	case OpServiceRestart:
		err = sysd.Restart(ctx, unit)
		result = map[string]any{"unit": unit, "action": "restart"}
	default:
		// validateRequest bu ailenin dışına çıkmamalı; savunma amaçlı:
		// windows operasyonları linux broker'da YOK.
		res.Status = StatusRejected
		res.ErrorCategory = CatUnsupportedOp
		return res
	}

	if err != nil {
		res.Output = clampStr(err.Error(), 500)
		switch {
		case errors.Is(err, svcops.ErrInvalidUnit):
			res.Status = StatusRejected
			res.ErrorCategory = CatInvalidParams
		case errors.Is(err, svcops.ErrTimeout):
			res.Status = StatusFailed
			res.ErrorCategory = CatExecutorTimedOut
		case errors.Is(err, pkgops.ErrPkgUnavailable), errors.Is(err, fwops.ErrPreviewUnsupported),
			errors.Is(err, dockerx.ErrUnsupportedAPI), errors.Is(err, dockerx.ErrUnavailable):
			res.Status = StatusRejected
			res.ErrorCategory = CatUnsupportedOp
		case errors.Is(err, dockerx.ErrInvalidRef), errors.Is(err, dockerx.ErrLimit):
			res.Status = StatusRejected
			res.ErrorCategory = CatInvalidParams
		case errors.Is(err, pkgops.ErrLimit), errors.Is(err, fwops.ErrInvalidPreview):
			res.Status = StatusRejected
			res.ErrorCategory = CatInvalidParams
		case errors.Is(err, webops.ErrSyntaxCheck), errors.Is(err, webops.ErrNotIncluded),
			errors.Is(err, webops.ErrPortConflict), errors.Is(err, webops.ErrConflict),
			errors.Is(err, webops.ErrForeignConfig):
			// Ortam-durum redleri: istek geçerli, durum uygunsuz.
			res.Status = StatusFailed
			res.ErrorCategory = CatExecutorFailed
		case errors.Is(err, webops.ErrInvalidName), errors.Is(err, webops.ErrInvalidSpec),
			errors.Is(err, webops.ErrNotFound), errors.Is(err, webops.ErrLimit):
			res.Status = StatusRejected
			res.ErrorCategory = CatInvalidParams
		case errors.Is(err, webops.ErrPHPUnsupported), errors.Is(err, webops.ErrPHPNotInstalled):
			res.Status = StatusRejected
			res.ErrorCategory = CatUnsupportedOp
		case errors.Is(err, webops.ErrUnavailable):
			res.Status = StatusRejected
			res.ErrorCategory = CatUnsupportedOp
		default:
			res.Status = StatusFailed
			res.ErrorCategory = CatExecutorFailed
		}
		return res
	}
	b, _ := json.Marshal(result)
	res.Status = StatusSucceeded
	res.Result = b
	return res
}

// fwRunner resolves the firewall runner (zero value probes PATH).
func (s *Server) fwRunner() FirewallRunner {
	if s.cfg.Firewall != nil {
		return s.cfg.Firewall
	}
	return &fwops.Runner{}
}

// isWebOp reports whether an operation belongs to the Faz 08 web family.
func isWebOp(op string) bool {
	switch op {
	case OpWebSiteList, OpWebSiteStatus, OpWebSiteCreate, OpWebSiteUpdate,
		OpWebSiteDisable, OpWebSiteDelete, OpWebPHPList:
		return true
	}
	return false
}

// dockerRunner resolves the Docker runner (nil config → unix client with
// the configured socket; the windows pipe broker has no docker family).
func (s *Server) dockerRunner() DockerRunner {
	if s.cfg.Docker != nil {
		return s.cfg.Docker
	}
	return &dockerx.Client{Socket: s.cfg.DockerSocket}
}
