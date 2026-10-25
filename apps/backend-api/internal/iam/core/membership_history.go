package core

import (
	"context"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// MembershipInterval records observed membership, without asserting facts
// before observation started. End is exclusive and nil means still active.
type MembershipInterval struct {
	PrincipalID guid.ID
	Start       time.Time
	End         *time.Time
}

// MembershipObservedSinceOn returns a conservative tenant observation cutoff.
// No period beginning before this cutoff can claim complete eligibility.
func (m *MembershipChecker) MembershipObservedSinceOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) (*time.Time, error) {
	if m == nil || tx == nil {
		return nil, ErrUnavailable
	}
	if tenant <= 0 {
		return nil, ErrInvalidArgument
	}
	q := query.BindWithNames(tx, m.names).MembershipPeriods
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant)).Order(q.StartedAt).Limit(1).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0].StartedAt, nil
}

func (r *Repository) recordMembershipTransition(ctx context.Context, previous string, member TenantMember, now time.Time) error {
	if previous == string(member.Status) {
		return nil
	}
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	q := tables.MembershipPeriods
	if previous == string(MemberActive) {
		if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(member.TenantID), q.PrincipalID.Eq(member.PrincipalID), q.EndedAt.IsNull()).Update(q.EndedAt, now); err != nil {
			return err
		}
	}
	if member.Status != MemberActive {
		return nil
	}
	id, err := r.nextID()
	if err != nil {
		return err
	}
	return q.WithContext(ctx).Create(&model.MembershipPeriod{TenantID: member.TenantID, ID: id, PrincipalID: member.PrincipalID, StartedAt: now})
}

// MembershipIntervalsOn is an internal fact reader, not an authorization
// capability. Consumers must authorize access or use a registered system job.
// It reports only observed intervals; absence never proves pre-migration
// ineligibility. Reads share the caller's transaction.
func (m *MembershipChecker) MembershipIntervalsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, start, end time.Time) ([]MembershipInterval, error) {
	if m == nil || tx == nil {
		return nil, ErrUnavailable
	}
	if tenant <= 0 || !start.Before(end) {
		return nil, ErrInvalidArgument
	}
	q := query.BindWithNames(tx, m.names).MembershipPeriods
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.StartedAt.Lt(end)).Where(q.WithContext(ctx).Where(q.EndedAt.IsNull()).Or(q.EndedAt.Gt(start))).Order(q.PrincipalID, q.StartedAt).Find()
	if err != nil {
		return nil, err
	}
	out := make([]MembershipInterval, len(rows))
	for i, row := range rows {
		out[i] = MembershipInterval{PrincipalID: row.PrincipalID, Start: row.StartedAt, End: row.EndedAt}
	}
	return out, nil
}
