package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// ProjectControlNotifications resolves actual persisted control episodes.
// Reading a source never locks its project before a recipient principal.
type ProjectControlNotifications struct {
	AutonomyOn func(context.Context, *gorm.DB, guid.ID, guid.ID, bool, int64) (bool, error)
	AndonOn    func(context.Context, *gorm.DB, guid.ID, guid.ID) (ProjectControlFact, bool, error)
	ModeOn     func(context.Context, *gorm.DB, guid.ID, guid.ID) (ProjectControlFact, bool, error)
	ReadableOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
}
type ProjectControlFact struct {
	ProjectID, OccurrenceID guid.ID
	CreatedAt               time.Time
	ToMode                  string
}

func (m *Module) provideProjectControls(s *module.Services) error {
	return module.Provide[ProjectControlNotifications](s, ProjectControlNotifications{AutonomyOn: m.projectAutonomyFactOn, AndonOn: m.projectAndonFactOn, ModeOn: m.projectModeFactOn, ReadableOn: m.projectControlReadableOn})
}

// Autonomy notifications describe an actual transition event, while delivery
// rechecks the current restriction before displaying or emailing that notice.
func (m *Module) projectAutonomyFactOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, held bool, version int64) (bool, error) {
	if tx == nil || tenant <= 0 || project <= 0 {
		return false, ErrInvalidArgument
	}
	if version <= 0 {
		return false, nil
	}
	q := query.BindProjects(tx, m.service.names)
	p, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.TeamMode.Eq("AUTONOMOUS"), q.ArchivedAt.IsNull(), q.Stage.NotIn("CLOSED", "CANCELLED")).First() //nolint:misspell // Frozen protocol state.
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if p.AutonomyVersion == nil || *p.AutonomyVersion != version {
		return false, nil
	}
	var holds []struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal([]byte(p.Holds), &holds) != nil {
		return false, ErrUnavailable
	}
	current := false
	for _, h := range holds {
		current = current || h.Kind == autonomyHold
	}
	return current == held, nil
}
func (m *Module) projectAndonFactOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (ProjectControlFact, bool, error) {
	var out ProjectControlFact
	if tx == nil || tenant <= 0 || project <= 0 {
		return out, false, ErrInvalidArgument
	}
	q := query.BindProjects(tx, m.service.names)
	p, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	var holds []projectAndonHold
	if json.Unmarshal([]byte(p.Holds), &holds) != nil {
		return out, false, ErrUnavailable
	}
	for _, h := range holds {
		if h.Kind == "MANUAL" && h.Source == "ANDON" && h.ID > 0 {
			return ProjectControlFact{ProjectID: project, OccurrenceID: h.ID, CreatedAt: h.Since}, true, nil
		}
	}
	return out, false, nil
}
func (m *Module) projectModeFactOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (ProjectControlFact, bool, error) {
	var out ProjectControlFact
	if tx == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalidArgument
	}
	q := query.BindModeChanges(tx, m.service.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	return ProjectControlFact{ProjectID: r.ProjectID, OccurrenceID: r.ID, CreatedAt: r.OccurredAt, ToMode: r.ToMode}, true, nil
}
func (m *Module) projectControlReadableOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID) (bool, error) {
	if actor <= 0 {
		return false, nil
	}
	if m.principals.ActiveHumanByID == nil || m.members == nil {
		return false, ErrUnavailable
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil || !active || human.ID != actor {
		return false, err
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil || !active {
		return false, err
	}
	readCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal})
	err = m.constitutionReadProjectOn(readCtx, tx, tenant, project, actor)
	if errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectNotFound) || errors.Is(err, ErrInvalidMember) || errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}
