// Package agentsrv implements the agent-facing gRPC services over mTLS
// (ADR-0008): Enroll (token+CSR → node identity certificate, no client cert
// yet) and Connect (agent dials outbound; one bidi stream per node carrying
// hello/heartbeat/capability reports and typed operation delivery).
//
// Security invariants:
//   - The agent's private key never leaves the node; only CSRs arrive.
//   - Connect requires a client certificate issued by the CA, not revoked,
//     bound to an existing non-retired node; envelope node_id must match the
//     certificate identity on EVERY message.
//   - Expired envelopes are rejected; operation payloads are re-validated
//     server-side against the typed registry before dispatch.
package agentsrv

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/certs"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/enroll"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/inventory"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/tasks"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/telemetry"
	agentv1 "github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/gen/proto/servercontrol/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const (
	// HeartbeatInterval is the server-advised heartbeat cadence.
	HeartbeatInterval = 30 * time.Second
	// MetricsInterval is the server-advised telemetry cadence (Faz 04).
	MetricsInterval = 60 * time.Second
	// dispatchLease bounds how long a claimed node task may ride the wire.
	dispatchLease = 60 * time.Second
	// maxMessagePayload bounds large fields (CSR, payloads, outputs).
	maxMessagePayload = 64 << 10
)

// MetricsMinInterval is the fastest accepted metrics cadence per node.
// Faster reports are silently dropped (rate-limit protects the database).
const MetricsMinInterval = 10 * time.Second

// Services bundles control-plane services the agent server needs.
type Services struct {
	CA        *certs.CA
	Enroll    *enroll.Service
	Inventory *inventory.Service
	Tasks     *tasks.Service
	TaskStore tasks.Store
	Telemetry *telemetry.Service
	Audit     audit.Recorder
	Logger    *slog.Logger
}

// Server implements agentv1.EnrollServiceServer and AgentServiceServer.
type Server struct {
	agentv1.UnimplementedEnrollServiceServer
	agentv1.UnimplementedAgentServiceServer

	svc    Services
	logger *slog.Logger

	mu          sync.Mutex
	hub         map[string]*nodeStream // nodeID → active stream
	lastMetrics map[string]time.Time   // nodeID → son kabul edilen metrics zamanı
}

// nodeStream is one live mTLS stream for a node.
type nodeStream struct {
	nodeID string
	send   chan *agentv1.ServerEnvelope
	done   chan struct{}
}

// New builds the agent gRPC server.
func New(svc Services, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		svc:         svc,
		logger:      logger,
		hub:         map[string]*nodeStream{},
		lastMetrics: map[string]time.Time{},
	}
}

// RegisterGRPC attaches both services to a gRPC server.
func (s *Server) RegisterGRPC(g *grpc.Server) {
	agentv1.RegisterEnrollServiceServer(g, s)
	agentv1.RegisterAgentServiceServer(g, s)
}

// ---- Enroll (no client certificate required) ----

// Enroll consumes a single-use token and returns the node identity certificate.
func (s *Server) Enroll(ctx context.Context, req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	if s.svc.Enroll == nil || s.svc.CA == nil {
		return nil, status.Error(codes.Unavailable, "enrollment yapılandırılmamış")
	}
	if req.GetProtocolVersion() != agentv1.ProtocolVersion_PROTOCOL_VERSION_V1 {
		return nil, status.Error(codes.InvalidArgument, "desteklenmeyen protokol sürümü")
	}
	if len(req.GetCsrPem()) > maxMessagePayload {
		return nil, status.Error(codes.InvalidArgument, "CSR çok büyük")
	}
	res, err := s.svc.Enroll.Enroll(ctx, req.GetEnrollmentToken(), req.GetCsrPem(), enroll.NodeMeta{
		DisplayName:  req.GetDisplayName(),
		Hostname:     req.GetHostname(),
		OSFamily:     req.GetOsFamily(),
		AgentVersion: req.GetAgentVersion(),
	})
	if err != nil {
		s.auditEvent(ctx, "agent.enroll", "node", "", audit.ResultDenied,
			map[string]any{"reason": err.Error()})
		switch {
		case errors.Is(err, enroll.ErrTokenBad), errors.Is(err, enroll.ErrTokenUsed),
			errors.Is(err, enroll.ErrTokenExpired), errors.Is(err, enroll.ErrTokenRevoked),
			errors.Is(err, enroll.ErrScopeNotFound), errors.Is(err, enroll.ErrNodeRetired),
			errors.Is(err, enroll.ErrInvalidInput),
			errors.Is(err, certs.ErrInvalidCSR), errors.Is(err, certs.ErrWeakCSRKey):
			return nil, status.Error(codes.PermissionDenied, "kayıt reddedildi")
		default:
			s.logger.Error("agent_enroll_failed", slog.String("error", err.Error()))
			return nil, status.Error(codes.Internal, "kayıt tamamlanamadı")
		}
	}
	s.auditEvent(ctx, "agent.enroll", "node", res.NodeID, audit.ResultSuccess,
		map[string]any{"fingerprint": res.Certificate.Fingerprint})
	return &agentv1.EnrollResponse{
		NodeId:           res.NodeID,
		CertPem:          res.Certificate.CertPEM,
		CaPem:            s.svc.CA.CertificatePEM(),
		CertNotAfterUnix: res.Certificate.NotAfter.Unix(),
		RequestId:        req.GetRequestId(),
	}, nil
}

// ---- Connect (mTLS required) ----

// Connect handles the agent's outbound bidi stream.
func (s *Server) Connect(stream agentv1.AgentService_ConnectServer) error {
	cert, err := clientCertFromContext(stream.Context())
	if err != nil {
		return status.Error(codes.Unauthenticated, "istemci sertifikası gerekli")
	}
	nodeID, err := certs.NodeIDFromSANs(cert.URIs)
	if err != nil {
		return status.Error(codes.Unauthenticated, "sertifika kimliği geçersiz")
	}
	if s.svc.Enroll == nil {
		return status.Error(codes.Unavailable, "agent hizmeti yapılandırılmamış")
	}
	if err := s.svc.Enroll.AuthorizeStreamCert(stream.Context(), nodeID, certs.Fingerprint(cert)); err != nil {
		s.auditEvent(stream.Context(), "agent.connect", "node", nodeID, audit.ResultDenied,
			map[string]any{"reason": err.Error()})
		return status.Error(codes.Unauthenticated, "sertifika reddedildi")
	}

	ns := &nodeStream{
		nodeID: nodeID,
		send:   make(chan *agentv1.ServerEnvelope, 16),
		done:   make(chan struct{}),
	}
	if !s.register(ns) {
		return status.Error(codes.ResourceExhausted, "bu node için zaten canlı bir stream var")
	}
	defer s.unregister(ns)

	recvErr := make(chan error, 1)
	go s.recvLoop(stream, ns, recvErr)

	// Gönderici döngüsü: dispatch ve yanıt zarfları.
	for {
		select {
		case <-stream.Context().Done():
			return status.Error(codes.Canceled, "stream kapandı")
		case err := <-recvErr:
			if err != nil {
				return err // istemciye orijinal status kodunu koru
			}
			return status.Error(codes.Canceled, "stream sonlandı")
		case env := <-ns.send:
			if err := stream.Send(env); err != nil {
				return status.Error(codes.Unavailable, "gönderim başarısız")
			}
		}
	}
}

// recvLoop reads agent envelopes until the stream ends.
func (s *Server) recvLoop(stream agentv1.AgentService_ConnectServer, ns *nodeStream, recvErr chan<- error) {
	defer close(recvErr)
	helloSeen := false
	for {
		in, err := stream.Recv()
		if err != nil {
			recvErr <- nil // normal kapanış; yeniden bağlanma istemci işidir
			return
		}
		if err := s.handleEnvelope(stream.Context(), ns, in, &helloSeen); err != nil {
			recvErr <- err
			return
		}
	}
}

// handleEnvelope validates and processes one agent envelope.
func (s *Server) handleEnvelope(ctx context.Context, ns *nodeStream, env *agentv1.AgentEnvelope, helloSeen *bool) error {
	if env == nil {
		return status.Error(codes.InvalidArgument, "boş zarf")
	}
	if env.GetNodeId() != ns.nodeID {
		return status.Error(codes.PermissionDenied, "zarf node kimliği sertifikayla eşleşmiyor")
	}
	if env.GetExpiresAt() != nil && env.GetExpiresAt().AsTime().Before(time.Now()) {
		// Süresi geçmiş zarflar yok sayılır (replay/timeout karışıklığı koruması).
		s.logger.Warn("agent_envelope_expired",
			slog.String("node_id", ns.nodeID), slog.String("request_id", env.GetRequestId()))
		return nil
	}
	switch p := env.GetPayload().(type) {
	case *agentv1.AgentEnvelope_Hello:
		if *helloSeen {
			return status.Error(codes.FailedPrecondition, "hello yalnızca bir kez gönderilebilir")
		}
		if p.Hello.GetProtocolVersion() != agentv1.ProtocolVersion_PROTOCOL_VERSION_V1 {
			return status.Error(codes.FailedPrecondition, "desteklenmeyen protokol sürümü")
		}
		*helloSeen = true
		if err := s.svc.Enroll.TouchHello(ctx, ns.nodeID, p.Hello.GetAgentVersion(),
			p.Hello.GetHostname(), p.Hello.GetOsFamily(), p.Hello.GetOsVersion(), p.Hello.GetArch()); err != nil {
			s.logger.Error("agent_hello_failed", slog.String("error", err.Error()))
		}
		s.auditEvent(ctx, "agent.connect", "node", ns.nodeID, audit.ResultSuccess,
			map[string]any{"agent_version": p.Hello.GetAgentVersion()})
		ns.send <- &agentv1.ServerEnvelope{
			RequestId: env.GetRequestId(),
			IssuedAt:  timestamppbNow(),
			ExpiresAt: timestamppbAfter(HeartbeatInterval * 2),
			Payload: &agentv1.ServerEnvelope_HelloAck{
				HelloAck: &agentv1.HelloAck{
					ProtocolVersion:          agentv1.ProtocolVersion_PROTOCOL_VERSION_V1,
					HeartbeatIntervalSeconds: int64(HeartbeatInterval.Seconds()),
					MetricsIntervalSeconds:   int64(MetricsInterval.Seconds()),
				},
			},
		}
	case *agentv1.AgentEnvelope_Heartbeat:
		if !*helloSeen {
			return status.Error(codes.FailedPrecondition, "önce hello bekleniyor")
		}
		if err := s.svc.Enroll.TouchHeartbeat(ctx, ns.nodeID); err != nil {
			s.logger.Error("agent_heartbeat_failed", slog.String("error", err.Error()))
		}
	case *agentv1.AgentEnvelope_CapabilityReport:
		if !*helloSeen {
			return status.Error(codes.FailedPrecondition, "önce hello bekleniyor")
		}
		s.handleCapabilities(ctx, ns.nodeID, env, p.CapabilityReport)
	case *agentv1.AgentEnvelope_OperationResult:
		if !*helloSeen {
			return status.Error(codes.FailedPrecondition, "önce hello bekleniyor")
		}
		s.handleOperationResult(ctx, ns, p.OperationResult)
	case *agentv1.AgentEnvelope_SystemFactsReport:
		if !*helloSeen {
			return status.Error(codes.FailedPrecondition, "önce hello bekleniyor")
		}
		s.handleSystemFacts(ctx, ns.nodeID, env, p.SystemFactsReport)
	case *agentv1.AgentEnvelope_MetricsReport:
		if !*helloSeen {
			return status.Error(codes.FailedPrecondition, "önce hello bekleniyor")
		}
		s.handleMetricsReport(ctx, ns.nodeID, env, p.MetricsReport)
	default:
		return status.Error(codes.InvalidArgument, "bilinmeyen zarf yükü")
	}
	return nil
}

// handleCapabilities upserts the reported capability set.
func (s *Server) handleCapabilities(ctx context.Context, nodeID string, env *agentv1.AgentEnvelope, rep *agentv1.CapabilityReport) {
	if len(rep.GetCapabilities()) > 64 {
		s.logger.Warn("agent_capability_report_too_large", slog.String("node_id", nodeID))
		return
	}
	for _, c := range rep.GetCapabilities() {
		cap := &inventory.Capability{
			NodeID:         nodeID,
			CapabilityID:   c.GetCapabilityId(),
			Version:        c.GetVersion(),
			Available:      c.GetAvailable(),
			AdapterID:      opt(c.GetAdapterId()),
			AdapterVersion: opt(c.GetAdapterVersion()),
		}
		if !cap.Available {
			r := strings.Clone(c.GetReasonUnavailable())
			if len(r) > 256 {
				r = r[:256]
			}
			cap.ReasonUnavailable = &r
		}
		if err := s.svc.Inventory.UpsertCapability(ctx, "agent:"+nodeID, env.GetCorrelationId(), cap); err != nil {
			s.logger.Error("agent_capability_upsert_failed",
				slog.String("node_id", nodeID),
				slog.String("capability_id", c.GetCapabilityId()),
				slog.String("error", err.Error()))
		}
	}
}

// handleSystemFacts upserts the agent-reported platform facts (Faz 04).
// Facts are low-frequency inventory data: each accepted report is audited.
func (s *Server) handleSystemFacts(ctx context.Context, nodeID string, env *agentv1.AgentEnvelope, rep *agentv1.SystemFactsReport) {
	if s.svc.Telemetry == nil {
		return
	}
	collected := time.Now().UTC()
	if rep.GetCollectedAt() != nil && !rep.GetCollectedAt().AsTime().IsZero() {
		collected = rep.GetCollectedAt().AsTime()
	}
	f := &telemetry.SystemFacts{
		NodeID:           nodeID,
		OSID:             rep.GetOsId(),
		OSVersionID:      rep.GetOsVersionId(),
		OSPrettyName:     rep.GetOsPrettyName(),
		OSHomeURL:        rep.GetOsHomeUrl(),
		KernelRelease:    rep.GetKernelRelease(),
		Arch:             rep.GetArch(),
		SystemdVersion:   rep.GetSystemdVersion(),
		PackageManager:   rep.GetPackageManager(),
		SystemdAvailable: rep.GetSystemdAvailable(),
		DistroFamily:     rep.GetDistroFamily(),
		DistroLike:       rep.GetDistroLike(),
		SupportState:     rep.GetSupportState(),
		SupportReason:    rep.GetSupportReason(),
		Source:           rep.GetSource(),
		CollectedAt:      collected,
	}
	if err := s.svc.Telemetry.UpsertFacts(ctx, f); err != nil {
		s.logger.Error("agent_facts_upsert_failed",
			slog.String("node_id", nodeID), slog.String("error", err.Error()))
		return
	}
	s.auditEvent(ctx, "agent.facts.report", "node", nodeID, audit.ResultSuccess,
		map[string]any{"os_id": f.OSID, "os_version_id": f.OSVersionID,
			"package_manager": f.PackageManager, "source": f.Source})
}

// handleMetricsReport persists one metric snapshot behind a per-node rate
// limit. Metrics are data-plane telemetry: they are NOT individually audited
// (the audit trail records control actions; log noise would bury them).
func (s *Server) handleMetricsReport(ctx context.Context, nodeID string, env *agentv1.AgentEnvelope, rep *agentv1.MetricsReport) {
	if s.svc.Telemetry == nil {
		return
	}
	now := time.Now().UTC()
	s.mu.Lock()
	if last, ok := s.lastMetrics[nodeID]; ok && now.Sub(last) < MetricsMinInterval {
		s.mu.Unlock()
		s.logger.Warn("agent_metrics_rate_limited", slog.String("node_id", nodeID))
		return
	}
	s.lastMetrics[nodeID] = now
	s.mu.Unlock()

	collected := now
	if rep.GetCollectedAt() != nil && !rep.GetCollectedAt().AsTime().IsZero() {
		collected = rep.GetCollectedAt().AsTime()
	}
	snap := &telemetry.Snapshot{
		CollectedAt:   collected,
		Source:        rep.GetSource(),
		UptimeSeconds: rep.GetUptimeSeconds(),
	}
	if cpu := rep.GetCpu(); cpu != nil {
		snap.CPU = telemetry.CPUSample{
			Percent: cpu.GetPercent(), LogicalCores: cpu.GetLogicalCores(),
			Load1m: cpu.GetLoad_1M(), Load5m: cpu.GetLoad_5M(), Load15m: cpu.GetLoad_15M(),
		}
	}
	if mem := rep.GetMemory(); mem != nil {
		snap.Memory = telemetry.MemorySample{
			TotalBytes: mem.GetTotalBytes(), AvailableBytes: mem.GetAvailableBytes(),
			UsedBytes: mem.GetUsedBytes(), UsedPercent: mem.GetUsedPercent(),
		}
	}
	for i, d := range rep.GetDisks() {
		if i >= telemetry.MaxDisks {
			break
		}
		snap.Disks = append(snap.Disks, telemetry.DiskSample{
			Device: d.GetDevice(), MountPoint: d.GetMountPoint(), FSType: d.GetFsType(),
			TotalBytes: d.GetTotalBytes(), UsedBytes: d.GetUsedBytes(), AvailableBytes: d.GetAvailableBytes(),
		})
	}
	for i, n := range rep.GetNetworks() {
		if i >= telemetry.MaxNetworks {
			break
		}
		snap.Networks = append(snap.Networks, telemetry.NetworkSample{
			Interface: n.GetInterface(), RxBytes: n.GetRxBytes(), TxBytes: n.GetTxBytes(),
		})
	}
	if err := s.svc.Telemetry.InsertMetrics(ctx, nodeID, snap); err != nil {
		if errors.Is(err, telemetry.ErrInvalidSnapshot) {
			s.logger.Warn("agent_metrics_invalid", slog.String("node_id", nodeID))
			return
		}
		s.logger.Error("agent_metrics_insert_failed",
			slog.String("node_id", nodeID), slog.String("error", err.Error()))
	}
}

// handleOperationResult completes or fails a dispatched task.
func (s *Server) handleOperationResult(ctx context.Context, ns *nodeStream, res *agentv1.OperationResult) {
	if s.svc.Tasks == nil || s.svc.TaskStore == nil {
		return
	}
	t, attempts, err := s.svc.Tasks.Get(ctx, res.GetTaskId())
	if err != nil || t == nil {
		s.logger.Warn("agent_result_unknown_task",
			slog.String("node_id", ns.nodeID), slog.String("task_id", res.GetTaskId()))
		return
	}
	if t.NodeID == nil || *t.NodeID != ns.nodeID {
		s.logger.Warn("agent_result_wrong_node",
			slog.String("node_id", ns.nodeID), slog.String("task_id", res.GetTaskId()))
		return
	}
	if t.Status != tasks.StatusDispatched {
		s.logger.Warn("agent_result_stale_task",
			slog.String("task_id", res.GetTaskId()), slog.String("status", t.Status))
		return
	}
	attemptNo := 1
	if len(attempts) > 0 {
		attemptNo = attempts[len(attempts)-1].AttemptNo
	}

	switch res.GetStatus() {
	case agentv1.OperationResult_STATUS_SUCCEEDED:
		result := map[string]any{}
		if len(res.GetResult()) > 0 {
			_ = json.Unmarshal(res.GetResult(), &result)
		}
		ok, cerr := s.svc.TaskStore.CompleteExecution(ctx, res.GetTaskId(), attemptNo, result)
		if cerr != nil || !ok {
			s.logger.Warn("agent_result_complete_failed",
				slog.String("task_id", res.GetTaskId()), slog.String("error", fmt.Sprint(cerr)))
			return
		}
		s.auditEvent(ctx, "task.complete", "task", res.GetTaskId(), audit.ResultSuccess,
			map[string]any{"node_id": ns.nodeID})
	case agentv1.OperationResult_STATUS_FAILED, agentv1.OperationResult_STATUS_REJECTED:
		category := res.GetErrorCategory()
		if category == "" {
			category = tasks.ErrCatExecutor
		}
		if len(category) > 64 {
			category = category[:64]
		}
		output := res.GetOutput()
		if len(output) > 4000 {
			output = output[:4000]
		}
		ok, cerr := s.svc.TaskStore.FailExecution(ctx, res.GetTaskId(), attemptNo, category, output)
		if cerr != nil || !ok {
			s.logger.Warn("agent_result_fail_failed",
				slog.String("task_id", res.GetTaskId()), slog.String("error", fmt.Sprint(cerr)))
			return
		}
		s.auditEvent(ctx, "task.fail", "task", res.GetTaskId(), audit.ResultFailure,
			map[string]any{"node_id": ns.nodeID, "error_category": category})
	default:
		s.logger.Warn("agent_result_unspecified", slog.String("task_id", res.GetTaskId()))
	}
}

// dispatchLoop claims queued node-targeted tasks and pushes them onto the
// stream while it lives. Claims use SKIP LOCKED leases; a dead stream leaves
// the lease to expire and retry-safe tasks requeue.
func (s *Server) dispatchLoop(ns *nodeStream) {
	owner := "agentstream:" + ns.nodeID
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ns.done:
			return
		case <-ticker.C:
		}
		if s.svc.Tasks == nil || s.svc.TaskStore == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		batch, err := s.svc.TaskStore.ClaimBatchForNode(ctx, ns.nodeID, owner, 10, dispatchLease)
		if err != nil {
			cancel()
			continue
		}
		for _, t := range batch {
			if err := s.svc.TaskStore.StartAttempt(ctx, t.ID, t.Attempts+1); err != nil {
				s.logger.Error("agent_dispatch_attempt_failed",
					slog.String("task_id", t.ID), slog.String("error", err.Error()))
				continue
			}
			deadline := time.Now().Add(dispatchLease)
			if t.DeadlineAt != nil {
				deadline = *t.DeadlineAt
			} else if spec, ok := s.svc.Tasks.OperationSpec(t.Operation); ok {
				deadline = time.Now().Add(spec.Timeout)
			}
			env := &agentv1.ServerEnvelope{
				RequestId:      t.ID,
				CorrelationId:  t.CorrelationID,
				IssuedAt:       timestamppbNow(),
				ExpiresAt:      timestamppbTime(deadline),
				IdempotencyKey: derefStr(t.IdempotencyKey),
				Payload: &agentv1.ServerEnvelope_OperationRequest{
					OperationRequest: &agentv1.OperationRequest{
						TaskId:           t.ID,
						Operation:        t.Operation,
						OperationVersion: t.OperationVersion,
						Payload:          []byte(t.Payload),
					},
				},
			}
			select {
			case ns.send <- env:
			case <-ns.done:
				cancel()
				return
			}
		}
		cancel()
	}
}

// register/unregister keep one live stream per node; the dispatch loop starts
// only after registration succeeds.
func (s *Server) register(ns *nodeStream) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.hub[ns.nodeID]; ok {
		return false
	}
	s.hub[ns.nodeID] = ns
	go s.dispatchLoop(ns)
	return true
}

func (s *Server) unregister(ns *nodeStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.hub[ns.nodeID]; ok && cur == ns {
		delete(s.hub, ns.nodeID)
	}
	close(ns.done)
}

func (s *Server) auditEvent(ctx context.Context, action, targetType, targetID, result string, meta map[string]any) {
	if s.svc.Audit == nil {
		return
	}
	_ = s.svc.Audit.Record(ctx, audit.Event{
		Actor:         "agent",
		Action:        action,
		TargetType:    targetType,
		TargetID:      targetID,
		Result:        result,
		CorrelationID: requestIDFromMD(ctx),
		Metadata:      audit.Redact(meta).(map[string]any),
	})
}

// clientCertFromContext extracts the first verified peer certificate (mTLS).
func clientCertFromContext(ctx context.Context) (*x509.Certificate, error) {
	p, ok := peer.FromContext(ctx)
	if !ok || p.AuthInfo == nil {
		return nil, errors.New("kimlik bilgisi yok")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return nil, errors.New("TLS kimlik bilgisi değil")
	}
	if len(tlsInfo.State.PeerCertificates) == 0 {
		return nil, errors.New("istemci sertifikası yok")
	}
	return tlsInfo.State.PeerCertificates[0], nil
}

// requestIDFromMD pulls X-Request-ID metadata if present.
func requestIDFromMD(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if v := md.Get("x-request-id"); len(v) > 0 {
		return v[0]
	}
	return ""
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func opt(s string) *string {
	if s == "" {
		return nil
	}
	v := strings.Clone(s)
	return &v
}
