package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/metrics"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/osdetect"
	agentv1 "github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/gen/proto/servercontrol/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Enroll exchanges the token + locally-generated CSR for a node certificate.
// The private key never leaves this process.
func Enroll(ctx context.Context, store *Store, serverAddr, token, displayName string) (string, error) {
	if token == "" {
		return "", fmt.Errorf("--token zorunlu")
	}
	caPEM, err := os.ReadFile(store.CAPath())
	if err != nil {
		return "", fmt.Errorf("--ca-file içeriğini %s konumuna kaydedin: %w", store.CAPath(), err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return "", fmt.Errorf("CA dosyası geçersiz")
	}

	key, err := store.GenerateKey()
	if err != nil {
		return "", err
	}
	csrPEM, err := BuildCSR(key)
	if err != nil {
		return "", err
	}

	conn, err := grpc.NewClient(serverAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			RootCAs:    caPool,
			MinVersion: tls.VersionTLS12,
		})))
	if err != nil {
		return "", fmt.Errorf("bağlantı kurulamadı: %w", err)
	}
	defer conn.Close()

	client := agentv1.NewEnrollServiceClient(conn)
	res, err := client.Enroll(ctx, &agentv1.EnrollRequest{
		ProtocolVersion: agentv1.ProtocolVersion_PROTOCOL_VERSION_V1,
		EnrollmentToken: token,
		CsrPem:          csrPEM,
		DisplayName:     displayName,
		Hostname:        hostName(),
		OsFamily:        osFamily(),
		AgentVersion:    Version,
		RequestId:       newRequestID(),
	})
	if err != nil {
		return "", fmt.Errorf("enroll reddedildi: %w", err)
	}
	// CA pinleme: sunucunun döndürdüğü CA, operatörün verdiğiyle aynı olmalı.
	if res.GetCaPem() != string(caPEM) {
		return "", fmt.Errorf("güvenlik: sunucu CA'sı beklenenle eşleşmiyor (MITM şüphesi)")
	}
	if err := store.SaveCert(res.GetCertPem(), res.GetCaPem()); err != nil {
		return "", err
	}
	return res.GetNodeId(), nil
}

// Version is the agent build version (ldflags ile override edilebilir).
var Version = "dev"

// dialMTLS opens the outbound mTLS connection with stored identity material.
func dialMTLS(store *Store, serverAddr string) (*grpc.ClientConn, error) {
	parts, err := store.LoadTLSConfigParts()
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(parts.certPEM, parts.keyPEM)
	if err != nil {
		return nil, fmt.Errorf("kimlik malzemesi geçersiz: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(parts.caPEM) {
		return nil, fmt.Errorf("CA dosyası geçersiz")
	}
	return grpc.NewClient(serverAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      caPool,
			MinVersion:   tls.VersionTLS12,
		})))
}

// RunOptions configures a Run session.
type RunOptions struct {
	// FactsRoot is the filesystem root for fact/metric detection ("" or "/"
	// = real host; testdata fixtures in tests).
	FactsRoot string
}

// Run opens the outbound stream and serves until ctx is cancelled. Reconnects
// use exponential backoff with jitter (agent-architecture.md).
func Run(ctx context.Context, store *Store, serverAddr string, executors *ExecutorRegistry, opts RunOptions) error {
	if !store.HasIdentity() {
		return ErrNotEnrolled
	}
	backoff := time.Second
	for {
		err := runOnce(ctx, store, serverAddr, executors, opts)
		if ctx.Err() != nil {
			return nil
		}
		fmt.Fprintf(os.Stderr, "server-agent: stream koptu (%v); yeniden bağlanıyor\n", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff + jitter(backoff)):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func runOnce(ctx context.Context, store *Store, serverAddr string, executors *ExecutorRegistry, opts RunOptions) error {
	conn, err := dialMTLS(store, serverAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := agentv1.NewAgentServiceClient(conn)
	stream, err := client.Connect(ctx)
	if err != nil {
		return fmt.Errorf("stream açılamadı: %w", err)
	}

	nodeID, err := nodeIDFromCert(store)
	if err != nil {
		return err
	}

	// Hello.
	startedAt := time.Now()
	if err := stream.Send(&agentv1.AgentEnvelope{
		RequestId: newRequestID(),
		NodeId:    nodeID,
		IssuedAt:  now(),
		Payload: &agentv1.AgentEnvelope_Hello{
			Hello: &agentv1.Hello{
				ProtocolVersion: agentv1.ProtocolVersion_PROTOCOL_VERSION_V1,
				AgentVersion:    Version,
				Hostname:        hostName(),
				OsFamily:        osFamily(),
				OsVersion:       osVersion(),
				Arch:            runtime.GOARCH,
			},
		},
	}); err != nil {
		return fmt.Errorf("hello gönderilemedi: %w", err)
	}

	var heartbeatInterval = 30 * time.Second
	var metricsInterval = 60 * time.Second // HelloAck ile sunucu ayarlar
	seq := uint64(0)

	// Alım döngüsü: OperationRequest'leri yürüt, sonuçları gönder.
	recvDone := make(chan error, 1)
	go func() {
		defer close(recvDone)
		for {
			in, err := stream.Recv()
			if err != nil {
				recvDone <- err
				return
			}
			if in.GetExpiresAt() != nil && in.GetExpiresAt().AsTime().Before(time.Now()) {
				continue // süresi geçmiş isteği yürütme (timeout karışıklığı koruması)
			}
			if op := in.GetOperationRequest(); op != nil {
				res := executors.Execute(ctx, nodeID, op)
				out := &agentv1.AgentEnvelope{
					RequestId:      in.GetRequestId(),
					NodeId:         nodeID,
					CorrelationId:  in.GetCorrelationId(),
					IdempotencyKey: in.GetIdempotencyKey(),
					IssuedAt:       now(),
					Payload:        &agentv1.AgentEnvelope_OperationResult{OperationResult: res},
				}
				if err := stream.Send(out); err != nil {
					recvDone <- err
					return
				}
			}
			if ack := in.GetHelloAck(); ack != nil {
				if ack.GetHeartbeatIntervalSeconds() > 0 {
					heartbeatInterval = time.Duration(ack.GetHeartbeatIntervalSeconds()) * time.Second
				}
				if ack.GetMetricsIntervalSeconds() > 0 {
					metricsInterval = clampInterval(time.Duration(ack.GetMetricsIntervalSeconds()) * time.Second)
				}
			}
		}
	}()

	// Heartbeat döngüsü + capability raporu.
	capabilityTicker := time.NewTicker(5 * time.Minute)
	defer capabilityTicker.Stop()
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	// İlk heartbeat hemen gider ki sunucu hızlı görünsün.
	sendHeartbeat := func() error {
		seq++
		return stream.Send(&agentv1.AgentEnvelope{
			RequestId: newRequestID(),
			NodeId:    nodeID,
			IssuedAt:  now(),
			Payload: &agentv1.AgentEnvelope_Heartbeat{
				Heartbeat: &agentv1.Heartbeat{
					Sequence:      seq,
					UptimeSeconds: time.Since(startedAt).Seconds(),
				},
			},
		})
	}
	if err := sendHeartbeat(); err != nil {
		return err
	}
	if err := stream.Send(capabilityEnvelope(ctx, nodeID, executors)); err != nil {
		return err
	}
	// Faz 04: sistem bilgileri Hello sonrası bir kez gönderilir.
	if factsEnv := factsEnvelope(nodeID, opts.FactsRoot); factsEnv != nil {
		if err := stream.Send(factsEnv); err != nil {
			return err
		}
	}

	metricsTicker := time.NewTicker(metricsInterval)
	defer metricsTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-recvDone:
			return err
		case <-heartbeat.C:
			if err := sendHeartbeat(); err != nil {
				return err
			}
		case <-metricsTicker.C:
			if env := metricsEnvelope(nodeID, opts.FactsRoot); env != nil {
				if err := stream.Send(env); err != nil {
					return err
				}
			}
		case <-capabilityTicker.C:
			if err := stream.Send(capabilityEnvelope(ctx, nodeID, executors)); err != nil {
				return err
			}
		}
	}
}

// capabilityEnvelope reports what this agent can do. Availability of
// broker-backed service capabilities is probed at report time so the server
// sees the CURRENT runtime contract (capability-system.md).
func capabilityEnvelope(ctx context.Context, nodeID string, executors *ExecutorRegistry) *agentv1.AgentEnvelope {
	caps := make([]*agentv1.Capability, 0, len(executors.Capabilities()))
	probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	for _, info := range executors.Capabilities() {
		if exec, ok := executors.Lookup(info.GetCapabilityId()); ok {
			if p, ok2 := exec.(Prober); ok2 {
				available, reason := p.ProbeAvailable(probeCtx)
				info.Available = available
				info.ReasonUnavailable = reason
			}
		}
		caps = append(caps, info)
	}
	return &agentv1.AgentEnvelope{
		RequestId: newRequestID(),
		NodeId:    nodeID,
		IssuedAt:  now(),
		Payload: &agentv1.AgentEnvelope_CapabilityReport{
			CapabilityReport: &agentv1.CapabilityReport{
				Capabilities: caps,
			},
		},
	}
}

// factsEnvelope collects and reports the platform facts. Returns nil when
// detection completely fails (the server keeps the previous value).
func factsEnvelope(nodeID, root string) *agentv1.AgentEnvelope {
	facts, err := osdetect.Detect(root, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server-agent: sistem bilgileri alınamadı: %v\n", err)
		return nil
	}
	return &agentv1.AgentEnvelope{
		RequestId: newRequestID(),
		NodeId:    nodeID,
		IssuedAt:  now(),
		Payload: &agentv1.AgentEnvelope_SystemFactsReport{
			SystemFactsReport: &agentv1.SystemFactsReport{
				OsId:             facts.OSID,
				OsVersionId:      facts.OSVersionID,
				OsPrettyName:     facts.OSPrettyName,
				OsHomeUrl:        facts.OSHomeURL,
				KernelRelease:    facts.KernelRelease,
				Arch:             facts.Arch,
				SystemdVersion:   facts.SystemdVersion,
				PackageManager:   facts.PackageManager,
				SystemdAvailable: facts.SystemdAvailable,
				CollectedAt:      now(),
				Source:           facts.Source,
			},
		},
	}
}

// metricsEnvelope collects one metric snapshot; nil when collection fails.
func metricsEnvelope(nodeID, root string) *agentv1.AgentEnvelope {
	snap, err := metrics.NewCollector(root).Collect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "server-agent: metrikler alınamadı: %v\n", err)
		return nil
	}
	disks := make([]*agentv1.DiskMetrics, 0, len(snap.Disks))
	for _, d := range snap.Disks {
		disks = append(disks, &agentv1.DiskMetrics{
			Device: d.Device, MountPoint: d.MountPoint, FsType: d.FSType,
			TotalBytes: d.TotalBytes, UsedBytes: d.UsedBytes, AvailableBytes: d.AvailableBytes,
		})
	}
	nets := make([]*agentv1.NetworkMetrics, 0, len(snap.Networks))
	for _, n := range snap.Networks {
		nets = append(nets, &agentv1.NetworkMetrics{Interface: n.Interface, RxBytes: n.RxBytes, TxBytes: n.TxBytes})
	}
	return &agentv1.AgentEnvelope{
		RequestId: newRequestID(),
		NodeId:    nodeID,
		IssuedAt:  now(),
		Payload: &agentv1.AgentEnvelope_MetricsReport{
			MetricsReport: &agentv1.MetricsReport{
				CollectedAt: now(),
				Source:      snap.Source,
				Cpu: &agentv1.CpuMetrics{
					Percent: snap.CPU.Percent, LogicalCores: snap.CPU.LogicalCores,
					Load_1M: snap.CPU.Load1m, Load_5M: snap.CPU.Load5m, Load_15M: snap.CPU.Load15m,
				},
				Memory: &agentv1.MemoryMetrics{
					TotalBytes: snap.Memory.TotalBytes, AvailableBytes: snap.Memory.AvailableBytes,
					UsedBytes: snap.Memory.UsedBytes, UsedPercent: snap.Memory.UsedPercent,
				},
				Disks:         disks,
				Networks:      nets,
				UptimeSeconds: snap.UptimeSeconds,
			},
		},
	}
}

// clampInterval bounds the server-advised metrics cadence.
func clampInterval(d time.Duration) time.Duration {
	if d < 10*time.Second {
		return 10 * time.Second
	}
	if d > 10*time.Minute {
		return 10 * time.Minute
	}
	return d
}

func nodeIDFromCert(store *Store) (string, error) {
	parts, err := store.LoadTLSConfigParts()
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(parts.certPEM)
	if block == nil {
		return "", fmt.Errorf("sertifika PEM'i geçersiz")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	for _, u := range cert.URIs {
		if u.Scheme == "urn" && len(u.Opaque) > 19 && u.Opaque[:19] == "servercontrol:node:" {
			return u.Opaque[19:], nil
		}
	}
	return "", fmt.Errorf("sertifikada node kimliği yok")
}
