package executor

import (
	"encoding/hex"
	"strings"
)

// Correlation is copied from the server-frozen plan. It supplies W3C trace
// propagation to the actual process, independently of runtime log text.
type Correlation struct {
	TraceID      string `json:"trace_id"`
	SpanID       string `json:"span_id"`
	ParentSpanID string `json:"parent_span_id,omitempty"`
}

func validCorrelation(c *Correlation) bool {
	if c == nil {
		return true // Historical plans predate trace recording.
	}
	valid := func(value string, size int) bool {
		b, err := hex.DecodeString(value)
		return err == nil && len(b) == size && value == strings.ToLower(value) && strings.Trim(value, "0") != ""
	}
	return valid(c.TraceID, 16) && valid(c.SpanID, 8) && (c.ParentSpanID == "" || valid(c.ParentSpanID, 8))
}

func correlationEnvironment(env []string, c *Correlation) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "TRACEPARENT") && !strings.EqualFold(key, "TRACESTATE") && !strings.EqualFold(key, "BAGGAGE") {
			out = append(out, entry)
		}
	}
	if c != nil && validCorrelation(c) {
		out = append(out, "TRACEPARENT=00-"+c.TraceID+"-"+c.SpanID+"-01")
	}
	return out
}
