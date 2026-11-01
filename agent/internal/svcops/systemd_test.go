package svcops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSysctl is a shell script standing in for systemctl in tests. It records
// its argv (one JSON per call) and responds with canned output. Tests are the
// ONLY place a shell script appears; production code never spawns one.
const fakeScript = `#!/bin/sh
echo "$0 $*" >> "$CALLS"
echo 'systemd 255 (255.4-1ubuntu8.4)'
`

// fakeShowScript mimics `systemctl show` key=value output and list-units.
const fakeShowScript = `#!/bin/sh
echo "$0 $*" >> "$CALLS"
if [ "$1" = "list-units" ]; then
  printf '%s\n' \
    'nginx.service loaded active running A high performance web server' \
    'cron.service loaded active waiting Regular background program'
  exit 0
fi
for kv in Id=nginx.service LoadState=loaded ActiveState=active SubState=running Description="A high performance web server"; do
  echo "$kv"
done
`

func installFakeSystemctl(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(bin, []byte(script), 0o750); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls.log")
	t.Setenv("CALLS", calls)
	t.Setenv("PATH", dir)
	t.Cleanup(func() { _ = os.Remove(calls) })
	return calls
}

func readCalls(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("çağrı kaydı okunamadı: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestValidateUnit(t *testing.T) {
	valid := []string{"nginx.service", "user@1000.service", "systemd-fsck@dev-sda1.service",
		"a.service", "php8.3-fpm.service", "unattended-upgrades.service"}
	for _, u := range valid {
		if err := ValidateUnit(u); err != nil {
			t.Errorf("geçerli unit reddedildi: %s (%v)", u, err)
		}
	}
	invalid := []string{
		"", "nginx", "-bash.service", "../etc/passwd.service", "a/b.service",
		"nginx .service", "nginx;reboot.service", "nginx|reboot.service",
		"nginx&reboot.service", "nginx$(reboot).service", "nginx`id`.service",
		"nginx$IFS.service", "%x.service", "nginx.service ",
		strings.Repeat("a", 300) + ".service",
	}
	for _, u := range invalid {
		if err := ValidateUnit(u); err == nil {
			t.Errorf("geçersiz unit kabul edildi: %q", u)
		}
	}
}

func TestListAndStatusArgv(t *testing.T) {
	calls := installFakeSystemctl(t, fakeShowScript)
	m := &Manager{}
	ctx := context.Background()

	units, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(units) != 2 || units[0].ID != "nginx.service" || units[0].Description != "A high performance web server" {
		t.Fatalf("list parse: %+v", units)
	}

	u, err := m.Status(ctx, "nginx.service")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if u.ID != "nginx.service" || u.ActiveState != "active" || u.SubState != "running" || u.LoadState != "loaded" {
		t.Fatalf("status parse: %+v", u)
	}

	for _, c := range readCalls(t, calls) {
		if strings.Contains(c, ";") || strings.Contains(c, "$(") || strings.Contains(c, "`") {
			t.Fatalf("shell meta karakteri argv'ya sızmış: %q", c)
		}
	}
}

func TestVerbExecutesWithFixedArgv(t *testing.T) {
	calls := installFakeSystemctl(t, fakeScript)
	m := &Manager{}
	ctx := context.Background()
	if err := m.Start(ctx, "nginx.service"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(ctx, "nginx.service"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := m.Restart(ctx, "nginx.service"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	got := readCalls(t, calls)
	if len(got) != 3 {
		t.Fatalf("3 çağrı: %v", got)
	}
	for i, want := range []string{"start", "stop", "restart"} {
		if !strings.HasSuffix(got[i], " "+want+" nginx.service") {
			t.Fatalf("argv %d: %q", i, got[i])
		}
	}
}

func TestVerbRejectsInvalidUnitBeforeExec(t *testing.T) {
	calls := installFakeSystemctl(t, fakeScript)
	m := &Manager{}
	err := m.Start(context.Background(), "../evil.service")
	if !errors.Is(err, ErrInvalidUnit) {
		t.Fatalf("ErrInvalidUnit beklenir: %v", err)
	}
	// systemctl ÇAĞRILMAMALI.
	if b, _ := os.ReadFile(calls); len(b) != 0 {
		t.Fatalf("yürütme öncesi reddedilmeli: %q", b)
	}
}

func TestStatusRejectsInvalidUnitBeforeExec(t *testing.T) {
	calls := installFakeSystemctl(t, fakeScript)
	m := &Manager{}
	if _, err := m.Status(context.Background(), "-x.service"); !errors.Is(err, ErrInvalidUnit) {
		t.Fatalf("ErrInvalidUnit beklenir: %v", err)
	}
	if b, _ := os.ReadFile(calls); len(b) != 0 {
		t.Fatalf("yürütme öncesi reddedilmeli")
	}
}

// Systemd absent (exit failure from list-units) maps to ErrSystemdAbsent.
func TestListSystemdAbsent(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 1\n"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	m := &Manager{}
	_, err := m.List(context.Background())
	if !errors.Is(err, ErrSystemdAbsent) {
		t.Fatalf("ErrSystemdAbsent beklenir: %v", err)
	}
}

func TestVerbTimeout(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n/bin/sleep 30\n"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	m := &Manager{Timeout: 500 * time.Millisecond}
	start := time.Now()
	err := m.Restart(context.Background(), "nginx.service")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("ErrTimeout beklenir: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("timeout uygulanmalı")
	}
}
