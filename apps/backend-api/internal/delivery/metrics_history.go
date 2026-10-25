package delivery

import (
	"context"
	"database/sql/driver"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type DeliveryQualityDay struct {
	Date                 string   `json:"date"`
	Received             int      `json:"received"`
	Returned             int      `json:"returned"`
	AcceptanceRate       *float64 `json:"acceptance_rate" doc:"Passed delivery receipts divided by passed receipts plus returns; unavailable without actual replies."`
	VerificationPassed   int      `json:"verification_passed"`
	VerificationFailed   int      `json:"verification_failed"`
	VerificationPassRate *float64 `json:"verification_pass_rate" doc:"Completed PASSED/FAILED internal verification attempt receipts, not an inferred suite or work-item verdict."`
}
type DeliveryQualityHistory struct {
	From time.Time            `json:"from"`
	To   time.Time            `json:"to"`
	Days []DeliveryQualityDay `json:"days"`
}
type deliveryQualityInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	From     string `query:"from" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
	To       string `query:"to" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
}

func (m *Module) deliveryQualityOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, from, to string) (DeliveryQualityHistory, error) {
	out := DeliveryQualityHistory{Days: []DeliveryQualityDay{}}
	now := time.Now().UTC().Truncate(24 * time.Hour)
	start, end := now.AddDate(0, 0, -7), now.AddDate(0, 0, 1)
	var err error
	if from != "" {
		start, err = time.Parse(time.DateOnly, from)
		if err != nil {
			return out, ErrInvalidArgument
		}
	}
	if to != "" {
		end, err = time.Parse(time.DateOnly, to)
		if err != nil {
			return out, ErrInvalidArgument
		}
	}
	if !start.Before(end) || end.Sub(start) > 93*24*time.Hour {
		return out, ErrInvalidArgument
	}
	// The project enumerator and per-work policy use current authorization, even
	// when the source receipts are historical. No cross-project owner table reads.
	projects, err := m.planningProjectsOn(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	out.From, out.To = start, end
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		out.Days = append(out.Days, DeliveryQualityDay{Date: day.Format(time.DateOnly)})
	}
	for _, project := range projects {
		if err = m.authorizeOn(ctx, tx, tenant, project, "work_item_read"); err != nil {
			return out, err
		}
		w := query.BindWorkItems(tx, m.service.names)
		policy, err := m.workPolicy(ctx, tx, tenant, project, "work_item_read", authn.PrincipalIDFromContext(ctx))
		if err != nil {
			return out, err
		}
		scope, err := workScope(policy, project, w.CreatedBy)
		if err != nil {
			return out, err
		}
		stmt := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ArchivedAt.IsNull())
		if scope != nil {
			stmt = stmt.Where(scope)
		}
		rows, err := stmt.Limit(10001).Find()
		if err != nil {
			return out, err
		}
		if len(rows) > 10000 {
			return out, ErrUnavailable
		}
		ids := make([]driver.Valuer, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		if len(ids) == 0 {
			continue
		}
		a := query.BindResultActions(tx, m.service.names)
		actions, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.WorkItemID.In(ids...), a.CreatedAt.Gte(start), a.CreatedAt.Lt(end), a.Kind.In("RECEIPT", "RETURN")).Limit(10001).Find()
		if err != nil {
			return out, err
		}
		if len(actions) > 10000 {
			return out, ErrUnavailable
		}
		for _, r := range actions {
			day := &out.Days[int(r.CreatedAt.UTC().Sub(start)/(24*time.Hour))]
			if r.Kind == "RECEIPT" && r.Verdict == "PASSED" {
				day.Received++
			}
			if r.Kind == "RETURN" && r.Verdict == "FAILED" {
				day.Returned++
			}
		}
		v := query.BindVerificationRuns(tx, m.service.names)
		runs, err := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ProjectID.Eq(project), v.WorkItemID.In(ids...), v.CompletedAt.Gte(start), v.CompletedAt.Lt(end), v.State.Eq("DONE"), v.Stage.In("AUTOMATED", "AGENT_TEST", "AGENT_ACCEPTANCE"), v.Verdict.In("PASSED", "FAILED")).Limit(10001).Find()
		if err != nil {
			return out, err
		}
		if len(runs) > 10000 {
			return out, ErrUnavailable
		}
		for _, r := range runs {
			if r.CompletedAt == nil || len(r.ReceiptHash) != 64 {
				continue
			}
			day := &out.Days[int(r.CompletedAt.UTC().Sub(start)/(24*time.Hour))]
			if r.Verdict == "PASSED" {
				day.VerificationPassed++
			} else {
				day.VerificationFailed++
			}
		}
	}
	for i := range out.Days {
		day := &out.Days[i]
		if n := day.Received + day.Returned; n > 0 {
			rate := float64(day.Received) / float64(n)
			day.AcceptanceRate = &rate
		}
		if n := day.VerificationPassed + day.VerificationFailed; n > 0 {
			rate := float64(day.VerificationPassed) / float64(n)
			day.VerificationPassRate = &rate
		}
	}
	return out, nil
}

func (m *Module) registerDeliveryQualityHTTP(api huma.API) {
	m.registerProjectMetricsHTTP(api)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "delivery.metrics.quality", Method: "GET", Path: "/metrics/delivery-quality", Summary: "Read historical delivery replies and completed internal verification receipts", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 422, 500, 503}}, authz.Guard{Resource: "work_item", Verb: "read"}, func(ctx context.Context, in *deliveryQualityInput) (*respx.Body[DeliveryQualityHistory], error) {
		tenant, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, projectHTTPError(ctx, ErrInvalidArgument)
		}
		if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
			return nil, projectHTTPError(ctx, ErrUnavailable)
		}
		var out DeliveryQualityHistory
		err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.deliveryQualityOn(ctx, tx, tenant, in.From, in.To)
			return err
		})
		if err != nil {
			return nil, projectHTTPError(ctx, err)
		}
		return respx.OK(ctx, out), nil
	})
}
