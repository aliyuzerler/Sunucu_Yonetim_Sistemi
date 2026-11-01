//go:build windows

// Package winscm implements the Faz 05 Windows Service Control Manager
// adapter. It talks to the SCM via golang.org/x/sys/windows/svc (structured
// API — never a shell, never sc.exe output parsing; ADR-0003/ADR-0009).
//
// Security: the CALLER (privileged broker / control-plane validators) owns
// the service-name allowlist, identity check and audit; this package only
// performs bounded, typed SCM operations.
package winscm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/winops"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Operation error categories (stable API contract, mirrors svcops).
var (
	ErrInvalidService = winops.ErrInvalidService
	ErrSCMAbsent      = errors.New("servis denetim yöneticisi kullanılamıyor")
	ErrTimeout        = errors.New("işlem zaman aşımına uğradı")
)

// Manager runs SCM operations against the local machine. The zero value is
// ready; Timeout (zero = 60s default) bounds stop/start transitions.
type Manager struct {
	Timeout time.Duration
}

func (m *Manager) execTimeout() time.Duration {
	if m.Timeout > 0 {
		return m.Timeout
	}
	return 60 * time.Second
}

// connectSCM opens the SCM with the LEAST rights that satisfy list+open
// (ENUMERATE_SERVICE | CONNECT). Full ALL_ACCESS (mgr.Connect) would need
// admin even for read-only listing.
func connectSCM() (*mgr.Mgr, error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ENUMERATE_SERVICE|windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSCMAbsent, err)
	}
	return &mgr.Mgr{Handle: h}, nil
}

// statusString maps svc.State to a stable identifier.
func statusString(s svc.State) string {
	switch s {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start_pending"
	case svc.StopPending:
		return "stop_pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue_pending"
	case svc.PausePending:
		return "pause_pending"
	case svc.Paused:
		return "paused"
	default:
		return "unknown"
	}
}

// startUpString maps mgr.StartType to a stable identifier.
func startUpString(t uint32) string {
	switch t {
	case windows.SERVICE_BOOT_START:
		return "boot"
	case windows.SERVICE_SYSTEM_START:
		return "system"
	case windows.SERVICE_AUTO_START:
		return "auto"
	case windows.SERVICE_DEMAND_START:
		return "demand"
	case windows.SERVICE_DISABLED:
		return "disabled"
	default:
		return "unknown"
	}
}

// List returns services visible to the caller (bounded to 500).
func (m *Manager) List(ctx context.Context) ([]winops.ServiceInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mgr, err := connectSCM()
	if err != nil {
		return nil, err
	}
	defer mgr.Disconnect()
	names, err := mgr.ListServices()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSCMAbsent, err)
	}
	if len(names) > 500 {
		names = names[:500]
	}
	out := make([]winops.ServiceInfo, 0, len(names))
	for _, name := range names {
		if len(name) > 80 || !isValidNameShape(name) {
			continue // allowlist shape: never echo anything odd
		}
		si, err := m.queryOne(mgr, name)
		if err != nil {
			continue // status may need rights the caller lacks; skip quietly
		}
		out = append(out, si)
	}
	return out, nil
}

// Status returns one service's typed properties. Read-only.
func (m *Manager) Status(ctx context.Context, name string) (winops.ServiceInfo, error) {
	if err := winops.ValidateServiceName(name); err != nil {
		return winops.ServiceInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return winops.ServiceInfo{}, err
	}
	mgr, err := connectSCM()
	if err != nil {
		return winops.ServiceInfo{}, err
	}
	defer mgr.Disconnect()
	return m.queryOne(mgr, name)
}

func (m *Manager) queryOne(mgr *mgr.Mgr, name string) (winops.ServiceInfo, error) {
	s, err := mgr.OpenService(name)
	if err != nil {
		return winops.ServiceInfo{}, fmt.Errorf("servis açılamadı %q: %v", name, err)
	}
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		return winops.ServiceInfo{}, fmt.Errorf("durum alınamadı %q: %v", name, err)
	}
	cfg, err := s.Config()
	startUp := "unknown"
	if err == nil {
		startUp = startUpString(cfg.StartType)
	}
	return winops.ServiceInfo{
		Name:     name,
		Status:   statusString(st.State),
		StartUp:  startUp,
		ExitCode: int(st.Win32ExitCode),
		PID:      st.ProcessId,
	}, nil
}

// Start / Stop / Restart are privileged actions; the CALLER (broker) is
// responsible for the allowlist, identity check and audit.
func (m *Manager) Start(ctx context.Context, name string) error { return m.verb(ctx, name, verbStart) }
func (m *Manager) Stop(ctx context.Context, name string) error  { return m.verb(ctx, name, verbStop) }
func (m *Manager) Restart(ctx context.Context, name string) error {
	return m.verb(ctx, name, verbRestart)
}

type verbKind int

const (
	verbStart verbKind = iota
	verbStop
	verbRestart
)

func (m *Manager) verb(ctx context.Context, name string, kind verbKind) error {
	if err := winops.ValidateServiceName(name); err != nil {
		return err
	}
	mgr, err := connectSCM()
	if err != nil {
		return err
	}
	defer mgr.Disconnect()
	s, err := mgr.OpenService(name)
	if err != nil {
		return fmt.Errorf("servis açılamadı %q: %v", name, err)
	}
	defer s.Close()

	switch kind {
	case verbStart:
		return m.startAndWait(ctx, s, name)
	case verbStop:
		return m.stopAndWait(ctx, s, name)
	case verbRestart:
		if err := m.stopAndWait(ctx, s, name); err != nil {
			return err
		}
		return m.startAndWait(ctx, s, name)
	}
	return nil
}

func (m *Manager) startAndWait(ctx context.Context, s *mgr.Service, name string) error {
	if st, err := s.Query(); err == nil && st.State == svc.Running {
		return nil // idempotent start
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("start başarısız %q: %v", name, err)
	}
	if err := m.waitState(ctx, s, svc.Running); err != nil {
		return err
	}
	return nil
}

func (m *Manager) stopAndWait(ctx context.Context, s *mgr.Service, name string) error {
	st, err := s.Query()
	if err != nil {
		return fmt.Errorf("durum alınamadı %q: %v", name, err)
	}
	if st.State == svc.Stopped {
		return nil // idempotent stop
	}
	if _, err := s.Control(svc.Stop); err != nil {
		return fmt.Errorf("stop başarısız %q: %v", name, err)
	}
	return m.waitState(ctx, s, svc.Stopped)
}

// waitState polls the service until it reaches want or the deadline hits.
func (m *Manager) waitState(ctx context.Context, s *mgr.Service, want svc.State) error {
	deadline := time.Now().Add(m.execTimeout())
	for {
		st, err := s.Query()
		if err != nil {
			return fmt.Errorf("durum yoklaması başarısız: %v", err)
		}
		if st.State == want {
			return nil
		}
		// Pending durumlar dışında beklemek anlamsız.
		if want == svc.Running && st.State != svc.StartPending {
			if st.State == svc.Stopped {
				return fmt.Errorf("%w: servis start beklerken durdu", ErrTimeout)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: servis %v durumuna geçmedi", ErrTimeout, statusString(want))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// isValidNameShape is a cheap pre-filter for List output (mirror of the
// allowlist without allocations).
func isValidNameShape(name string) bool {
	return winops.ValidateServiceName(name) == nil
}
