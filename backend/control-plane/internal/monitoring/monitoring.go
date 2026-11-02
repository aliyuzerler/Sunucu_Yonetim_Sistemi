// Package monitoring implements the Faz 13 process-metric registry: a
// stdlib-only Prometheus text-exposition renderer with HARD cardinality
// bounds. No user-controlled value may become a label value; label value
// sets must be closed enums (route patterns, HTTP methods, status codes,
// task statuses, node UUIDs) so series counts stay bounded by design.
//
// Prometheus text format reference:
// https://github.com/prometheus/docs/blob/main/content/docs/instrumenting/exposition_formats.md
package monitoring

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// maxSeriesPerFamily is the hard per-family series ceiling. Registering a
// new series beyond the bound is refused (typed error) — cardinality can
// therefore never grow with user input, only with the bounded enum sets.
const maxSeriesPerFamily = 512

// ErrCardinalityBound is returned when a label combination would create a
// series beyond the per-family ceiling.
var ErrCardinalityBound = fmt.Errorf("monitoring: label kardinalite sınırı aşıldı (aile başına %d seri)", maxSeriesPerFamily)

// validMetricName enforces the Prometheus metric name grammar.
func validMetricName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_' || r == ':':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// validLabelName enforces the Prometheus label name grammar (no colon).
func validLabelName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// escapeLabelValue escapes the characters the text format requires inside
// label values (backslash, quote, newline).
func escapeLabelValue(v string) string {
	r := strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
	return r.Replace(v)
}

// seriesKey joins label values with a separator that cannot occur in the
// bounded enum sets the registry is fed with.
func seriesKey(values []string) string { return strings.Join(values, "\x1f") }

// counterFamily is one counter metric and its label values.
type counterFamily struct {
	name, help string
	labels     []string
	series     map[string]*float64
}

// gaugeValue is one gauge series value with an optional Prometheus sample
// timestamp (epoch milliseconds; 0 = scrape time). Phase 13 criterion 1:
// exported series carry their collection time.
type gaugeValue struct {
	value float64
	ts    int64
}

// gaugeFamily is one gauge metric and its label values.
type gaugeFamily struct {
	name, help string
	labels     []string
	series     map[string]*gaugeValue
}

// histSample accumulates one histogram series (cumulative bucket counts).
type histSample struct {
	buckets []uint64 // cumulative, len == len(boundaries)
	count   uint64
	sum     float64
}

// histFamily is one histogram metric with fixed boundaries.
type histFamily struct {
	name, help string
	labels     []string
	boundaries []float64
	series     map[string]*histSample
}

// Registry is the process-wide metric registry. All methods are safe for
// concurrent use.
type Registry struct {
	mu       sync.Mutex
	counters map[string]*counterFamily
	gauges   map[string]*gaugeFamily
	hists    map[string]*histFamily
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		counters: map[string]*counterFamily{},
		gauges:   map[string]*gaugeFamily{},
		hists:    map[string]*histFamily{},
	}
}

func checkLabels(name string, labelNames []string) error {
	if !validMetricName(name) {
		return fmt.Errorf("monitoring: geçersiz metrik adı %q", name)
	}
	seen := map[string]bool{}
	for _, l := range labelNames {
		if !validLabelName(l) || seen[l] || l == "le" {
			return fmt.Errorf("monitoring: geçersiz/tekrarlı label %q", l)
		}
		seen[l] = true
	}
	return nil
}

func (r *Registry) counterFamilyFor(name string, values []string) (*counterFamily, error) {
	f, ok := r.counters[name]
	if !ok {
		return nil, fmt.Errorf("monitoring: kayıtsız metrik %q", name)
	}
	if len(values) != len(f.labels) {
		return nil, fmt.Errorf("monitoring: %q label sayısı uyuşmuyor", name)
	}
	return f, nil
}

func (r *Registry) gaugeFamilyFor(name string, values []string) (*gaugeFamily, error) {
	f, ok := r.gauges[name]
	if !ok {
		return nil, fmt.Errorf("monitoring: kayıtsız gauge %q", name)
	}
	if len(values) != len(f.labels) {
		return nil, fmt.Errorf("monitoring: %q label sayısı uyuşmuyor", name)
	}
	return f, nil
}

// IncCounter increments the counter series identified by the label values.
// A new series beyond the ceiling is refused.
func (r *Registry) IncCounter(name string, values []string, delta float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := r.counterFamilyFor(name, values)
	if err != nil {
		return err
	}
	key := seriesKey(values)
	if _, exists := f.series[key]; !exists {
		if len(f.series) >= maxSeriesPerFamily {
			return ErrCardinalityBound
		}
		f.series[key] = new(float64)
	}
	*f.series[key] += delta
	return nil
}

// SetGauge sets the gauge series to value (sample time = scrape time).
func (r *Registry) SetGauge(name string, values []string, value float64) error {
	return r.setGauge(name, values, value, 0)
}

// SetGaugeAt sets the gauge series to value with an explicit sample
// timestamp (epoch milliseconds). Use ONLY for values whose collection time
// is known (e.g. node snapshot collected_at); never for user-supplied data.
func (r *Registry) SetGaugeAt(name string, values []string, value float64, tsMillis int64) error {
	return r.setGauge(name, values, value, tsMillis)
}

func (r *Registry) setGauge(name string, values []string, value float64, tsMillis int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := r.gaugeFamilyFor(name, values)
	if err != nil {
		return err
	}
	key := seriesKey(values)
	if _, exists := f.series[key]; !exists {
		if len(f.series) >= maxSeriesPerFamily {
			return ErrCardinalityBound
		}
		f.series[key] = &gaugeValue{}
	}
	g := f.series[key]
	g.value = value
	g.ts = tsMillis
	return nil
}

// Observe adds one observation to the histogram series.
func (r *Registry) Observe(name string, values []string, v float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.hists[name]
	if !ok {
		return fmt.Errorf("monitoring: kayıtsız histogram %q", name)
	}
	if len(values) != len(f.labels) {
		return fmt.Errorf("monitoring: %q label sayısı uyuşmuyor", name)
	}
	key := seriesKey(values)
	s, exists := f.series[key]
	if !exists {
		if len(f.series) >= maxSeriesPerFamily {
			return ErrCardinalityBound
		}
		s = &histSample{buckets: make([]uint64, len(f.boundaries))}
		f.series[key] = s
	}
	for i, bound := range f.boundaries {
		if v <= bound {
			s.buckets[i]++
		}
	}
	s.count++
	s.sum += v
	return nil
}

// Counter registers (or reuses) a counter family.
func (r *Registry) Counter(name, help string, labelNames ...string) error {
	if err := checkLabels(name, labelNames); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if f, ok := r.counters[name]; ok {
		if f.help != help || strings.Join(f.labels, ",") != strings.Join(labelNames, ",") {
			return fmt.Errorf("monitoring: %q çelişkili tanım", name)
		}
		return nil
	}
	r.counters[name] = &counterFamily{name: name, help: help, labels: labelNames, series: map[string]*float64{}}
	return nil
}

// Gauge registers (or reuses) a gauge family.
func (r *Registry) Gauge(name, help string, labelNames ...string) error {
	if err := checkLabels(name, labelNames); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if f, ok := r.gauges[name]; ok {
		if f.help != help || strings.Join(f.labels, ",") != strings.Join(labelNames, ",") {
			return fmt.Errorf("monitoring: %q çelişkili tanım", name)
		}
		return nil
	}
	r.gauges[name] = &gaugeFamily{name: name, help: help, labels: labelNames, series: map[string]*gaugeValue{}}
	return nil
}

// Histogram registers (or reuses) a histogram family with fixed boundaries.
func (r *Registry) Histogram(name, help string, boundaries []float64, labelNames ...string) error {
	if err := checkLabels(name, labelNames); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if f, ok := r.hists[name]; ok {
		if f.help != help || strings.Join(f.labels, ",") != strings.Join(labelNames, ",") {
			return fmt.Errorf("monitoring: %q çelişkili tanım", name)
		}
		return nil
	}
	r.hists[name] = &histFamily{name: name, help: help, labels: labelNames, boundaries: boundaries, series: map[string]*histSample{}}
	return nil
}

// renderLabelBlock renders `{k="v",...}` (empty string when unlabeled).
func renderLabelBlock(names, values []string) string {
	if len(names) == 0 {
		return ""
	}
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf(`%s="%s"`, n, escapeLabelValue(values[i]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// Render produces the Prometheus text exposition of all registered metrics,
// deterministic (families and series sorted by name/label values).
func (r *Registry) Render() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var sb strings.Builder

	writeSeries := func(name, labels string, value float64) {
		sb.WriteString(name)
		sb.WriteString(labels)
		sb.WriteByte(' ')
		sb.WriteString(formatFloat(value))
		sb.WriteByte('\n')
	}

	// Counters.
	counterNames := make([]string, 0, len(r.counters))
	for k := range r.counters {
		counterNames = append(counterNames, k)
	}
	sort.Strings(counterNames)
	for _, name := range counterNames {
		f := r.counters[name]
		sb.WriteString(fmt.Sprintf("# HELP %s %s\n# TYPE %s counter\n", f.name, f.help, f.name))
		keys := counterSeriesKeysSorted(f.series)
		for _, k := range keys {
			writeSeries(f.name, renderLabelBlock(f.labels, strings.Split(k, "\x1f")), *f.series[k])
		}
	}
	// Gauges.
	gaugeNames := make([]string, 0, len(r.gauges))
	for k := range r.gauges {
		gaugeNames = append(gaugeNames, k)
	}
	sort.Strings(gaugeNames)
	for _, name := range gaugeNames {
		f := r.gauges[name]
		sb.WriteString(fmt.Sprintf("# HELP %s %s\n# TYPE %s gauge\n", f.name, f.help, f.name))
		keys := gaugeSeriesKeysSorted(f.series)
		for _, k := range keys {
			g := f.series[k]
			if g.ts > 0 {
				// Criterion 1: series carry their collection timestamp
				// (epoch milliseconds) per the text exposition format.
				sb.WriteString(f.name)
				sb.WriteString(renderLabelBlock(f.labels, strings.Split(k, "\x1f")))
				sb.WriteByte(' ')
				sb.WriteString(formatFloat(g.value))
				sb.WriteByte(' ')
				sb.WriteString(strconv.FormatInt(g.ts, 10))
				sb.WriteByte('\n')
				continue
			}
			writeSeries(f.name, renderLabelBlock(f.labels, strings.Split(k, "\x1f")), g.value)
		}
	}
	// Histograms.
	histNames := make([]string, 0, len(r.hists))
	for k := range r.hists {
		histNames = append(histNames, k)
	}
	sort.Strings(histNames)
	for _, name := range histNames {
		f := r.hists[name]
		sb.WriteString(fmt.Sprintf("# HELP %s %s\n# TYPE %s histogram\n", f.name, f.help, f.name))
		keys := histSeriesKeysSorted(f.series)
		for _, k := range keys {
			vals := strings.Split(k, "\x1f")
			lbl := renderLabelBlock(f.labels, vals)
			s := f.series[k]
			for i, bound := range f.boundaries {
				le := `le="` + formatFloat(bound) + `"`
				label := lbl
				if lbl == "" {
					label = "{" + le + "}"
				} else {
					label = lbl[:len(lbl)-1] + "," + le + "}"
				}
				writeSeries(f.name+"_bucket", label, float64(s.buckets[i]))
			}
			infLabel := lbl
			if lbl == "" {
				infLabel = `{le="+Inf"}`
			} else {
				infLabel = lbl[:len(lbl)-1] + `,le="+Inf"}`
			}
			writeSeries(f.name+"_bucket", infLabel, float64(s.count))
			writeSeries(f.name+"_sum", lbl, s.sum)
			writeSeries(f.name+"_count", lbl, float64(s.count))
		}
	}
	return sb.String()
}

func formatFloat(v float64) string {
	return fmt.Sprintf("%g", v)
}

func counterSeriesKeysSorted(m map[string]*float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func gaugeSeriesKeysSorted(m map[string]*gaugeValue) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func histSeriesKeysSorted(m map[string]*histSample) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
