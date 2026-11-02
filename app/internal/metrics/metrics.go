// Package metrics 是一个极小的 Prometheus text exposition 实现。
//
// 设计依据：docs/designs/20-可观测与指标.md。只支持 counter / gauge /
// histogram 三种类型，全部内存态，无第三方依赖。
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var defaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Registry 是进程内指标集合；Default 是全局实例。
type Registry struct {
	mu      sync.Mutex
	samples map[string]*sample
	hists   map[string]*histogram
}

type sample struct {
	name   string
	help   string
	typ    string
	labels map[string]string
	value  float64
}

type histogram struct {
	name    string
	help    string
	labels  map[string]string
	buckets []float64
	counts  []uint64
	sum     float64
	count   uint64
}

// Default 是全局注册表；HTTP 与网关直接写它。
var Default = NewRegistry()

// NewRegistry 构造空注册表。
func NewRegistry() *Registry {
	return &Registry{samples: map[string]*sample{}, hists: map[string]*histogram{}}
}

// Inc 给全局 counter 加 1。
func Inc(name, help string, labels map[string]string) {
	Default.Add(name, help, labels, 1)
}

// Add 给全局 counter 加 delta。
func Add(name, help string, labels map[string]string, delta float64) {
	Default.Add(name, help, labels, delta)
}

// Set 设置全局 gauge。
func Set(name, help string, labels map[string]string, value float64) {
	Default.Set(name, help, labels, value)
}

// Observe 向全局 histogram 记一个值。
func Observe(name, help string, labels map[string]string, value float64) {
	Default.Observe(name, help, labels, value)
}

// Add 累加一个 counter。
func (r *Registry) Add(name, help string, labels map[string]string, delta float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := "c\x00" + name + "\x00" + labelsKey(labels)
	s := r.samples[key]
	if s == nil {
		s = &sample{name: name, help: help, typ: "counter", labels: cloneLabels(labels)}
		r.samples[key] = s
	}
	s.value += delta
}

// Set 设置一个 gauge。
func (r *Registry) Set(name, help string, labels map[string]string, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := "g\x00" + name + "\x00" + labelsKey(labels)
	s := r.samples[key]
	if s == nil {
		s = &sample{name: name, help: help, typ: "gauge", labels: cloneLabels(labels)}
		r.samples[key] = s
	}
	s.value = value
}

// Observe 记一个 histogram 值。
func (r *Registry) Observe(name, help string, labels map[string]string, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := name + "\x00" + labelsKey(labels)
	h := r.hists[key]
	if h == nil {
		h = &histogram{
			name:    name,
			help:    help,
			labels:  cloneLabels(labels),
			buckets: append([]float64(nil), defaultBuckets...),
			counts:  make([]uint64, len(defaultBuckets)),
		}
		r.hists[key] = h
	}
	for i, bound := range h.buckets {
		if value <= bound {
			h.counts[i]++
		}
	}
	h.sum += value
	h.count++
}

// Reset 清空注册表（测试用）。
func (r *Registry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples = map[string]*sample{}
	r.hists = map[string]*histogram{}
}

// WriteText 输出 Prometheus text exposition 0.0.4。
func (r *Registry) WriteText(w io.Writer) error {
	r.mu.Lock()
	samples := make([]*sample, 0, len(r.samples))
	for _, s := range r.samples {
		samples = append(samples, &sample{name: s.name, help: s.help, typ: s.typ, labels: cloneLabels(s.labels), value: s.value})
	}
	hists := make([]*histogram, 0, len(r.hists))
	for _, h := range r.hists {
		copyH := &histogram{name: h.name, help: h.help, labels: cloneLabels(h.labels), buckets: append([]float64(nil), h.buckets...), counts: append([]uint64(nil), h.counts...), sum: h.sum, count: h.count}
		hists = append(hists, copyH)
	}
	r.mu.Unlock()

	names := make(map[string]bool, len(samples)+len(hists))
	for _, s := range samples {
		names[s.name] = true
	}
	for _, h := range hists {
		names[h.name] = true
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	for _, name := range ordered {
		help := ""
		for _, s := range samples {
			if s.name == name {
				help = s.help
				break
			}
		}
		if help == "" {
			for _, h := range hists {
				if h.name == name {
					help = h.help
					break
				}
			}
		}
		if help != "" {
			if _, err := fmt.Fprintf(w, "# HELP %s %s\n", name, escapeHelp(help)); err != nil {
				return err
			}
		}
		typ := "counter"
		for _, s := range samples {
			if s.name == name {
				typ = s.typ
				break
			}
		}
		for _, h := range hists {
			if h.name == name {
				typ = "histogram"
				break
			}
		}
		if _, err := fmt.Fprintf(w, "# TYPE %s %s\n", name, typ); err != nil {
			return err
		}

		for _, s := range samples {
			if s.name != name {
				continue
			}
			if _, err := fmt.Fprintf(w, "%s%s %s\n", s.name, formatLabels(s.labels), formatFloat(s.value)); err != nil {
				return err
			}
		}
		for _, h := range hists {
			if h.name != name {
				continue
			}
			for i, bound := range h.buckets {
				labels := cloneLabels(h.labels)
				labels["le"] = formatFloat(bound)
				if _, err := fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, formatLabels(labels), h.counts[i]); err != nil {
					return err
				}
			}
			labels := cloneLabels(h.labels)
			labels["le"] = "+Inf"
			if _, err := fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, formatLabels(labels), h.count); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "%s_sum%s %s\n", h.name, formatLabels(h.labels), formatFloat(h.sum)); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "%s_count%s %d\n", h.name, formatLabels(h.labels), h.count); err != nil {
				return err
			}
		}
	}
	return nil
}

func cloneLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func labelsKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
		b.WriteByte('\x00')
	}
	return b.String()
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// le 放在最后，便于阅读。
	for i, k := range keys {
		if k == "le" {
			keys = append(keys[:i], keys[i+1:]...)
			keys = append(keys, "le")
			break
		}
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteString("=\"")
		b.WriteString(escapeLabel(labels[k]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return value
}

func escapeHelp(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return value
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}
