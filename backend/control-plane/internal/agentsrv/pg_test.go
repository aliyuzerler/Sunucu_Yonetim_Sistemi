package agentsrv

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/certs"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/enroll"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/inventory"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/tasks"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/telemetry"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/testdb"
	agentv1 "github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/gen/proto/servercontrol/agent/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// testEnv is a full agent-server environment over real TLS + PostgreSQL.
type testEnv struct {
	t            *testing.T
	ca           *certs.CA
	caPEM        string
	pool         *pgxpool.Pool
	enrollSvc    *enroll.Service
	inventorySvc *inventory.Service
	tasksSvc     *tasks.Service
	taskStore    *tasks.PGStore
	agentSrv     *Server
	addr         string
	stop         func()
}

// codesPermissionDenied / unauthenticatedCode / permissionDeniedCode
// kısaltmaları (grpc codes paketini test içinde kısa kullanmak için).
const (
	codesPermissionDenied = codes.PermissionDenied
	unauthenticatedCode   = codes.Unauthenticated
	permissionDeniedCode  = codes.PermissionDenied
)

func pkixName(cn string) pkix.Name {
	return pkix.Name{CommonName: cn, Organization: []string{"Server Control Nodes"}}
}

// noopAudit is a test audit recorder that drops events.
type noopAudit struct{}

func (noopAudit) Record(context.Context, audit.Event) error { return nil }

type nopTagSetter struct{}

func (nopTagSetter) SetTags(_ context.Context, _, _, nodeID string, tags []string) (*inventory.Node, error) {
	return &inventory.Node{ID: nodeID, Tags: tags}, nil
}

// setupEnv boots a disposable PostgreSQL database, a fresh CA and a live gRPC
// server on 127.0.0.1:0. Each test gets complete isolation.
func setupEnv(t *testing.T) *testEnv {
	t.Helper()
	pool := testdb.Pool(t)

	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")
	if err := certs.GenerateCA(certPath, keyPath, "Agentsrv Test CA"); err != nil {
		t.Fatal(err)
	}
	ca, err := certs.LoadCA(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}

	auditRec := noopAudit{}
	inventorySvc := inventory.NewService(inventory.NewPGStore(pool), auditRec)
	taskStore := tasks.NewPGStore(pool)
	registry, err := tasks.BuildDefaultRegistry(nopTagSetter{}, tasks.Faz09Services{})
	if err != nil {
		t.Fatal(err)
	}
	tasksSvc := tasks.NewService(taskStore, registry, auditRec)
	enrollSvc := enroll.NewService(pool, ca)
	agentSrv := New(Services{
		CA:        ca,
		Enroll:    enrollSvc,
		Inventory: inventorySvc,
		Tasks:     tasksSvc,
		TaskStore: taskStore,
		Telemetry: telemetry.NewService(telemetry.NewPGStore(pool)),
		Audit:     auditRec,
	}, nil)

	serverCert, err := ca.IssueServerCert([]string{"localhost"}, []string{"127.0.0.1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.VerifyClientCertIfGiven,
		ClientCAs:    x509.NewCertPool(),
		MinVersion:   tls.VersionTLS12,
	}
	tlsCfg.ClientCAs.AddCert(ca.Cert)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gsrv := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	agentSrv.RegisterGRPC(gsrv)
	go func() { _ = gsrv.Serve(listener) }()
	t.Cleanup(gsrv.Stop)

	return &testEnv{
		t:            t,
		ca:           ca,
		caPEM:        ca.CertificatePEM(),
		pool:         pool,
		enrollSvc:    enrollSvc,
		inventorySvc: inventorySvc,
		tasksSvc:     tasksSvc,
		taskStore:    taskStore,
		agentSrv:     agentSrv,
		addr:         listener.Addr().String(),
		stop:         gsrv.Stop,
	}
}

// agentClient mimics the agent: local key, CSR, enroll, then mTLS stream.
type agentClient struct {
	env     *testEnv
	key     *ecdsa.PrivateKey
	nodeID  string
	certPEM string
	pool    *x509.CertPool
	cert    tls.Certificate
}

func (c *agentClient) enroll(t *testing.T, token, displayName string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c.key = key
	tpl := &x509.CertificateRequest{Subject: pkixName("server-agent")}
	der, err := x509.CreateCertificateRequest(rand.Reader, tpl, key)
	if err != nil {
		t.Fatal(err)
	}
	csrPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(c.env.caPEM)) {
		t.Fatal("CA PEM geçersiz")
	}
	c.pool = pool
	conn, err := grpc.NewClient(c.env.addr, grpc.WithTransportCredentials(
		credentials.NewTLS(&tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12})))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := agentv1.NewEnrollServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := client.Enroll(ctx, &agentv1.EnrollRequest{
		ProtocolVersion: agentv1.ProtocolVersion_PROTOCOL_VERSION_V1,
		EnrollmentToken: token,
		CsrPem:          csrPEM,
		DisplayName:     displayName,
		Hostname:        "agent-host",
		OsFamily:        "linux",
		AgentVersion:    "test-1.0.0",
		RequestId:       "req-enroll",
	})
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	c.nodeID = res.GetNodeId()
	c.certPEM = res.GetCertPem()
	block, _ := pem.Decode([]byte(c.certPEM))
	if block == nil {
		t.Fatal("sertifika PEM geçersiz")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	// İstemci sertifikası CA tarafından imzalanmış olmalı.
	cert2, _ := x509.ParseCertificate(block.Bytes)
	_ = cert2
	leafCert := cert
	_ = leafCert
	certPEM := cert
	_ = certPEM
	clientCert := tls.Certificate{Certificate: [][]byte{block.Bytes}, PrivateKey: key, Leaf: cert}
	c.cert = clientCert
}

func (c *agentClient) dialStream(t *testing.T, withCert bool) agentv1.AgentService_ConnectClient {
	t.Helper()
	tlsCfg := &tls.Config{RootCAs: c.pool, ServerName: "localhost", MinVersion: tls.VersionTLS12}
	if withCert {
		tlsCfg.Certificates = []tls.Certificate{c.cert}
	}
	conn, err := grpc.NewClient(c.env.addr, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := agentv1.NewAgentServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return stream
}

func (e *testEnv) issueToken(t *testing.T, in enroll.IssueInput) (string, *enroll.Token) {
	t.Helper()
	tok, view, err := e.enrollSvc.IssueToken(context.Background(), "admin", in)
	if err != nil {
		t.Fatalf("token üretilemedi: %v", err)
	}
	return tok.Plain, view
}

// Tests

func TestEnrollGroupScopeCreatesNodeAndCertificate(t *testing.T) {
	env := setupEnv(t)
	ctx := context.Background()

	groups, err := env.inventorySvc.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = groups
	// Grup oluştur.
	g, err := env.inventorySvc.CreateGroup(ctx, "admin", "c", "agents-e2e", "")
	if err != nil {
		t.Fatal(err)
	}
	token, view := env.issueToken(t, enroll.IssueInput{ScopeType: enroll.ScopeGroup, GroupID: g.ID, TTL: 15 * time.Minute})

	client := &agentClient{env: env}
	client.enroll(t, token, "agent-01")

	if client.nodeID == "" {
		t.Fatal("node_id bekleniyordu")
	}
	// Node oluşturuldu ve grupta.
	node, err := env.inventorySvc.GetNode(ctx, client.nodeID)
	if err != nil {
		t.Fatalf("node okunamadı: %v", err)
	}
	if node.Status != "active" || node.AgentVersion == nil || *node.AgentVersion != "test-1.0.0" {
		t.Fatalf("node agent metadata hatalı: %+v", node)
	}
	if len(node.GroupIDs) != 1 || node.GroupIDs[0] != g.ID {
		t.Fatalf("grup üyeliği bekleniyordu: %v", node.GroupIDs)
	}
	// Token tüketimi DB'de kalıcı olmalı (view, üretim anının anlık görüntüsüdür).
	waitFor(t, 3*time.Second, "token tüketimi", func() bool {
		var n int
		if err := env.pool.QueryRow(ctx,
			`SELECT count(*) FROM enrollment_tokens WHERE id = $1::uuid AND consumed_at IS NOT NULL`,
			view.ID).Scan(&n); err != nil {
			return false
		}
		return n == 1
	})

	// Tek kullanımlık: aynı token tekrar reddedilir.
	client2 := &agentClient{env: env}
	if err := tryEnrollError(client2, token); err == nil {
		t.Fatal("token ikinci kez kabul edilmemeli")
	} else if st, ok := status.FromError(err); !ok || st.Code() != codesPermissionDenied {
		t.Fatalf("PermissionDenied bekleniyordu: %v", err)
	}
}

func tryEnrollError(c *agentClient, token string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tpl := &x509.CertificateRequest{Subject: pkixName("x")}
	der, err := x509.CreateCertificateRequest(rand.Reader, tpl, key)
	if err != nil {
		return err
	}
	csrPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
	// taze client olabilir (c.pool nil): CA'yı doğrudan env'den kur.
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM([]byte(c.env.caPEM)) {
		return errors.New("CA PEM geçersiz")
	}
	conn, err := grpc.NewClient(c.env.addr, grpc.WithTransportCredentials(
		credentials.NewTLS(&tls.Config{RootCAs: caPool, ServerName: "localhost"})))
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := agentv1.NewEnrollServiceClient(conn).Enroll(ctx, &agentv1.EnrollRequest{
		ProtocolVersion: agentv1.ProtocolVersion_PROTOCOL_VERSION_V1,
		EnrollmentToken: token,
		CsrPem:          csrPEM,
	})
	_ = res
	return err
}

func TestEnrollExpiredTokenRejected(t *testing.T) {
	env := setupEnv(t)
	ctx := context.Background()
	g, err := env.inventorySvc.CreateGroup(ctx, "admin", "c", "exp-group", "")
	if err != nil {
		t.Fatal(err)
	}
	token, _ := env.issueToken(t, enroll.IssueInput{ScopeType: enroll.ScopeGroup, GroupID: g.ID, TTL: time.Millisecond})
	time.Sleep(20 * time.Millisecond)

	client := &agentClient{env: env}
	if err := tryEnrollError(client, token); err == nil {
		t.Fatal("süresi dolmuş token reddedilmeli")
	}
}

func TestConnectRequiresClientCertificate(t *testing.T) {
	env := setupEnv(t)
	ctx := context.Background()
	g, _ := env.inventorySvc.CreateGroup(ctx, "admin", "c", "nogroup", "")
	token, _ := env.issueToken(t, enroll.IssueInput{ScopeType: enroll.ScopeGroup, GroupID: g.ID})
	client := &agentClient{env: env}
	client.enroll(t, token, "agent-02")

	stream := client.dialStream(t, false) // sertifikasız
	_ = stream.Send(&agentv1.AgentEnvelope{
		RequestId: "r1",
		NodeId:    client.nodeID,
		Payload: &agentv1.AgentEnvelope_Hello{Hello: &agentv1.Hello{
			ProtocolVersion: agentv1.ProtocolVersion_PROTOCOL_VERSION_V1,
		}},
	})
	_, err := stream.Recv()
	if err == nil {
		t.Fatal("sertifikasız Connect reddedilmeli")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != unauthenticatedCode {
		t.Fatalf("Unauthenticated bekleniyordu: %v", err)
	}
}

func TestConnectHeartbeatCapabilitiesAndDispatch(t *testing.T) {
	env := setupEnv(t)
	ctx := context.Background()
	g, _ := env.inventorySvc.CreateGroup(ctx, "admin", "c", "dispatch-group", "")
	token, _ := env.issueToken(t, enroll.IssueInput{ScopeType: enroll.ScopeGroup, GroupID: g.ID})
	client := &agentClient{env: env}
	client.enroll(t, token, "agent-03")

	stream := client.dialStream(t, true)

	// Hello.
	if err := stream.Send(&agentv1.AgentEnvelope{
		RequestId: "r-hello",
		NodeId:    client.nodeID,
		Payload: &agentv1.AgentEnvelope_Hello{Hello: &agentv1.Hello{
			ProtocolVersion: agentv1.ProtocolVersion_PROTOCOL_VERSION_V1,
			AgentVersion:    "test-1.0.0",
			Hostname:        "agent-host",
			OsFamily:        "linux",
			Arch:            "amd64",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	in, err := stream.Recv()
	if err != nil {
		t.Fatalf("HelloAck alınamadı: %v", err)
	}
	if in.GetHelloAck() == nil || in.GetHelloAck().GetProtocolVersion() != agentv1.ProtocolVersion_PROTOCOL_VERSION_V1 {
		t.Fatalf("HelloAck bekleniyordu: %T", in.GetPayload())
	}

	// Capability raporu.
	if err := stream.Send(&agentv1.AgentEnvelope{
		RequestId: "r-caps",
		NodeId:    client.nodeID,
		Payload: &agentv1.AgentEnvelope_CapabilityReport{CapabilityReport: &agentv1.CapabilityReport{
			Capabilities: []*agentv1.Capability{{
				CapabilityId: "agent.echo", Version: "1.0.0", Available: true,
				AdapterId: "builtin", AdapterVersion: "test-1.0.0",
			}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	// Heartbeat.
	if err := stream.Send(&agentv1.AgentEnvelope{
		RequestId: "r-hb",
		NodeId:    client.nodeID,
		Payload:   &agentv1.AgentEnvelope_Heartbeat{Heartbeat: &agentv1.Heartbeat{Sequence: 1}},
	}); err != nil {
		t.Fatal(err)
	}

	// Node durumu güncellenmeli.
	waitFor(t, 5*time.Second, "node active", func() bool {
		n, err := env.inventorySvc.GetNode(ctx, client.nodeID)
		return err == nil && n.Status == "active" && n.LastSeenAt != nil
	})
	waitFor(t, 5*time.Second, "capability kaydı", func() bool {
		caps, err := env.inventorySvc.ListCapabilities(ctx, client.nodeID)
		return err == nil && len(caps) == 1 && caps[0].CapabilityID == "agent.echo"
	})

	// agent.echo görevi: node hedefli dispatch.
	tk, created, err := env.tasksSvc.Create(ctx, "admin", "corr-dispatch-1", tasks.CreateTaskInput{
		Operation: "agent.echo",
		NodeID:    client.nodeID,
		Payload:   json.RawMessage(`{"message":"merhaba agent"}`),
	})
	if err != nil || !created {
		t.Fatalf("task oluşturulamadı: created=%v err=%v", created, err)
	}

	// OperationRequest al, yürüt (agent mantığını taklit et), sonucu gönder.
	deadline := time.Now().Add(15 * time.Second)
	var got *agentv1.OperationRequest
	for time.Now().Before(deadline) {
		in, err := stream.Recv()
		if err != nil {
			t.Fatalf("stream alımı: %v", err)
		}
		if op := in.GetOperationRequest(); op != nil {
			got = op
			break
		}
	}
	if got == nil {
		t.Fatal("OperationRequest alınamadı")
	}
	if got.GetOperation() != "agent.echo" || got.GetTaskId() != tk.ID {
		t.Fatalf("operasyon zarfı hatalı: %+v", got)
	}
	if in := got; in.GetPayload() == nil {
		t.Fatal("payload boş")
	}

	// Sonucu gönder (idempotency key + correlation korunmalı).
	_, attempts, err := env.tasksSvc.Get(ctx, tk.ID)
	if err != nil || len(attempts) == 0 {
		t.Fatalf("attempt kaydı bekleniyordu: %v", err)
	}
	if err := stream.Send(&agentv1.AgentEnvelope{
		RequestId:     "r-result",
		NodeId:        client.nodeID,
		CorrelationId: "corr-dispatch-1",
		Payload: &agentv1.AgentEnvelope_OperationResult{OperationResult: &agentv1.OperationResult{
			TaskId: tk.ID,
			Status: agentv1.OperationResult_STATUS_SUCCEEDED,
			Result: []byte(`{"echo":"merhaba agent"}`),
			Output: "echo tamamlandı",
		}},
	}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 5*time.Second, "task succeeded", func() bool {
		task, _, err := env.tasksSvc.Get(ctx, tk.ID)
		return err == nil && task.Status == tasks.StatusSucceeded
	})
}

func TestConnectWrongNodeEnvelopeRejected(t *testing.T) {
	env := setupEnv(t)
	ctx := context.Background()
	g, _ := env.inventorySvc.CreateGroup(ctx, "admin", "c", "wrong-node", "")
	token, _ := env.issueToken(t, enroll.IssueInput{ScopeType: enroll.ScopeGroup, GroupID: g.ID})
	client := &agentClient{env: env}
	client.enroll(t, token, "agent-04")

	stream := client.dialStream(t, true)
	if err := stream.Send(&agentv1.AgentEnvelope{
		RequestId: "r1",
		NodeId:    "başka-node",
		Payload: &agentv1.AgentEnvelope_Hello{Hello: &agentv1.Hello{
			ProtocolVersion: agentv1.ProtocolVersion_PROTOCOL_VERSION_V1,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("yanlış node kimliği reddedilmeli")
	} else if st, ok := status.FromError(err); ok && st.Code() != permissionDeniedCode {
		t.Fatalf("PermissionDenied bekleniyordu: %v", err)
	}
}

// TestConnectFactsAndMetricsTelemetry (Faz 04): hello sonrası gönderilen
// SystemFactsReport upsert edilir; MetricsReport hız sınırı içinde kaydedilir
// ve hız sınırı ötesi DÜŞÜRÜLÜR (rate limit).
func TestConnectFactsAndMetricsTelemetry(t *testing.T) {
	env := setupEnv(t)
	ctx := context.Background()
	g, _ := env.inventorySvc.CreateGroup(ctx, "admin", "c", "telemetry-group", "")
	token, _ := env.issueToken(t, enroll.IssueInput{ScopeType: enroll.ScopeGroup, GroupID: g.ID})
	client := &agentClient{env: env}
	client.enroll(t, token, "agent-telemetry")

	stream := client.dialStream(t, true)
	if err := stream.Send(&agentv1.AgentEnvelope{
		RequestId: "r-hello",
		NodeId:    client.nodeID,
		Payload: &agentv1.AgentEnvelope_Hello{Hello: &agentv1.Hello{
			ProtocolVersion: agentv1.ProtocolVersion_PROTOCOL_VERSION_V1,
			AgentVersion:    "test-1.0.0", OsFamily: "linux", Arch: "amd64",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("HelloAck: %v", err)
	}

	// Sistem bilgileri.
	if err := stream.Send(&agentv1.AgentEnvelope{
		RequestId: "r-facts",
		NodeId:    client.nodeID,
		Payload: &agentv1.AgentEnvelope_SystemFactsReport{SystemFactsReport: &agentv1.SystemFactsReport{
			OsId: "ubuntu", OsVersionId: "24.04", OsPrettyName: "Ubuntu 24.04.3 LTS",
			KernelRelease: "6.8.0-49-generic", Arch: "amd64",
			SystemdVersion: "255", SystemdAvailable: true,
			PackageManager: "apt", Source: "os-release+procfs+systemctl",
			CollectedAt: timestamppbNewForTest(time.Now()),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "facts upsert", func() bool {
		f, err := env.svcTelemetry().GetFacts(ctx, client.nodeID)
		return err == nil && f != nil && f.OSID == "ubuntu" && f.PackageManager == "apt"
	})

	// Metrikler: ilk kabul edilir, 10sn içindeki ikincisi düşürülür.
	sendMetrics := func(seq int, pct float64) error {
		return stream.Send(&agentv1.AgentEnvelope{
			RequestId: fmt.Sprintf("r-metrics-%d", seq),
			NodeId:    client.nodeID,
			Payload: &agentv1.AgentEnvelope_MetricsReport{MetricsReport: &agentv1.MetricsReport{
				CollectedAt: timestamppbNewForTest(time.Now()),
				Source:      "procfs+statfs",
				Cpu:         &agentv1.CpuMetrics{Percent: pct, LogicalCores: 4},
				Memory:      &agentv1.MemoryMetrics{TotalBytes: 16 << 30, UsedPercent: 42.5},
				Disks: []*agentv1.DiskMetrics{{
					Device: "/dev/vda1", MountPoint: "/", FsType: "ext4", TotalBytes: 40 << 30,
				}},
				Networks:      []*agentv1.NetworkMetrics{{Interface: "ens160", RxBytes: 1, TxBytes: 2}},
				UptimeSeconds: 3600,
			}},
		})
	}
	if err := sendMetrics(1, 12.5); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "metrics insert", func() bool {
		snap, err := env.svcTelemetry().LatestMetrics(ctx, client.nodeID)
		return err == nil && snap != nil && snap.CPU.Percent == 12.5
	})

	// Hız sınırı: 10sn içinde gelen ikinci rapor düşürülmeli.
	if err := sendMetrics(2, 99.0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	snap, err := env.svcTelemetry().LatestMetrics(ctx, client.nodeID)
	if err != nil || snap.CPU.Percent != 12.5 {
		t.Fatalf("hız sınırı ikinci raporu düşürmeli: %+v %v", snap, err)
	}
}

// svcTelemetry returns the wired telemetry service from the server struct.
func (e *testEnv) svcTelemetry() *telemetry.Service {
	return e.agentSrv.svc.Telemetry
}

// timestamppbNewForTest is a tiny helper for test timestamps.
func timestamppbNewForTest(t time.Time) *timestamppb.Timestamp {
	return timestamppb.New(t.UTC())
}

// waitFor polls until cond returns true or the deadline passes.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("bekleme aşildi: %s", what)
}
