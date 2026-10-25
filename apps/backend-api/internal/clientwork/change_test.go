package clientwork

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func confirmedProposal(t *testing.T, m *Module, db *gorm.DB) (Receipt, string) {
	t.Helper()
	sheet := draftAndPublish(t, m, db)
	var r Receipt
	run := func(f func(*gorm.DB) error) { require.NoError(t, db.Transaction(f)) }
	run(func(tx *gorm.DB) error {
		_, e := m.signSheetOn(testContext(t, 4), tx, 3, sheet.ID, sheet.Version, SignoffBody{1, sheet.ContentHash, "SIGN", "Reviewed"})
		return e
	})
	run(func(tx *gorm.DB) error {
		var e error
		r, e = m.createChangeOn(testContext(t, 4), tx, 3, ChangeBody{sheet.ID, 1, "Add reporting"})
		return e
	})
	description := "Confirmed reporting scope"
	run(func(tx *gorm.DB) error {
		var e error
		r, e = m.actChangeOn(testContext(t, 2), tx, 3, r.ID, r.Version, ChangeAction{Action: "ASSESS", Impact: "One extra milestone", Proposal: &ClientChangeProposal{RequirementID: 10, RequirementVersion: 1, Change: ClientChangeDraft{BaseVersionNo: 1, Description: &description, Summary: "Reporting", StaleWorkIDs: []guid.ID{11}}}})
		return e
	})
	v, e := m.get_changeHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: r.ID.String()})
	require.NoError(t, e)
	public, e := json.Marshal(v.Body.Data)
	require.NoError(t, e)
	require.Contains(t, string(public), description)
	require.NotContains(t, string(public), "stale_work_ids")
	require.NotContains(t, string(public), "reauth_proof")
	q := query.BindChangeRequest(db, m.names)
	stored, e := q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, e)
	require.NotContains(t, *stored.Proposal, "reauth_proof")
	run(func(tx *gorm.DB) error {
		var e error
		r, e = m.actChangeOn(testContext(t, 4), tx, 3, r.ID, r.Version, ChangeAction{Action: "CONFIRM", ProposalHash: v.Body.Data.ProposalHash})
		return e
	})
	return r, v.Body.Data.ProposalHash
}

func TestChangeApplicationExactConfirmationAndAtomicCanonicalCommand(t *testing.T) {
	m, db, projects, _ := harness(t)
	r, hash := confirmedProposal(t, m, db)
	apply := func(ctx context.Context, version int, digest string) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.applyChangeOn(ctx, tx, 3, r.ID, version, "session-cookie", ChangeApplicationBody{ProposalHash: digest, ReauthProof: "fresh-proof"})
			return e
		})
	}
	require.ErrorIs(t, apply(testContext(t, 4), r.Version, hash), ErrNotFound)
	require.ErrorIs(t, apply(testContext(t, 2), r.Version-1, hash), ErrConflict)
	require.ErrorIs(t, apply(testContext(t, 2), r.Version, strings.Repeat("0", 64)), ErrConflict)
	projects.applyErr = delivery.ErrRequirementVersion
	require.ErrorIs(t, apply(testContext(t, 2), r.Version, hash), delivery.ErrRequirementVersion)
	projects.applyErr = nil
	// Failure of the outbox must roll back the canonical write as well.
	publisher := m.events
	m.events = failPublisher{}
	require.ErrorIs(t, apply(testContext(t, 2), r.Version, hash), ErrUnavailable)
	var count int64
	require.NoError(t, db.Table("baseline_probe").Where("id = ?", 88).Count(&count).Error)
	require.Zero(t, count)
	v, e := m.get_changeHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: r.ID.String()})
	require.NoError(t, e)
	require.Equal(t, "CONFIRMED", v.Body.Data.State)
	require.False(t, v.Body.Data.Applied)
	m.events = publisher
	require.NoError(t, apply(testContext(t, 2), r.Version, hash))
	require.Equal(t, "fresh-proof", projects.lastChange.ReauthProof)
	require.Equal(t, "session-cookie", projects.lastCookie)
	require.Equal(t, []guid.ID{11}, projects.lastChange.StaleWorkIDs)
	v, e = m.get_changeHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: r.ID.String()})
	require.NoError(t, e)
	require.True(t, v.Body.Data.Applied)
	require.Equal(t, "APPLIED", v.Body.Data.State)
	require.ErrorIs(t, apply(testContext(t, 2), r.Version, hash), ErrConflict)
}

func TestChangeProposalMustBelongToFrozenSheetAndCurrentRequirement(t *testing.T) {
	m, db, projects, _ := harness(t)
	r, _ := confirmedProposal(t, m, db)
	row, e := m.changeOn(testContext(t, 2), db, 3, r.ID, false)
	require.NoError(t, e)
	description := "Updated"
	p := ClientChangeProposal{RequirementID: 10, RequirementVersion: 1, Change: ClientChangeDraft{BaseVersionNo: 1, Summary: "Impact", Description: &description}}
	require.NoError(t, m.validateChangeProposalOn(testContext(t, 2), db, 3, row, &p))
	projects.changed = true
	require.ErrorIs(t, m.validateChangeProposalOn(testContext(t, 2), db, 3, row, &p), ErrConflict)
	p.RequirementID = 99
	require.ErrorIs(t, m.validateChangeProposalOn(testContext(t, 2), db, 3, row, &p), ErrNotFound)
	p.RequirementID = 0
	require.ErrorIs(t, m.validateChangeProposalOn(testContext(t, 2), db, 3, row, &p), ErrInvalid)
}

func TestChangeApplicationHTTPReplayRechecksCurrentAuthority(t *testing.T) {
	m, db, _, clients := harness(t)
	r, hash := confirmedProposal(t, m, db)
	in := &ChangeApplicationInput{EditInput: EditInput[ChangeApplicationBody]{EntityID: "3", ID: r.ID.String(), IfMatch: fmt.Sprintf("\"%d\"", r.Version), Key: "apply-confirmed", Body: ChangeApplicationBody{ProposalHash: hash, ReauthProof: "fresh-proof"}}, Cookie: "session"}
	first, e := m.applyChangeHTTP(testContext(t, 2), in)
	require.NoError(t, e)
	require.Equal(t, "APPLIED", first.Body.Data.State)
	replay, e := m.applyChangeHTTP(testContext(t, 2), in)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	clients.revoked = true
	_, e = m.applyChangeHTTP(testContext(t, 4), in)
	require.Error(t, e)
	in.IfMatch = "bad"
	_, e = m.applyChangeHTTP(testContext(t, 2), in)
	require.Error(t, e)
}

func TestChangeAssessmentHTTPSchemaNeedsNoReauthenticationCredential(t *testing.T) {
	m, db, _, _ := harness(t)
	previous, _ := confirmedProposal(t, m, db)
	v, e := m.get_changeHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: previous.ID.String()})
	require.NoError(t, e)
	var r Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		r, e = m.createChangeOn(testContext(t, 4), tx, 3, ChangeBody{v.Body.Data.SheetID, 1, "More reporting"})
		return e
	}))
	_, api := humatest.New(t)
	api.UseMiddleware(func(c huma.Context, next func(huma.Context)) {
		next(huma.WithContext(c, authn.WithClaims(c.Context(), &authn.Claims{PrincipalID: 2, TenantID: 1, EntityID: 3, SubjectType: authn.SubjectTypePrincipal})))
	})
	huma.Register(api, huma.Operation{OperationID: "test.assess", Method: "POST", Path: "/entities/{entity_id}/client-changes/{id}/actions"}, m.changeHTTP)
	response := api.Post("/entities/3/client-changes/"+r.ID.String()+"/actions", "Idempotency-Key: credential-free-proposal", "If-Match: \"1\"", map[string]any{"action": "ASSESS", "impact": "One milestone", "proposal": map[string]any{"requirement_id": "10", "requirement_version": 1, "change": map[string]any{"base_version_no": 1, "summary": "More reporting", "description": "Updated scope", "criteria": map[string]any{}}}})
	require.Equal(t, 200, response.Code, response.Body.String())
}
