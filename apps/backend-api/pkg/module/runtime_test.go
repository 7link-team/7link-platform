package module

import "testing"

func TestRuntimeIsAValueSnapshot(t *testing.T) {
	rt := Runtime{Name: "api", Version: "1.0.0", DB: "router"}
	if rt.Name != "api" || rt.Version != "1.0.0" || rt.DB != "router" {
		t.Fatalf("Runtime fields not preserved: %+v", rt)
	}
	// Runtime carries no shared state by itself; the DB field is an opaque value
	// that database-consuming modules narrow at construction.
	empty := Runtime{}
	if empty.Name != "" || empty.Version != "" || empty.DB != nil {
		t.Fatalf("zero-value Runtime is not empty: %+v", empty)
	}
}
