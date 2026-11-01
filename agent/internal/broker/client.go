package broker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// Client is the agent-side client of the local privileged broker. It dials
// the platform endpoint per request (one request/response per connection)
// with strict deadlines: unix socket (SO_PEERCRED) on Linux, named pipe
// (SDDL + impersonation identity) on Windows.
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

// Call performs one typed request over the platform transport.
func (c *Client) Call(ctx context.Context, req *Request) (*Response, error) {
	callCtx := ctx
	var cancel context.CancelFunc = func() {}
	if c.CallTimeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, c.CallTimeout)
	}
	defer cancel()

	if c.DialTimeout > 0 {
		var dcancel context.CancelFunc
		dialCtx, dcancel := context.WithTimeout(callCtx, c.DialTimeout)
		defer dcancel()
		conn, err := c.dial(dialCtx)
		if err != nil {
			return nil, fmt.Errorf("broker bağlantısı kurulamadı: %w", err)
		}
		defer conn.Close()
		if dl, ok := callCtx.Deadline(); ok {
			_ = conn.SetDeadline(dl)
		}
		return c.roundTrip(conn, req)
	}
	conn, err := c.dial(callCtx)
	if err != nil {
		return nil, fmt.Errorf("broker bağlantısı kurulamadı: %w", err)
	}
	defer conn.Close()
	if dl, ok := callCtx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	return c.roundTrip(conn, req)
}

// roundTrip writes the request and reads one response line.
func (c *Client) roundTrip(conn net.Conn, req *Request) (*Response, error) {
	req.V = ProtocolV1
	// Yazma hatasını yut: broker bazı redlerde (peer_denied) isteği okumadan
	// yanıtı yazıp kapanır; istemci yine de yanıtı okumayı dener.
	_ = json.NewEncoder(conn).Encode(req)
	// Yarı kapatma: sunucu tek istek/yanıt modelinde hemen EOF'u görür.
	halfClose(conn)

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
		Operation: probeOperation(),
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
