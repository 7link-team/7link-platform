package audit

import "context"

// OmitConfidentialRequest discards already captured bytes before the audit
// adapter persists them. Call at the resource boundary before parsing or
// authorization, so even malformed and rejected confidential writes are safe.
func OmitConfidentialRequest(ctx context.Context) {
	entry, ok := inflightFromContext(ctx)
	if !ok {
		return
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	entry.reqBody = []byte(`{"omitted":"confidential_resource"}`)
	entry.reqLimited = false
	if entry.event.Metadata == nil {
		entry.event.Metadata = make(map[string]string)
	}
	entry.event.Metadata["confidential_resource"] = "true"
}

// Error schemas can echo rejected field values; suppress both response bodies
// and success-envelope messages at the same confidential resource boundary.
func omitConfidentialResponse(entry *inflight, event *Event) {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.event.Metadata["confidential_resource"] != "true" {
		return
	}
	event.Response = Payload{Size: event.Response.Size, Body: `{"omitted":"confidential_resource"}`}
	event.Message = ""
}
