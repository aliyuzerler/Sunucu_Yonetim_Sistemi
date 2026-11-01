//go:build unix

package broker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"syscall"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fwops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/pkgops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/svcops"
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
func (s *Server) execute(req *Request, unit string, listLimit int, preview fwops.PreviewParams) *Response {
	res := &Response{Status: StatusFailed}
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
		case errors.Is(err, pkgops.ErrPkgUnavailable), errors.Is(err, fwops.ErrPreviewUnsupported):
			res.Status = StatusRejected
			res.ErrorCategory = CatUnsupportedOp
		case errors.Is(err, pkgops.ErrLimit), errors.Is(err, fwops.ErrInvalidPreview):
			res.Status = StatusRejected
			res.ErrorCategory = CatInvalidParams
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
