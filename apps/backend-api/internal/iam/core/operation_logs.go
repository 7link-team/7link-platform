package core

import (
	"context"
	"errors"
	"net/http"

	"7link.com/platform/internal/infra/audit"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
)

type operationLogsAdapter struct {
	enabled   bool
	read      audit.CorrelatedOperations
	registrar *authz.Registrar
}

func (*operationLogsAdapter) ModuleName() string           { return "operation-logs" }
func (m *operationLogsAdapter) Enabled() bool              { return m.enabled }
func (*operationLogsAdapter) ModuleDependencies() []string { return []string{audit.Name, Name} }
func (m *operationLogsAdapter) InjectServices(s *module.Services) error {
	var err error
	if m.read, err = module.Resolve[audit.CorrelatedOperations](s); err != nil {
		return err
	}
	m.registrar, err = module.Resolve[*authz.Registrar](s)
	return err
}

type operationLogsInput struct {
	CorrelationID string `query:"correlation_id" required:"true" minLength:"1" maxLength:"64" pattern:"^[A-Za-z0-9:.-]+$"`
	Offset        int    `query:"offset" default:"0" minimum:"0" maximum:"1000000"`
	Limit         int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}

func (m *operationLogsAdapter) RegisterHTTP(_ context.Context, api huma.API) error {
	if m.read == nil || m.registrar == nil {
		return audit.ErrDatabaseSinkUnavailable
	}
	authz.RegisterPlatform(m.registrar, api, huma.Operation{OperationID: "audit.correlated-operations", Method: http.MethodGet, Path: "/system/operation-logs", Summary: "Find operation summaries by job correlation", Tags: []string{"audit"}, Errors: []int{401, 403, 422, 503}}, authz.Guard{Resource: "system_audit_event", Verb: "view"}, func(ctx context.Context, in *operationLogsInput) (*respx.Body[[]audit.OperationSummary], error) {
		rows, total, err := m.read(ctx, in.CorrelationID, in.Offset, in.Limit)
		if errors.Is(err, audit.ErrInvalidCorrelation) {
			return nil, huma.Error422UnprocessableEntity(respx.InvalidID)
		}
		if err != nil {
			return nil, huma.Error503ServiceUnavailable(respx.ServiceUnavailable)
		}
		return respx.List(ctx, rows, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(rows), Total: total}), nil
	})
	return nil
}

var (
	_ module.Enabler         = (*operationLogsAdapter)(nil)
	_ module.Depender        = (*operationLogsAdapter)(nil)
	_ module.ServiceInjector = (*operationLogsAdapter)(nil)
	_ module.HTTPRegistrar   = (*operationLogsAdapter)(nil)
)

// NewOperationLogs registers the existing capability adapter.
func NewOperationLogs(enabled bool) module.Module { return &operationLogsAdapter{enabled: enabled} }
