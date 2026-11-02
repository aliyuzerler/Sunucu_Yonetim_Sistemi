// httpmetrics.go wires the Faz 13 HTTP request metrics into the chi
// middleware chain. The route label uses the chi ROUTE PATTERN (e.g.
// "/api/v1/nodes/{nodeID}") — never the raw URL — so path parameters can
// never widen label cardinality (phase 13 criterion 2). Unmatched requests
// (404 before routing) are counted under the constant "unmatched" route.
package monitoring

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Metric names + fixed label sets (bounded cardinality contract).
const (
	MetricHTTPRequests = "sc_http_requests_total"
	MetricHTTPDuration = "sc_http_request_duration_seconds"

	LabelRoute = "route"
	LabelMeth  = "method"
	LabelCode  = "code"

	// RouteUnmatched is the constant label for requests that resolved to no
	// route pattern (NOT a user value — a fixed enum member).
	RouteUnmatched = "unmatched"
)

// HTTPObserver observes request metrics into a registry.
type HTTPObserver struct {
	reg *Registry
}

// NewHTTPObserver registers the HTTP metric families on the registry.
func NewHTTPObserver(reg *Registry) (*HTTPObserver, error) {
	o := &HTTPObserver{reg: reg}
	if err := reg.Counter(MetricHTTPRequests,
		"Toplam HTTP isteği (route pattern; kullanıcı girdisi label değeri değildir).",
		LabelRoute, LabelMeth, LabelCode); err != nil {
		return nil, err
	}
	if err := reg.Histogram(MetricHTTPDuration,
		"HTTP isteği işleme süresi (saniye; sabit bucket'lar).",
		[]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		LabelRoute); err != nil {
		return nil, err
	}
	return o, nil
}

// Middleware returns the request-metrics middleware. It observes AFTER the
// handler completes, reading the chi route pattern from the request context
// (the pattern — not the URL — keeps cardinality bounded).
func (o *HTTPObserver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		route := RouteUnmatched
		if rctx := chi.RouteContext(r.Context()); rctx != nil {
			if p := rctx.RoutePattern(); p != "" {
				route = p
			}
		}
		// Label set: route (pattern enum) × method (fixed) × code (1xx-5xx).
		_ = o.reg.IncCounter(MetricHTTPRequests, []string{route, r.Method, strconv.Itoa(ww.Status())}, 1)
		_ = o.reg.Observe(MetricHTTPDuration, []string{route}, time.Since(start).Seconds())
	})
}
