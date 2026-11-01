package broker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

// Client is the agent-side client of the local privileged broker. It dials
// the Unix socket per request (one request/response per connection) with
// strict deadlines.
type Client struct {
	SocketPath string
	// DialTimeout/CallTimeout bound connection and whole-call durations.
	DialTimeout time.Duration
	CallTimeout time.Duration
}

// NewClient builds a client with safe default deadlines.
func NewClient(socketPath string) *Client {
	return &Client{SocketPath: socketPath, DialTimeout: 2 * time.Second, CallTimeout: 90 * time.Second}
}

// Call performs one typed request over the Unix socket.
func (c *Client) Call(ctx context.Context, req *Request) (*Response, error) {
	callCtx := ctx
	var cancel context.CancelFunc = func() {}
	if c.CallTimeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, c.CallTimeout)
	}
	defer cancel()

	d := net.Dialer{Timeout: c.DialTimeout}
	conn, err := d.DialContext(callCtx, "unix", c.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("broker bağlantısı kurulamadı: %w", err)
	}
	defer conn.Close()
	if dl, ok := callCtx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	req.V = ProtocolV1
	// Yazma hatasını yut: broker bazı redlerde (peer_denied) isteği okumadan
	// yanıtı yazıp kapanır; istemci yine de yanıtı okumayı dener.
	_ = json.NewEncoder(conn).Encode(req)
	// Yarı kapatma: sunucu tek istek/yanıt modelinde hemen EOF'u görür.
	if tc, ok := conn.(*net.UnixConn); ok {
		_ = tc.CloseWrite()
	}

	line, err := bufio.NewReaderSize(conn, maxRequestBytes+2).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, fmt.Errorf("yanıt okunamadı: %w", err)
	}
	var res Response
	if err := json.Unmarshal(line, &res); err != nil {
		return nil, fmt.Errorf("yanıt çözülemedi: %w", err)
	}
	return &res, nil
}

// Ping probes broker availability for capability reporting. It does not
// execute anything; a rejected-but-answered probe still proves reachability.
func (c *Client) Ping(ctx context.Context) (bool, string) {
	res, err := c.Call(ctx, &Request{
		V:         ProtocolV1,
		Operation: OpServiceList,
		RequestID: fmt.Sprintf("probe-%d", time.Now().UnixNano()),
		Params:    []byte(`{"limit":1}`),
	})
	if err != nil {
		return false, "broker_unreachable"
	}
	switch res.Status {
	case StatusSucceeded:
		return true, ""
	case StatusRejected:
		// Erişilebilir ama istek reddedildi (ör. izin yok) — capability
		// açısından broker canlı; nedeni raporla.
		return false, "broker_rejected:" + res.ErrorCategory
	default:
		return false, "broker_error:" + res.Status
	}
}

// SocketDirPerms documents the required socket directory mode.
const SocketDirPerms os.FileMode = 0o750
const SocketFilePerms os.FileMode = 0o660
