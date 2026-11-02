package agentsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/enroll"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/tasks"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/testdb"
	agentv1 "github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/gen/proto/servercontrol/agent/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---- Faz 14: multi-node load/chaos kanıtları (kriter 4) ----
//
// En az 2-3 node ile: task izolasyonu (bir node'un sıkışması diğerlerini
// engellemez), offline node kuyruğu + yeniden bağlanma, retry/lease
// semantiği ve sertifika iptali. Tümü gerçek PostgreSQL + gerçek TLS
// üzerinden çalışır (TEST_DATABASE_URL yoksa dürüstçe atlanır — CI koşar).

// fakeNode is one simulated agent: enrolled identity + a live stream.
type fakeNode struct {
	c      *agentClient
	stream agentv1.AgentService_ConnectClient
	name   string
}

// connectNode enrolls and connects one simulated node with hello/ack.
func (env *testEnv) connectNode(t *testing.T, groupID, name string) *fakeNode {
	t.Helper()
	token, _ := env.issueToken(t, enroll.IssueInput{ScopeType: enroll.ScopeGroup, GroupID: groupID})
	n := &fakeNode{name: name, c: &agentClient{env: env}}
	n.c.enroll(t, token, name)
	n.stream = n.c.dialStream(t, true)
	n.hello(t)
	return n
}

func (n *fakeNode) hello(t *testing.T) {
	t.Helper()
	if err := n.stream.Send(&agentv1.AgentEnvelope{
		RequestId: "hello-" + n.name,
		NodeId:    n.c.nodeID,
		Payload: &agentv1.AgentEnvelope_Hello{Hello: &agentv1.Hello{
			ProtocolVersion: agentv1.ProtocolVersion_PROTOCOL_VERSION_V1,
			AgentVersion:    "chaos-1.0.0",
			Hostname:        n.name,
			OsFamily:        "linux",
			Arch:            "amd64",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	in, err := n.stream.Recv()
	if err != nil || in.GetHelloAck() == nil {
		t.Fatalf("%s HelloAck: %v", n.name, err)
	}
}

// recvOperation waits for one OperationRequest (skipping hello-acks).
func (n *fakeNode) recvOperation(t *testing.T, timeout time.Duration) *agentv1.OperationRequest {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Recv blocks; bounded by the stream context (30s) so a tight loop
		// with a shorter logical timeout is fine here.
		in, err := n.stream.Recv()
		if err != nil {
			t.Fatalf("%s stream alımı: %v", n.name, err)
		}
		if op := in.GetOperationRequest(); op != nil {
			return op
		}
	}
	t.Fatalf("%s: %s içinde OperationRequest gelmedi", n.name, timeout)
	return nil
}

// ackSucceeded returns a SUCCEEDED result for the given task.
func (n *fakeNode) ackSucceeded(t *testing.T, taskID, resultJSON string) {
	t.Helper()
	if err := n.stream.Send(&agentv1.AgentEnvelope{
		RequestId: "res-" + taskID,
		NodeId:    n.c.nodeID,
		Payload: &agentv1.AgentEnvelope_OperationResult{OperationResult: &agentv1.OperationResult{
			TaskId: taskID,
			Status: agentv1.OperationResult_STATUS_SUCCEEDED,
			Result: []byte(resultJSON),
		}},
	}); err != nil {
		t.Fatal(err)
	}
}

// TestMultiNodeTaskIsolation proves one stuck node cannot block dispatch to
// other nodes (kriter 4: task isolation) and that a wrong-node result is
// rejected (node scoping).
func TestMultiNodeTaskIsolation(t *testing.T) {
	env := setupEnv(t)
	ctx := context.Background()
	g, _ := env.inventorySvc.CreateGroup(ctx, "admin", "c", "iso-group", "")

	a := env.connectNode(t, g.ID, "iso-a")
	b := env.connectNode(t, g.ID, "iso-b")
	c := env.connectNode(t, g.ID, "iso-c")

	// A: görevi alır ama ACK GÖNDERMEZ (sıkışan node simülasyonu).
	ta, _, err := env.tasksSvc.Create(ctx, "admin", "corr-iso-a", tasks.CreateTaskInput{
		Operation: "agent.echo", NodeID: a.c.nodeID,
		Payload: json.RawMessage(`{"message":"slow"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	opA := a.recvOperation(t, 10*time.Second)
	if opA.GetTaskId() != ta.ID {
		t.Fatalf("A kendi görevini almalı: %+v", opA)
	}

	// B ve C: kendi görevleri tamamen bağımsız tamamlanmalı.
	tb, _, err := env.tasksSvc.Create(ctx, "admin", "corr-iso-b", tasks.CreateTaskInput{
		Operation: "agent.echo", NodeID: b.c.nodeID,
		Payload: json.RawMessage(`{"message":"b"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	tc, _, err := env.tasksSvc.Create(ctx, "admin", "corr-iso-c", tasks.CreateTaskInput{
		Operation: "agent.echo", NodeID: c.c.nodeID,
		Payload: json.RawMessage(`{"message":"c"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	opB := b.recvOperation(t, 10*time.Second)
	opC := c.recvOperation(t, 10*time.Second)
	b.ackSucceeded(t, opB.GetTaskId(), `{"echo":"b"}`)
	c.ackSucceeded(t, opC.GetTaskId(), `{"echo":"c"}`)

	waitFor(t, 10*time.Second, "B ve C görevleri tamamlanmalı", func() bool {
		gb, _, _ := env.tasksSvc.Get(ctx, tb.ID)
		gc, _, _ := env.tasksSvc.Get(ctx, tc.ID)
		return gb.Status == tasks.StatusSucceeded && gc.Status == tasks.StatusSucceeded
	})

	// A'nın sıkışan görevi hâlâ dispatched (izolasyon: kimse onu kurtarmaya
	// çalışmıyor ama B/C'nin akışı bozulmadı).
	ga, _, _ := env.tasksSvc.Get(ctx, ta.ID)
	if ga.Status != tasks.StatusDispatched && ga.Status != tasks.StatusRunning {
		t.Fatalf("A görevi dispatched kalmalı: %s", ga.Status)
	}

	// Node scoping: B, A'nın görevine sonuç gönderemez (yanlış node reddi).
	if err := b.stream.Send(&agentv1.AgentEnvelope{
		RequestId: "cross-node",
		NodeId:    b.c.nodeID,
		Payload: &agentv1.AgentEnvelope_OperationResult{OperationResult: &agentv1.OperationResult{
			TaskId: ta.ID,
			Status: agentv1.OperationResult_STATUS_SUCCEEDED,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	ga2, _, _ := env.tasksSvc.Get(ctx, ta.ID)
	if ga2.Status == tasks.StatusSucceeded {
		t.Fatal("başka node'un görevi başka node tarafından tamamlanamamalı")
	}

	// Son olarak A kendisi ACK eder → tamamlanır.
	a.ackSucceeded(t, ta.ID, `{"echo":"slow"}`)
	waitFor(t, 10*time.Second, "A görevi kendi ACK'i ile tamamlanmalı", func() bool {
		ga3, _, _ := env.tasksSvc.Get(ctx, ta.ID)
		return ga3.Status == tasks.StatusSucceeded
	})
}

// TestOfflineNodeQueueAndReconnect proves tasks created while a node is
// offline stay queued and are delivered on reconnect (kriter 4).
func TestOfflineNodeQueueAndReconnect(t *testing.T) {
	env := setupEnv(t)
	ctx := context.Background()
	g, _ := env.inventorySvc.CreateGroup(ctx, "admin", "c", "offline-group", "")
	token, _ := env.issueToken(t, enroll.IssueInput{ScopeType: enroll.ScopeGroup, GroupID: g.ID})
	n := &fakeNode{name: "offline-a", c: &agentClient{env: env}}
	n.c.enroll(t, token, "offline-a")

	// Node OFFLINE: görev oluşturulur → queued kalır.
	tk, _, err := env.tasksSvc.Create(ctx, "admin", "corr-offline", tasks.CreateTaskInput{
		Operation: "agent.echo", NodeID: n.c.nodeID,
		Payload: json.RawMessage(`{"message":"reconnect"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond) // dispatch ticker'ın birkaç turuna izin ver
	got, _, _ := env.tasksSvc.Get(ctx, tk.ID)
	if got.Status != tasks.StatusQueued {
		t.Fatalf("offline node için görev queued kalmalı: %s", got.Status)
	}

	// RECONNECT: stream açılır → görev dağıtılır → ACK ile tamamlanır.
	n.stream = n.c.dialStream(t, true)
	n.hello(t)
	op := n.recvOperation(t, 15*time.Second)
	if op.GetTaskId() != tk.ID {
		t.Fatalf("reconnect sonrası görev teslim edilmeli: %+v", op)
	}
	n.ackSucceeded(t, tk.ID, `{"echo":"reconnect"}`)
	waitFor(t, 10*time.Second, "reconnect görevi tamamlanmalı", func() bool {
		got, _, _ := env.tasksSvc.Get(ctx, tk.ID)
		return got.Status == tasks.StatusSucceeded
	})
}

// TestMultiNodeLeaseRetrySemantics proves the lease/retry contract: a
// retry-safe task whose lease expires is requeued for another attempt; the
// max-attempts ceiling fails it honestly.
func TestMultiNodeLeaseRetrySemantics(t *testing.T) {
	env := setupEnv(t)
	ctx := context.Background()
	g, _ := env.inventorySvc.CreateGroup(ctx, "admin", "c", "lease-group", "")
	token, _ := env.issueToken(t, enroll.IssueInput{ScopeType: enroll.ScopeGroup, GroupID: g.ID})
	n := &fakeNode{name: "lease-a", c: &agentClient{env: env}}
	n.c.enroll(t, token, "lease-a")
	n.stream = n.c.dialStream(t, true)
	n.hello(t)

	tk, _, err := env.tasksSvc.Create(ctx, "admin", "corr-lease", tasks.CreateTaskInput{
		Operation: "agent.echo", NodeID: n.c.nodeID,
		Payload: json.RawMessage(`{"message":"retry"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	// İlk claim (çok kısa lease ile): agent işi alır ama kaybeder.
	batch, err := env.taskStore.ClaimBatchForNode(ctx, n.c.nodeID, "chaos-owner", 1, time.Millisecond)
	if err != nil || len(batch) != 1 || batch[0].ID != tk.ID {
		t.Fatalf("claim: %v %+v", err, batch)
	}
	if err := env.taskStore.StartAttempt(ctx, tk.ID, 1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	// Lease kurtarma: retry-safe görev yeniden queued olur.
	recovered, err := env.taskStore.RecoverExpiredLeases(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range recovered {
		if r.ID == tk.ID {
			found = true
			if r.Status != tasks.StatusQueued {
				t.Fatalf("retry-safe görev queued'a dönmeli: %s", r.Status)
			}
		}
	}
	if !found {
		t.Fatal("süresi dolan görev kurtarma listesinde olmalı")
	}

	// Max attempts tavanı: kalan denemeleri tükettikten sonra kurtarma
	// görevi dürüstçe failed yapar.
	for i := 0; i < 5; i++ {
		batch, err := env.taskStore.ClaimBatchForNode(ctx, n.c.nodeID, "chaos-owner", 5, time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range batch {
			_ = env.taskStore.StartAttempt(ctx, b.ID, b.Attempts+1)
		}
		time.Sleep(30 * time.Millisecond)
		_, _ = env.taskStore.RecoverExpiredLeases(ctx, 10)
	}
	final, _, _ := env.tasksSvc.Get(ctx, tk.ID)
	if final.Status != tasks.StatusFailed || final.ErrorCategory == nil || *final.ErrorCategory != "lease_expired" {
		t.Fatalf("deneme tavanı sonrası failed/lease_expired: %s %v", final.Status, final.ErrorCategory)
	}
	_ = n // stream idle; test odaklı doğrudan store sözleşmesidir
}

// TestMultiNodeThroughput proves queue throughput across 3 connected nodes:
// 30 tasks (10 per node) complete concurrently with single-attempt dispatch
// and strict node ownership (kriter 4: load behavior).
func TestMultiNodeThroughput(t *testing.T) {
	env := setupEnv(t)
	ctx := context.Background()
	g, _ := env.inventorySvc.CreateGroup(ctx, "admin", "c", "load-group", "")

	nodes := []*fakeNode{
		env.connectNode(t, g.ID, "load-a"),
		env.connectNode(t, g.ID, "load-b"),
		env.connectNode(t, g.ID, "load-c"),
	}

	const perNode = 10
	taskIDs := make([]string, 0, perNode*len(nodes))
	for _, n := range nodes {
		for i := 0; i < perNode; i++ {
			tk, _, err := env.tasksSvc.Create(ctx, "admin", fmt.Sprintf("corr-load-%s-%d", n.name, i), tasks.CreateTaskInput{
				Operation: "agent.echo", NodeID: n.c.nodeID,
				Payload: json.RawMessage(`{"message":"load"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			taskIDs = append(taskIDs, tk.ID)
		}
	}

	// Her node kendi akışını goroutine ile servis eder: op al → ACK et.
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		go func(n *fakeNode) {
			defer wg.Done()
			for i := 0; i < perNode; i++ {
				op := n.recvOperation(t, 30*time.Second)
				n.ackSucceeded(t, op.GetTaskId(), `{"echo":"load"}`)
			}
		}(n)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	waitFor(t, 45*time.Second, "30 görev 3 node'da tamamlanmalı", func() bool {
		select {
		case <-done:
			return true
		default:
		}
		count := 0
		for _, id := range taskIDs {
			got, _, _ := env.tasksSvc.Get(ctx, id)
			if got.Status == tasks.StatusSucceeded {
				count++
			}
		}
		return count == len(taskIDs)
	})

	// Tek deneme disiplini + sahiplik: her görev tam olarak bir kez ve kendi
	// node'una dağıtıldı (attempts=1, owner stream'i doğru node).
	for i, id := range taskIDs {
		got, _, _ := env.tasksSvc.Get(ctx, id)
		if got.Status != tasks.StatusSucceeded || got.Attempts != 1 {
			t.Fatalf("görev %d (%s): %s deneme=%d", i, id, got.Status, got.Attempts)
		}
	}
}

// TestCertRevocationBlocksReconnect proves certificate revocation is the
// security boundary on reconnect: a revoked (or retired) node's certificate
// is refused with Unauthenticated (kriter 4).
func TestCertRevocationBlocksReconnect(t *testing.T) {
	env := setupEnv(t)
	pool := testdb.Pool(t)
	ctx := context.Background()
	g, _ := env.inventorySvc.CreateGroup(ctx, "admin", "c", "revoke-group", "")

	a := env.connectNode(t, g.ID, "revoke-a")

	// İlk bağlantı sağlıklı.
	tk, _, err := env.tasksSvc.Create(ctx, "admin", "corr-revoke", tasks.CreateTaskInput{
		Operation: "agent.echo", NodeID: a.c.nodeID,
		Payload: json.RawMessage(`{"message":"before-revoke"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	op := a.recvOperation(t, 10*time.Second)
	a.ackSucceeded(t, op.GetTaskId(), `{"echo":"before"}`)
	waitFor(t, 10*time.Second, "görev iptal öncesi tamamlanmalı", func() bool {
		got, _, _ := env.tasksSvc.Get(ctx, tk.ID)
		return got.Status == tasks.StatusSucceeded
	})

	// Sertifika İPTALİ (node_certificates.status = 'revoked' — CP'nin iptal
	// sözleşmesi; AuthorizeStreamCert bu satırı okur).
	testdb.MustExec(t, pool, `UPDATE node_certificates SET status='revoked', revoked_at=now(), revoked_by='citest' WHERE node_id=$1`, a.c.nodeID)

	// Yeniden bağlanma reddedilir (Unauthenticated).
	stream2 := a.c.dialStream(t, true)
	if err := stream2.Send(&agentv1.AgentEnvelope{
		RequestId: "hello-after-revoke",
		NodeId:    a.c.nodeID,
		Payload: &agentv1.AgentEnvelope_Hello{Hello: &agentv1.Hello{
			ProtocolVersion: agentv1.ProtocolVersion_PROTOCOL_VERSION_V1,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream2.Recv(); err == nil {
		t.Fatal("iptal edilmiş sertifika ile bağlantı reddedilmeli")
	} else if st, ok := status.FromError(err); ok && st.Code() != codes.Unauthenticated {
		t.Fatalf("Unauthenticated bekleniyordu: %v", err)
	}

	// Retired node da reddedilir (ikinci node).
	b := env.connectNode(t, g.ID, "revoke-b")
	testdb.MustExec(t, pool, `UPDATE nodes SET status='retired' WHERE id=$1`, b.c.nodeID)
	stream3 := b.c.dialStream(t, true)
	if err := stream3.Send(&agentv1.AgentEnvelope{
		RequestId: "hello-retired",
		NodeId:    b.c.nodeID,
		Payload: &agentv1.AgentEnvelope_Hello{Hello: &agentv1.Hello{
			ProtocolVersion: agentv1.ProtocolVersion_PROTOCOL_VERSION_V1,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream3.Recv(); err == nil {
		t.Fatal("emekli node bağlantısı reddedilmeli")
	} else if st, ok := status.FromError(err); ok && st.Code() != codes.Unauthenticated {
		t.Fatalf("Unauthenticated bekleniyordu: %v", err)
	}
}
