package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// AcceptanceReminderProjects is a trusted system capability. Clientwork holds
// the current published package/receipt facts in the same transaction. Project
// serialization always precedes package locks, matching ordinary commands.
type AcceptanceReminderProjects struct {
	CurrentOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (bool, error)
}

func (m *Module) provideAcceptanceReminderProjects(s *module.Services) error {
	return module.Provide[AcceptanceReminderProjects](s, AcceptanceReminderProjects{CurrentOn: m.acceptanceReminderProjectOn})
}
func (m *Module) acceptanceReminderProjectOn(ctx context.Context, tx *gorm.DB, tenant, project, pkg guid.ID, overdue bool) (bool, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || pkg <= 0 {
		return false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return false, ErrInvalidArgument
	}
	if m == nil || m.service == nil {
		return false, ErrUnavailable
	}
	q := query.BindProjects(tx, m.service.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if row.ArchivedAt != nil || row.Stage == "CLOSED" || row.Stage == "CANCELLED" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		return false, nil
	}
	if !overdue {
		return true, nil
	}
	var reasons []map[string]json.RawMessage
	if json.Unmarshal([]byte(row.HealthReasons), &reasons) != nil {
		return false, ErrUnavailable
	}
	id, _ := json.Marshal(pkg)
	found := false
	for _, r := range reasons {
		if string(r["code"]) == `"ACCEPTANCE_OVERDUE"` && string(r["package_id"]) == string(id) {
			found = true
		}
	}
	if !found {
		reasons = append(reasons, map[string]json.RawMessage{"code": json.RawMessage(`"ACCEPTANCE_OVERDUE"`), "package_id": id})
	}
	data, err := json.Marshal(reasons)
	if err != nil {
		return false, err
	}
	_, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).UpdateSimple(q.Health.Value("RED"), q.HealthReasons.Value(string(data)), q.HealthRefreshedAt.Value(time.Now().UTC().Truncate(time.Millisecond)))
	return err == nil, err
}
