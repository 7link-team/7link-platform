package intake

import (
	"context"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func triageDraft() TriageDraft {
	return TriageDraft{Summary: "Requested change", RequirementDraft: "Allow export", Criteria: []string{"Export contains current rows"}, Risks: []string{}, Questions: []string{"Which format?"}, Size: "S"}
}

func TestTriagePreservesSourceAndImmutableRevisions(t *testing.T) {
	m, db, ctx, allowed, _ := changeSourceHarness(t)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": "NEW", "status": "RECEIVED", "version": 1, "raw_content": "Original bytes"}).Error)
	var first TriageSource
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = m.triageSourceOn(ctx, tx, 7, 20, 10, 1, triageDraft())
		return err
	}))
	require.Equal(t, 1, first.Revision)
	require.Equal(t, 2, first.Version)
	require.Positive(t, int64(first.ID))
	in := triageDraft()
	in.Summary = "Revised assessment"
	handlerCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: 7, PrincipalID: 6, SubjectType: authn.SubjectTypePrincipal})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		next, err := m.triageSourceOn(handlerCtx, tx, 7, 20, 10, 2, in)
		require.Equal(t, 2, next.Revision)
		require.NotEqual(t, first.Hash, next.Hash)
		return err
	}))
	// A later client update is not the internal actor who assessed the source.
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Update("updated_by", guid.ID(4)).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		source, err := m.readIntakeLedgerSourceOn(ctx, tx, 7, 20, 10)
		require.NoError(t, err)
		require.Equal(t, "Original bytes", *source.RawContent)
		require.Nil(t, source.Card)
		require.Equal(t, in, source.Triage.Draft)
		require.NotNil(t, source.LastHandlerID)
		require.Equal(t, guid.ID(6), *source.LastHandlerID)
		require.Equal(t, source.Triage.ActorID, *source.LastHandlerID)
		require.NotEqual(t, first.ActorID, *source.LastHandlerID)
		_, err = m.triageSourceOn(ctx, tx, 7, 20, 10, 1, in)
		require.ErrorIs(t, err, ErrConflict)
		_, err = m.triageSourceOn(ctx, tx, 7, 21, 10, 3, in)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		return nil
	}))
	var cards []struct {
		ProposalJSON string
		ChangeID     *guid.ID
	}
	require.NoError(t, db.Table(m.names.TriageCards.String()).Order("revision").Find(&cards).Error)
	require.Len(t, cards, 2)
	require.Contains(t, cards[0].ProposalJSON, "Requested change")
	require.Nil(t, cards[0].ChangeID)
	*allowed = false
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.triageSourceOn(ctx, tx, 7, 20, 10, 3, in); return err }), ErrForbidden)
}

func TestTriageRollbackValidationAndTerminalState(t *testing.T) {
	for _, kind := range []string{"NEW", "REWORK"} {
		t.Run(kind, func(t *testing.T) {
			m, db, ctx, _, pub := changeSourceHarness(t)
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": kind, "status": "RECEIVED", "version": 1}).Error)
			pub.fail = true
			err := db.Transaction(func(tx *gorm.DB) error { _, err := m.triageSourceOn(ctx, tx, 7, 20, 10, 1, triageDraft()); return err })
			require.Error(t, err)
			var n int64
			require.NoError(t, db.Table(m.names.TriageCards.String()).Count(&n).Error)
			require.Zero(t, n)
			require.NoError(t, db.Table("brief_probe").Count(&n).Error)
			require.Zero(t, n)
			pub.fail = false
			m.brief.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.triageSourceOn(ctx, tx, 7, 20, 10, 1, triageDraft()); return err }))
			require.NoError(t, db.Table(m.names.TriageCards.String()).Count(&n).Error)
			require.Zero(t, n)
		})
	}
	for _, mutate := range []func(*TriageDraft){func(v *TriageDraft) { v.Summary = " " }, func(v *TriageDraft) { v.Size = "FAKE" }, func(v *TriageDraft) { v.Criteria = nil }, func(v *TriageDraft) { v.Questions = []string{""} }, func(v *TriageDraft) { v.RequirementDraft = strings.Repeat("x", 8001) }} {
		v := triageDraft()
		mutate(&v)
		_, err := (&Module{}).triageSourceOn(context.Background(), nil, 7, 20, 10, 1, v)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	m, db, ctx, _, _ := changeSourceHarness(t)
	for _, status := range []string{"CONVERTED", "DECLINED", "MERGED", "CLIENT_APPROVAL"} {
		require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": "NEW", "status": status, "version": 1, "decline_reason_code": "OTHER", "decline_reason": "No longer needed", "merged_into_intake_id": 11}).Error)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.triageSourceOn(ctx, tx, 7, 20, 10, 1, triageDraft()); return err }), ErrConflict)
	}
}

func TestTriageCorruptStoredAssessmentNeverReturnsAPartialSource(t *testing.T) {
	for _, corrupted := range []string{"missing", "json", "hash"} {
		t.Run(corrupted, func(t *testing.T) {
			m, db, ctx, _, _ := changeSourceHarness(t)
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": "NEW", "status": "RECEIVED", "version": 1}).Error)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.triageSourceOn(ctx, tx, 7, 20, 10, 1, triageDraft()); return e }))
			switch corrupted {
			case "missing":
				require.NoError(t, db.Exec("DELETE FROM "+m.names.TriageCards.String()).Error)
			case "json":
				require.NoError(t, db.Table(m.names.TriageCards.String()).Where("intake_id=10").Update("proposal_json", "{").Error)
			case "hash":
				require.NoError(t, db.Table(m.names.TriageCards.String()).Where("intake_id=10").Update("proposal_hash", strings.Repeat("f", 64)).Error)
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				out, e := m.readIntakeLedgerSourceOn(ctx, tx, 7, 20, 10)
				require.Error(t, e)
				require.Zero(t, out.ID)
				require.Nil(t, out.RawContent)
				return nil
			}))
		})
	}
}
