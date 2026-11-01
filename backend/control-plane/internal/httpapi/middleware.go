package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"
)

type ctxKey int

const requestIDKey ctxKey = 1

const requestIDHeader = "X-Request-ID"

var safeRequestID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

// requestIDFrom returns the correlation/request ID stored in the context.
func requestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok && v != "" {
		return v
	}
	return "unknown"
}

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// generateRequestID returns 16 random bytes hex-encoded.
func generateRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Extremely unlikely; fall back to timestamp-based ID.
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b[:])
}

// requestID middleware assigns (or adopts a safe client-supplied) request ID.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if !safeRequestID.MatchString(id) {
			id = generateRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(withRequestID(r.Context(), id)))
	})
}

// logging middleware emits one structured line per request. Query strings are
// deliberately excluded: they may carry sensitive values.
func logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			level := slog.LevelInfo
			if sw.status >= 500 {
				level = slog.LevelWarn
			}
			logger.Log(context.Background(), level, "http_request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", sw.status),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("request_id", requestIDFrom(r.Context())),
			)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// recover middleware converts panics into 500 JSON envelope responses.
func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic_recovered",
						slog.String("path", r.URL.Path),
						slog.String("request_id", requestIDFrom(r.Context())),
						slog.Any("panic", rec),
					)
					writeError(w, r, http.StatusInternalServerError,
						"internal_error", "Beklenmeyen sunucu hatası.", false)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// securityHeaders applies conservative API-only headers.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// limitBody constrains request bodies to n bytes before handlers run.
func limitBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// timeout bounds handler latency. On expiry it writes the standard JSON
// error envelope (503, retryable=true) unless a response already started.
func timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return &timeoutHandler{h: next, dt: d}
	}
}

type timeoutHandler struct {
	h  http.Handler
	dt time.Duration
}

func (th *timeoutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), th.dt)
	defer cancel()
	r = r.WithContext(ctx)

	done := make(chan struct{})
	tw := &timeoutWriter{rw: w, h: make(http.Header)}
	go func() {
		defer close(done)
		th.h.ServeHTTP(tw, r)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		tw.timeoutWrite(r)
	}
}

// timeoutWriter buffers the inner handler's headers and forwards them to the
// real writer on first write. After the deadline it ignores late writes and,
// if nothing was written yet, emits the JSON timeout envelope itself.
type timeoutWriter struct {
	mu          sync.Mutex
	rw          http.ResponseWriter
	h           http.Header
	timedOut    bool
	wroteHeader bool
}

func (w *timeoutWriter) Header() http.Header { return w.h }

func (w *timeoutWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut || w.wroteHeader {
		return
	}
	w.flushHeaderLocked()
	w.rw.WriteHeader(code)
}

func (w *timeoutWriter) flushHeaderLocked() {
	dst := w.rw.Header()
	for k, vv := range w.h {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
	w.wroteHeader = true
}

func (w *timeoutWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		return 0, http.ErrHandlerTimeout
	}
	if !w.wroteHeader {
		w.flushHeaderLocked()
		w.rw.WriteHeader(http.StatusOK)
	}
	return w.rw.Write(b)
}

// timeoutWrite emits the timeout envelope directly to the real writer.
func (w *timeoutWriter) timeoutWrite(r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut || w.wroteHeader {
		return
	}
	w.timedOut = true
	rh := w.rw.Header()
	rh.Set("Content-Type", "application/json; charset=utf-8")
	if id := requestIDFrom(r.Context()); id != "unknown" {
		rh.Set(requestIDHeader, id)
	}
	w.rw.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w.rw).Encode(errorEnvelope{
		Code:          "timeout",
		Message:       "İstek zaman aşımına uğradı.",
		CorrelationID: requestIDFrom(r.Context()),
		Retryable:     true,
	})
}
