package intake

import (
	"context"
	"errors"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TriageAgentReader resolves the current enabled source channel and validates
// its configured coordinator. A missing coordinator never implies AI success.
type TriageAgentReader func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (guid.ID, bool, error)
type TriageRequestAuthority func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error
type TriageRequestCoordinator func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, TriageRequestAuthority) error
type TriageAgentConnector struct {
	Bind                   func(TriageAgentReader) error
	BindAuthority          func(TriageRequestAuthority) error
	BindRequestCoordinator func(TriageRequestCoordinator) error
}
type TriageWakeTarget struct {
	ReadOn             func(context.Context, *gorm.DB, events.Envelope) (guid.ID, bool, error)
	AuthorizeRequestOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
	RequestOn          func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (TriageRequest, error)
}

func (m *Module) provideTriageAgent(s *module.Services) error {
	if err := module.Provide[TriageWakeTarget](s, TriageWakeTarget{ReadOn: m.triageWakeTargetOn, AuthorizeRequestOn: m.authorizeTriageRequestOn, RequestOn: m.requestTriageOn}); err != nil {
		return err
	}
	return module.Provide[TriageAgentConnector](s, TriageAgentConnector{Bind: func(read TriageAgentReader) error {
		if read == nil || m.triageAgent != nil {
			return ErrInvalidArgument
		}
		m.triageAgent = read
		return nil
	}, BindAuthority: func(check TriageRequestAuthority) error {
		if check == nil || m.triageRequestAuthority != nil {
			return ErrInvalidArgument
		}
		m.triageRequestAuthority = check
		return nil
	}, BindRequestCoordinator: func(check TriageRequestCoordinator) error {
		if check == nil || m.triageRequestCoordinator != nil {
			return ErrInvalidArgument
		}
		m.triageRequestCoordinator = check
		return nil
	}})
}

// Only a canonical received source or accepted triage request can wake its Agent. The
// inbox provider deduplicates the event and publishes agents.wake atomically.
// This queues work; it neither invents a suggestion nor converts the source.
func (m *Module) triageWakeTargetOn(ctx context.Context, tx *gorm.DB, event events.Envelope) (guid.ID, bool, error) {
	if tx == nil || tx.Statement == nil || (event.EventType != "intake.received" && event.EventType != "intake.triage_requested") || event.EventID <= 0 || event.TenantID <= 0 || event.ProjectID <= 0 || event.Subject.Kind != "intake" || event.Subject.ID <= 0 || event.SubjectVersion <= 0 {
		return 0, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, false, ErrInvalidArgument
	}
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(event.TenantID), q.ProjectID.Eq(event.ProjectID), q.ID.Eq(event.Subject.ID)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	eligible := row.Status == "RECEIVED" && (row.Kind == "NEW" || row.Kind == "REWORK")
	versionMatches := int64(row.Version) == event.SubjectVersion
	if event.EventType == "intake.triage_requested" {
		eligible = (row.Status == "RECEIVED" || row.Status == "TRIAGED" || row.Status == "CLARIFYING") && row.LastTriageRequestedAt != nil
		// A queued request is a reference to the current source. Later eligible
		// edits must not erase it before consumption; future versions still have
		// no persisted request to consume. Inbox deduplication uses the event ID.
		versionMatches = int64(row.Version) >= event.SubjectVersion
	}
	if !eligible || !versionMatches {
		return 0, false, nil
	}
	if m.triageAgent == nil || m.triageRequestCoordinator == nil {
		return 0, false, ErrUnavailable
	}
	// System consumption locks the same configured participants/channel before
	// the source as the request path; it carries no caller/project write grant.
	err = m.triageRequestCoordinator(ctx, tx, event.TenantID, event.ProjectID, row.ChannelID, row.ChannelKind, func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil })
	if errors.Is(err, ErrInvalidArgument) || errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	_, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(event.TenantID), q.ProjectID.Eq(event.ProjectID), q.ID.Eq(row.ID), q.ChannelID.Eq(row.ChannelID), q.ChannelKind.Eq(row.ChannelKind), q.Version.Eq(row.Version), q.Status.Eq(row.Status)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	agent, found, err := m.triageAgent(ctx, tx, event.TenantID, event.ProjectID, row.ChannelID, row.ChannelKind)
	if err != nil || !found {
		return 0, false, err
	}
	if agent <= 0 {
		return 0, false, ErrUnavailable
	}
	return agent, true, nil
}
