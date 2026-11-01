package dockerx

import (
	"context"
	"os"
	"testing"
	"time"
)

// realClient returns a client for the runner's local Engine socket when the
// EXPECTED_DOCKER gate is set (CI agent-docker job). The gate CANNOT mask
// evidence: when it is set and the socket is unreachable the test FAILS —
// it never silently skips (CI-17 lesson).
func realClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("EXPECTED_DOCKER") != "1" {
		t.Skip("EXPECTED_DOCKER=1 yok — gerçek docker kanıtı CI agent-docker job'ında üretilir")
	}
	return &Client{Socket: os.Getenv("DOCKER_SOCKET")}
}

// TestRealDockerSmoke pins the negotiated identity of a REAL Engine plus the
// read families (containers/images/networks/volumes).
func TestRealDockerSmoke(t *testing.T) {
	c := realClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := c.Ping(ctx)
	if err != nil {
		t.Fatalf("gerçek docker ping başarısız (gate konmuştu — sessiz skip olmaz): %v", err)
	}
	t.Logf("gerçek docker: server=%s api=%s os=%s", p.ServerVersion, p.APIVersion, p.OSType)
	if p.ServerVersion == "" || p.APIVersion == "" {
		t.Fatal("ping kimliği boş")
	}
	n, err := c.ContainersList(ctx, 50)
	if err != nil {
		t.Fatalf("gerçek container listesi: %v", err)
	}
	t.Logf("gerçek container listesi: %d satır", len(n))
	if _, err := c.ImagesList(ctx, 50); err != nil {
		t.Fatalf("gerçek image listesi: %v", err)
	}
	if _, err := c.NetworksList(ctx, 50); err != nil {
		t.Fatalf("gerçek network listesi: %v", err)
	}
	if _, err := c.VolumesList(ctx, 50); err != nil {
		t.Fatalf("gerçek volume listesi: %v", err)
	}
	t.Logf("gerçek image/network/volume listeleri okundu")
}

// TestRealDockerLifecycle exercises the read/lifecycle adapter calls against
// a REAL disposable container the CI job created via docker CLI (busybox
// sleep). Name comes from DOCKER_TEST_CONTAINER; the CI job removes it in
// its cleanup step. Nothing outside the runner's docker is touched.
func TestRealDockerLifecycle(t *testing.T) {
	c := realClient(t)
	name := os.Getenv("DOCKER_TEST_CONTAINER")
	if name == "" {
		t.Skip("DOCKER_TEST_CONTAINER yok — lifecycle kanıtı agent-docker job'ında üretilir")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	d, err := c.ContainerInspect(ctx, name)
	if err != nil {
		t.Fatalf("gerçek inspect: %v", err)
	}
	t.Logf("gerçek inspect: %s state=%s image=%s", d.ID[:12], d.State.Status, d.Image)
	if d.State.Status != "running" {
		t.Fatalf("container running olmalı: %s", d.State.Status)
	}
	if err := c.ContainerStop(ctx, name, 5); err != nil {
		t.Fatalf("gerçek stop: %v", err)
	}
	d, err = c.ContainerInspect(ctx, name)
	if err != nil {
		t.Fatalf("stop sonrası inspect: %v", err)
	}
	if d.State.Status != "exited" {
		t.Fatalf("stop sonrası exited beklenir: %s", d.State.Status)
	}
	t.Logf("gerçek stop: %s → exited", name)
	if err := c.ContainerStart(ctx, name); err != nil {
		t.Fatalf("gerçek start: %v", err)
	}
	if err := c.ContainerRestart(ctx, name, 5); err != nil {
		t.Fatalf("gerçek restart: %v", err)
	}
	d, err = c.ContainerInspect(ctx, name)
	if err != nil {
		t.Fatalf("restart sonrası inspect: %v", err)
	}
	if d.State.Status != "running" {
		t.Fatalf("restart sonrası running beklenir: %s", d.State.Status)
	}
	lc, err := c.ContainerLogs(ctx, name, 50)
	if err != nil {
		t.Fatalf("gerçek logs: %v", err)
	}
	t.Logf("gerçek logs: stdout=%d bayt stderr=%d bayt truncated=%v", len(lc.Stdout), len(lc.Stderr), lc.Truncated)
}
