package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"time"
)

type AcceptanceReminderView struct {
	ID          guid.ID    `json:"id"`
	Day         int        `json:"day"`
	Kind        string     `json:"kind"`
	DueAt       time.Time  `json:"due_at"`
	EmittedAt   *time.Time `json:"emitted_at"`
	CancelledAt *time.Time `json:"cancelled_at"` //nolint:misspell // Preserve the published domain or serialized contract spelling.
}

func (m *Module) registerAcceptanceRemindersHTTP(api huma.API) {
	m.registerAcceptanceMetricsHTTP(api)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package.acceptance-reminders", Method: "GET", Path: entityBase + "/delivery-packages/{id}/acceptance-reminders", Summary: "Read the frozen in-app publication reminder schedule", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "delivery", Verb: "read"}, m.acceptanceRemindersHTTP)
}
func (m *Module) acceptanceRemindersHTTP(ctx context.Context, in *PackageTimelineInput) (*respx.Body[Page[AcceptanceReminderView]], error) {
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return readDomain(ctx, m, &in.ListInput, "acceptance-reminders:"+id.String(), "delivery_read", true, func(tx *gorm.DB, external bool, last guid.ID, n int) ([]AcceptanceReminderView, []guid.ID, error) {
		project, e := guid.RequireID(in.EntityID)
		if e != nil {
			return nil, nil, ErrInvalid
		}
		pkg, e := m.packageOn(ctx, tx, project, id, external)
		if e != nil {
			return nil, nil, e
		}
		q := query.BindAcceptanceReminder(tx, m.names)
		base := q.WithContext(ctx).Where(q.TenantID.Eq(pkg.TenantID), q.ProjectID.Eq(project), q.PackageID.Eq(id), q.ID.Gt(last))
		if external {
			base = base.Where(q.Kind.Eq("REMINDER"))
		}
		rows, e := base.Order(q.ID).Limit(n).Find()
		if e != nil {
			return nil, nil, e
		}
		out := make([]AcceptanceReminderView, 0, len(rows))
		ids := make([]guid.ID, 0, len(rows))
		for _, r := range rows {
			out = append(out, AcceptanceReminderView{r.ID, r.Day, r.Kind, r.DueAt, r.EmittedAt, r.CancelledAt})
			ids = append(ids, r.ID)
		}
		return out, ids, nil
	})
}
