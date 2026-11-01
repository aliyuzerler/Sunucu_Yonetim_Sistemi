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

// Run opens the outbound stream and serves until ctx is cancelled. Reconnects
// use exponential backoff with jitter (agent-architecture.md).
func Run(ctx context.Context, store *Store, serverAddr string, executors *ExecutorRegistry) error {
	if !store.HasIdentity() {
		return ErrNotEnrolled
	}
	backoff := time.Second
	for {
		err := runOnce(ctx, store, serverAddr, executors)
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

func runOnce(ctx context.Context, store *Store, serverAddr string, executors *ExecutorRegistry) error {
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
			if ack := in.GetHelloAck(); ack != nil && ack.GetHeartbeatIntervalSeconds() > 0 {
				heartbeatInterval = time.Duration(ack.GetHeartbeatIntervalSeconds()) * time.Second
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
	if err := stream.Send(capabilityEnvelope(nodeID, executors)); err != nil {
		return err
	}

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
		case <-capabilityTicker.C:
			if err := stream.Send(capabilityEnvelope(nodeID, executors)); err != nil {
				return err
			}
		}
	}
}

// capabilityEnvelope reports what this agent can do (Faz 03: yalnızca echo).
func capabilityEnvelope(nodeID string, executors *ExecutorRegistry) *agentv1.AgentEnvelope {
	return &agentv1.AgentEnvelope{
		RequestId: newRequestID(),
		NodeId:    nodeID,
		IssuedAt:  now(),
		Payload: &agentv1.AgentEnvelope_CapabilityReport{
			CapabilityReport: &agentv1.CapabilityReport{
				Capabilities: executors.Capabilities(),
			},
		},
	}
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
