package yunxiao

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) bindIntakeTiming(s *module.Services) error {
	c, err := module.Resolve[intake.ChannelTimingConnector](s)
	if err != nil {
		return err
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	if err = c.Bind(m.clarificationTimingOn); err != nil {
		return err
	}
	triage, err := module.Resolve[intake.TriageAgentConnector](s)
	// Timing-only compositions predate autonomous intake routing. They retain
	// timing support; Intake's unbound wake target remains explicitly unavailable.
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if triage.Bind == nil {
		return ErrUnavailable
	}
	if err = triage.Bind(m.intakeTriageCoordinatorOn); err != nil {
		return err
	}
	if triage.BindRequestCoordinator == nil {
		return ErrUnavailable
	}
	return triage.BindRequestCoordinator(m.intakeRequestedCoordinatorOn)
}

func (m *Module) intakeTriageCoordinatorOn(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, kind string) (guid.ID, bool, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || channel <= 0 {
		return 0, false, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, false, ErrInvalid
	}
	q := query.BindIntakeChannels(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(channel), q.Kind.Eq(kind)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	view, err := channelView(row)
	if err != nil {
		return 0, false, err
	}
	if err = validateIntakeSettings(view.Settings); err != nil {
		return 0, false, err
	}
	if !view.Settings.Enabled || view.Settings.TriageAgentID == nil {
		return 0, false, nil
	}
	if err = m.intakeTriageAgentOn(ctx, tx, tenant, view.Settings); err != nil {
		return 0, false, err
	}
	return *view.Settings.TriageAgentID, true, nil
}

// Reads only the exact authoritative policy referenced by Intake. Local/shared
// channels without a configured ingress policy deliberately have no deadline.
// The existing settings contract has no reminder-count field: the specified
// default of two is used, while interval and timeout always come from storage.
func (m *Module) clarificationTimingOn(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, kind string) (intake.ClarificationTiming, bool, error) {
	out := intake.ClarificationTiming{}
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || channel <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return out, false, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalid
	}
	q := query.BindIntakeChannels(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(channel), q.Kind.Eq(kind)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	view, err := channelView(row)
	if err != nil {
		return out, false, err
	}
	if err = validateIntakeSettings(view.Settings); err != nil {
		return out, false, err
	}
	return intake.ClarificationTiming{ChannelID: row.ID, ChannelVersion: row.Version, ReminderHours: view.Settings.ReminderHours, TimeoutHours: view.Settings.TimeoutHours, MaxReminders: 2}, true, nil
}

// Match receiving/policy replacement: coordinator participants precede project
// and channel locks. Compare the locked policy before using discovered facts.
func (m *Module) intakeRequestedCoordinatorOn(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, kind string, authorize intake.TriageRequestAuthority) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || channel <= 0 || authorize == nil {
		return ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	q := query.BindIntakeChannels(tx, m.names)
	hint, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(channel), q.Kind.Eq(kind)).Take()
	if err != nil {
		return err
	}
	view, err := channelView(hint)
	if err != nil {
		return err
	}
	if err = validateIntakeSettings(view.Settings); err != nil {
		return err
	}
	if !view.Settings.Enabled || view.Settings.TriageAgentID == nil {
		return intake.ErrInvalidArgument
	}
	if err = m.intakeTriageAgentOn(ctx, tx, tenant, view.Settings); err != nil {
		return err
	}
	if err = authorize(ctx, tx, tenant, project, "intake_triage"); err != nil {
		return err
	}
	current, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(channel), q.Kind.Eq(kind)).Take()
	if err != nil {
		return err
	}
	if current.Version != hint.Version || current.Document != hint.Document {
		return ErrConflict
	}
	return nil
}
