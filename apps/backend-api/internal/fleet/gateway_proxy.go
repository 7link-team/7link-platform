package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GatewaySettlement func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64) error
type GatewaySettlementConnector struct{ Bind func(GatewaySettlement) error }

func (m *Module) provideGatewaySettlement(s *module.Services) error {
	return module.Provide[GatewaySettlementConnector](s, GatewaySettlementConnector{Bind: func(p GatewaySettlement) error {
		if p == nil || m.gatewaySettlement != nil {
			return ErrUnavailable
		}
		m.gatewaySettlement = p
		return nil
	}})
}

// The payload lives only for the HTTP request; neither prompts nor upstream
// response bytes enter the ledger, audit, outbox or idempotency receipt.
type GatewayProxyInput struct {
	ProjectID       guid.ID         `json:"project_id"`
	MaxInputTokens  int64           `json:"max_input_tokens" minimum:"1" maximum:"1000000"`
	MaxOutputTokens int64           `json:"max_output_tokens" minimum:"1" maximum:"1000000"`
	Request         json.RawMessage `json:"request"`
}

// TransformSchema documents exactly the text-only upstream subset accepted by
// admission while keeping the original JSON bytes for strict field validation.
func (GatewayProxyInput) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Properties["request"] = &huma.Schema{Type: "object", AdditionalProperties: false, Required: []string{"input"}, Properties: map[string]*huma.Schema{
		"input":             {Type: "string", Description: "Text input; UTF-8 bytes must not exceed max_input_tokens"},
		"model":             {Type: "string", Description: "Configured model, defaults to the tenant default"},
		"stream":            {Type: "boolean"},
		"store":             {Type: "boolean", Description: "Always forced to false"},
		"max_output_tokens": {Type: "integer", Description: "Overridden by the outer reservation cap"},
	}}
	return schema
}

type gatewayRoute struct {
	request  *model.GatewayRequest
	provider GatewayProvider
	price    GatewayModel
	key      string
	body     []byte
	stream   bool
}

func (m *Module) prepareGatewayProxyOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in GatewayProxyInput) (gatewayRoute, error) {
	var route gatewayRoute
	if err := fleetTransaction(tx, tenant, in.ProjectID); err != nil {
		return route, err
	}
	if in.MaxInputTokens < 1 || in.MaxInputTokens > 1000000 || in.MaxOutputTokens < 1 || in.MaxOutputTokens > 1000000 || len(in.Request) > 1024*1024 {
		return route, ErrInvalidArgument
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "gateway_manage"); err != nil {
		return route, err
	}
	config, err := m.gatewayOn(ctx, tx, tenant, true)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return route, ErrUnavailable
	}
	if err != nil {
		return route, err
	}
	v, err := gatewayView(config)
	if err != nil {
		return route, err
	}
	if !v.Enabled || m.gatewaySettlement == nil || m.gatewayBudgets.ReserveOn == nil {
		return route, ErrUnavailable
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(in.Request, &body) != nil || body == nil {
		return route, ErrInvalidArgument
	}
	for field := range body {
		switch field {
		case "input", "model", "stream", "store", "max_output_tokens":
		default:
			return route, ErrInvalidArgument
		}
	}
	var modelID string
	if raw, ok := body["model"]; ok && json.Unmarshal(raw, &modelID) != nil {
		return route, ErrInvalidArgument
	}
	if modelID == "" {
		modelID = v.DefaultModel
	}
	if raw, ok := body["stream"]; ok && json.Unmarshal(raw, &route.stream) != nil {
		return route, ErrInvalidArgument
	}
	for _, price := range v.Models {
		if price.ID == modelID {
			route.price = price
			break
		}
	}
	if route.price.ID == "" {
		return route, ErrInvalidArgument
	}
	for _, provider := range v.Providers {
		if provider.ID == route.price.Provider {
			route.provider = provider
			break
		}
	}
	// Providers use the Responses protocol. Claude Code providers require a
	// compatible endpoint; no fabricated conversion to the Messages protocol.
	u, err := url.Parse(route.provider.BaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return route, ErrUnavailable
	}
	if route.provider.KeyRef != nil {
		q := query.BindGatewayKeys(tx, m.names)
		key, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(*route.provider.KeyRef), q.RevokedAt.IsNull()).First()
		if err != nil {
			return route, ErrUnavailable
		}
		plain, err := m.openGatewayKey(tenant, key)
		if err != nil {
			return route, ErrUnavailable
		}
		route.key = string(plain)
		clear(plain)
	}
	body["model"], _ = json.Marshal(modelID)
	// Admission and the upstream output cap must agree; callers cannot forward
	// an unbounded generation after reserving a small request.
	body["max_output_tokens"], _ = json.Marshal(in.MaxOutputTokens)
	body["store"] = json.RawMessage("false")
	if _, ok := body["input"]; !ok {
		return route, ErrInvalidArgument
	}
	for _, field := range []string{"previous_response_id", "background"} {
		if _, ok := body[field]; ok {
			return route, ErrInvalidArgument
		}
	}
	route.body, err = json.Marshal(body)
	if err != nil {
		return route, err
	}
	// A UTF-8 byte upper bound is conservative for text tokens. Arbitrary
	// multimodal/opaque inputs cannot be budgeted by this path.
	var text string
	if json.Unmarshal(body["input"], &text) != nil || int64(len([]byte(text))) > in.MaxInputTokens {
		return route, ErrInvalidArgument
	}
	for _, field := range []string{"tools", "instructions"} {
		if len(body[field]) > 0 {
			return route, ErrInvalidArgument
		}
	}
	q := query.BindGatewayRequests(tx, m.names)
	active, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.In("RUNNING", "QUEUED")).Count()
	if err != nil {
		return route, err
	}
	if active >= 8 {
		return route, ErrVersionConflict
	}
	id, err := m.ids.Next()
	if err != nil {
		return route, err
	}
	if id <= 0 {
		return route, ErrUnavailable
	}
	cost, _, err := EstimateTokenCost(in.MaxInputTokens, in.MaxOutputTokens, &TokenPrice{InputMicros: max(route.price.InputMicros, route.price.CacheReadMicros, route.price.CacheWriteMicros), OutputMicros: route.price.OutputMicros})
	if err != nil {
		return route, err
	}
	var limit *int64
	for _, b := range v.Budgets {
		if b.ProjectID == in.ProjectID {
			limit = &b.LimitMicros
			break
		}
	}
	if err = m.gatewayBudgets.ReserveOn(ctx, tx, tenant, in.ProjectID, guid.ID(id), cost, limit); err != nil {
		return route, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	route.request = &model.GatewayRequest{ID: guid.ID(id), TenantID: tenant, ProjectID: &in.ProjectID, Revision: config.Revision, Model: modelID, State: "RUNNING", CreatedAt: now, StartedAt: &now}
	if err = q.WithContext(ctx).Create(route.request); err != nil {
		return route, err
	}
	return route, m.gatewayEventOn(ctx, tx, tenant, route.request.ID, "gateway.request_changed", 1)
}

// No redirects: forwarding Authorization to a redirect target would expose the
// sealed provider credential. Callers' cookies, machine tokens and headers are
// never copied. There is deliberately no automatic retry of uncertain requests.
func gatewayUpstream(ctx context.Context, route gatewayRoute, transport http.RoundTripper) (*http.Response, error) {
	u := strings.TrimRight(route.provider.BaseURL, "/") + "/responses"
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(route.body))
	if err != nil {
		return nil, ErrUnavailable
	}
	r.Header.Set("Content-Type", "application/json")
	if route.key != "" {
		r.Header.Set("Authorization", "Bearer "+route.key)
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(r)
	if err != nil {
		return nil, ErrUnavailable
	}
	return response, nil
}

type gatewayUsage struct {
	Input        *int64 `json:"input_tokens"`
	Output       *int64 `json:"output_tokens"`
	InputDetails struct {
		Cached     int64 `json:"cached_tokens"`
		CacheWrite int64 `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
}

func gatewayUsageCost(raw []byte, price GatewayModel) (int64, bool, bool) {
	var value struct {
		Type     string        `json:"type"`
		Status   string        `json:"status"`
		Usage    *gatewayUsage `json:"usage"`
		Response *struct {
			Status string        `json:"status"`
			Usage  *gatewayUsage `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return 0, false, false
	}
	usage, status := value.Usage, value.Status
	if value.Response != nil {
		status = value.Response.Status
		if value.Type != "response."+status {
			return 0, false, false
		}
		usage = value.Response.Usage
	}
	if status != "completed" && status != "incomplete" && status != "failed" {
		return 0, false, false
	}
	if usage == nil || usage.Input == nil || usage.Output == nil {
		return 0, false, false
	}
	read, write := usage.InputDetails.Cached, usage.InputDetails.CacheWrite
	if *usage.Input < 0 || *usage.Output < 0 || read < 0 || write < 0 || read > *usage.Input || write > *usage.Input-read {
		return 0, false, false
	}
	total := new(big.Int)
	for _, item := range [][2]int64{{*usage.Input - read - write, price.InputMicros}, {*usage.Output, price.OutputMicros}, {read, price.CacheReadMicros}, {write, price.CacheWriteMicros}} {
		if item[1] < 0 {
			return 0, false, false
		}
		total.Add(total, new(big.Int).Mul(big.NewInt(item[0]), big.NewInt(item[1])))
	}
	total.Add(total, big.NewInt(999999)).Quo(total, big.NewInt(1000000))
	if !total.IsInt64() {
		return 0, false, false
	}
	return total.Int64(), true, status == "completed"
}

// Copy and flush every read without accumulating the response. Only one bounded
// SSE line (or the bounded nonstream JSON) is held to observe usage in memory.
func copyGatewayResponse(out io.Writer, in io.Reader, stream bool, price GatewayModel, flush func() error) (int64, bool, bool, error) {
	buf := make([]byte, 16*1024)
	pending := []byte{}
	var cost int64
	known, completed := false, false
	total := 0
	for {
		n, err := in.Read(buf)
		if n > 0 {
			total += n
			if !stream && total > 900*1024 {
				return cost, known, completed, ErrInvalidArgument
			}
			pending = append(pending, buf[:n]...)
			if stream {
				for {
					i := bytes.IndexByte(pending, '\n')
					if i < 0 {
						break
					}
					if i > 900*1024 {
						return cost, known, completed, ErrInvalidArgument
					}
					line := bytes.TrimSpace(pending[:i])
					if bytes.HasPrefix(line, []byte("data:")) {
						if v, ok, done := gatewayUsageCost(bytes.TrimSpace(line[5:]), price); ok {
							cost, known, completed = v, true, done
						}
					}
					pending = pending[i+1:]
				}
				if len(pending) > 900*1024 {
					return cost, known, completed, ErrInvalidArgument
				}
			} else {
				cost, known, completed = gatewayUsageCost(pending, price)
			}
			written, writeErr := out.Write(buf[:n])
			if writeErr != nil {
				return cost, known, completed, writeErr
			}
			if written != n {
				return cost, known, completed, io.ErrShortWrite
			}
			if flush != nil {
				if flushErr := flush(); flushErr != nil {
					return cost, known, completed, flushErr
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if !stream {
					cost, known, completed = gatewayUsageCost(pending, price)
				}
				return cost, known, completed, nil
			}
			return cost, known, completed, err
		}
	}
}

func (m *Module) finishGatewayProxyOn(ctx context.Context, tx *gorm.DB, route gatewayRoute, cost int64, known, completed bool) error {
	r := route.request
	if r == nil || r.ProjectID == nil {
		return ErrInvalidArgument
	}
	if err := fleetTransaction(tx, r.TenantID, *r.ProjectID); err != nil {
		return err
	}
	q := query.BindGatewayRequests(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(r.ID)).First()
	if err != nil {
		return err
	}
	if row.State != "RUNNING" {
		return ErrVersionConflict
	}
	if known {
		if m.gatewaySettlement == nil {
			return ErrUnavailable
		}
		if err = m.gatewaySettlement(ctx, tx, r.TenantID, *r.ProjectID, r.ID, cost); err != nil {
			return err
		}
	}
	state, reason := "FAILED", "UPSTREAM_FAILED"
	if !known {
		reason = "USAGE_UNKNOWN"
	} else if completed {
		state, reason = "COMPLETED", ""
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(r.ID), q.State.Eq("RUNNING")).UpdateSimple(q.State.Value(state), q.Reason.Value(reason), q.FinishedAt.Value(now)); err != nil {
		return err
	}
	old, err := q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.State.In("COMPLETED", "FAILED", "CANCELLED")).Order(q.ID.Desc()).Offset(100).Find() //nolint:misspell // Frozen database protocol spelling.
	if err != nil {
		return err
	}
	for _, entry := range old {
		if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(entry.ID), q.State.In("COMPLETED", "FAILED", "CANCELLED")).Delete(); err != nil { //nolint:misspell // Frozen database protocol spelling.
			return err
		}
	}
	return m.gatewayEventOn(ctx, tx, r.TenantID, r.ID, "gateway.request_changed", 2)
}

func (m *Module) gatewayProxyCurrentOn(ctx context.Context, tx *gorm.DB, route gatewayRoute) error {
	r := route.request
	if r == nil {
		return ErrInvalidArgument
	}
	if err := m.controlAuthorityOn(ctx, tx, r.TenantID, "gateway_manage"); err != nil {
		return err
	}
	config, err := m.gatewayOn(ctx, tx, r.TenantID, false)
	if err != nil {
		return err
	}
	if !config.Enabled || config.Revision != r.Revision {
		return ErrVersionConflict
	}
	if route.provider.KeyRef != nil {
		q := query.BindGatewayKeys(tx, m.names)
		n, err := q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(*route.provider.KeyRef), q.RevokedAt.IsNull()).Count()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrVersionConflict
		}
	}
	return nil
}
