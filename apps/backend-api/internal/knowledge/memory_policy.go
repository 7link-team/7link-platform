package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
)

var ErrPolicyUnsupported = errors.New("knowledge: memory policy unsupported")

// MemoryPolicyValidator validates Agent-owned policy without database access.
// STANDARD is the current profile envelope; the flat form remains compatible
// with the knowledge policy contract. Unsupported retrieval never silently falls back.
type MemoryPolicyValidator struct {
	Validate func(context.Context, guid.ID, json.RawMessage) error
}

type memoryPolicyOptions struct {
	RetrievalPolicy  string `json:"retrieval_policy"`
	RequiresApproval *bool  `json:"requires_approval_for_rules"`
	TTLDays          *int   `json:"ttl_days"`
	MaxItems         *int   `json:"max_items"`
}

func decodeMemoryPolicy(raw []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return ErrInvalid
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

func validateMemoryPolicy(_ context.Context, tenant guid.ID, raw json.RawMessage) error {
	if tenant <= 0 || len(raw) == 0 || len(raw) > 4096 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ErrInvalid
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return ErrInvalid
	}
	var options memoryPolicyOptions
	if _, profile := fields["profile"]; profile {
		var envelope struct {
			Profile   string          `json:"profile"`
			Overrides json.RawMessage `json:"overrides"`
		}
		if err := decodeMemoryPolicy(raw, &envelope); err != nil {
			return err
		}
		if envelope.Profile != "STANDARD" {
			return ErrPolicyUnsupported
		}
		options.RetrievalPolicy = "RECENT"
		if len(envelope.Overrides) > 0 {
			if bytes.Equal(bytes.TrimSpace(envelope.Overrides), []byte("null")) {
				return ErrInvalid
			}
			if err := decodeMemoryPolicy(envelope.Overrides, &options); err != nil {
				return err
			}
		}
	} else if err := decodeMemoryPolicy(raw, &options); err != nil {
		return err
	}
	if options.RetrievalPolicy != "RECENT" {
		return ErrPolicyUnsupported
	}
	if options.TTLDays != nil && (*options.TTLDays < 1 || *options.TTLDays > 3650) || options.MaxItems != nil && (*options.MaxItems < 1 || *options.MaxItems > 500) {
		return ErrInvalid
	}
	// RULE approval is an independent Human gate and cannot be disabled by an
	// Agent profile. All current STANDARD policies preserve that gate.
	if options.RequiresApproval != nil && !*options.RequiresApproval {
		return ErrPolicyUnsupported
	}
	return nil
}

func (m *Module) provideMemoryPolicy(s *module.Services) error {
	return module.Provide[MemoryPolicyValidator](s, MemoryPolicyValidator{Validate: validateMemoryPolicy})
}
