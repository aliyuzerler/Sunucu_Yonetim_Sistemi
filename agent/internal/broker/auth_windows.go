//go:build windows

package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
	"unsafe"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fwops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/winops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/winscm"
	"golang.org/x/sys/windows"
)

// PeerIdentity is the kernel-verified caller identity.
type PeerIdentity struct {
	Unix bool // false on windows
	UID  uint32
	GID  uint32
	PID  int32
	SID  string // canonical "S-1-…" of the caller
}

// identifyPeer resolves the caller SID via named-pipe impersonation
// (kernel-guaranteed; no PID-reuse race). The agent client opens the pipe
// with SECURITY_IDENTIFICATION, so the broker can identify but never act AS
// the caller. Any other transport is rejected.
func identifyPeer(conn net.Conn) (PeerIdentity, error) {
	pc, ok := conn.(*pipeConn)
	if !ok {
		return PeerIdentity{}, errors.New("named pipe dışı taşıyıcı reddedilir")
	}
	sid, err := pipeCallerSID(pc.handle)
	if err != nil {
		return PeerIdentity{}, err
	}
	return PeerIdentity{SID: sid}, nil
}

// peerAllowed checks the SID allowlist (default deny; canonical strings).
func (s *Server) peerAllowed(ident PeerIdentity) bool {
	for _, sid := range s.cfg.AllowedSIDs {
		if sid == ident.SID {
			return true
		}
	}
	return false
}

var (
	advapi32                 = windows.NewLazySystemDLL("advapi32.dll")
	procImpersonateNamedPipe = advapi32.NewProc("ImpersonateNamedPipeClient")
)

// pipeCallerSID impersonates the pipe caller just long enough to read the
// thread token's user SID, then reverts. Token is at IDENTIFICATION level
// (the client's SQoS), so identity query works but no action can be taken
// as the caller.
func pipeCallerSID(h windows.Handle) (string, error) {
	if err := procImpersonateNamedPipe.Find(); err != nil {
		return "", fmt.Errorf("impersonation API yok: %w", err)
	}
	r1, _, _ := procImpersonateNamedPipe.Call(uintptr(h))
	if r1 == 0 {
		return "", errors.New("ImpersonateNamedPipeClient başarısız")
	}
	defer func() { _ = windows.RevertToSelf() }()

	// The pipe thread is the current thread during a synchronous Handle.
	thread, err := windows.GetCurrentThread()
	if err != nil {
		return "", err
	}
	var token windows.Token
	if err := windows.OpenThreadToken(thread, windows.TOKEN_QUERY, true, &token); err != nil {
		return "", fmt.Errorf("thread token alınamadı: %w", err)
	}
	defer token.Close()

	var needed uint32
	_ = windows.GetTokenInformation(token, windows.TokenUser, nil, 0, &needed)
	if needed == 0 || needed > 4<<10 {
		return "", fmt.Errorf("token bilgisi boyutu şüpheli: %d", needed)
	}
	buf := make([]byte, needed)
	if err := windows.GetTokenInformation(token, windows.TokenUser, &buf[0], needed, &needed); err != nil {
		return "", fmt.Errorf("token bilgisi okunamadı: %w", err)
	}
	tu := (*windows.Tokenuser)(unsafe.Pointer(&buf[0]))
	if tu.User.Sid == nil {
		return "", errors.New("SID boş")
	}
	return tu.User.Sid.String(), nil
}

// execute runs the validated windows operation with bounded concurrency +
// timeout. Faz 06 linux survey ops never reach here: validateRequest
// validates them, but this windows dispatcher has no member for the linux
// family — they fall through to the default unsupported rejection.
func (s *Server) execute(req *Request, name string, listLimit int, _ fwops.PreviewParams) *Response {
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
	scm := s.cfg.Scm
	if scm == nil {
		return &Response{V: ProtocolV1, Status: StatusRejected, ErrorCategory: CatUnsupportedOp,
			Output: "SCM runner yapılandırılmadı"}
	}

	var result any
	var err error
	switch req.Operation {
	case OpWinServiceList:
		var services []winops.ServiceInfo
		services, err = scm.List(ctx)
		if services != nil && len(services) > listLimit {
			services = services[:listLimit]
		}
		result = map[string]any{"services": services}
	case OpWinServiceStatus:
		var si winops.ServiceInfo
		si, err = scm.Status(ctx, name)
		result = map[string]any{"service": si}
	case OpWinServiceStart:
		err = scm.Start(ctx, name)
		result = map[string]any{"service": name, "action": "start"}
	case OpWinServiceStop:
		err = scm.Stop(ctx, name)
		result = map[string]any{"service": name, "action": "stop"}
	case OpWinServiceRestart:
		err = scm.Restart(ctx, name)
		result = map[string]any{"service": name, "action": "restart"}
	default:
		// validateRequest bu ailenin dışına çıkmamalı; savunma amaçlı:
		// linux operasyonları windows broker'da YOK.
		res.Status = StatusRejected
		res.ErrorCategory = CatUnsupportedOp
		return res
	}

	if err != nil {
		res.Output = clampStr(err.Error(), 500)
		switch {
		case errors.Is(err, winops.ErrInvalidService):
			res.Status = StatusRejected
			res.ErrorCategory = CatInvalidParams
		case errors.Is(err, winscm.ErrTimeout):
			res.Status = StatusFailed
			res.ErrorCategory = CatExecutorTimedOut
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
