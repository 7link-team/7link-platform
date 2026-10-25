package clientwork

import (
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestHTTPSourceErrorsRetainDomainStatusWithoutInternalLeakage(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{&collab.Interrupted{}, 409},
		{collab.ErrSeenSeqRequired, 422},
		{&execution.ClientAnchorError{Field: "start_line"}, 422},
		{&execution.ClientAnchorError{Field: "version_id", Unavailable: true}, 422},
		{&execution.ClientThreadError{Reason: "NOT_THIS_ITEM"}, 422},
		{ErrBundleTooLarge, 422}, {ErrBundleScan, 422}, {ErrOpenException, 409}, {authn.ErrInvalidCredentials, 403},
	} {
		var status huma.StatusError
		require.ErrorAs(t, httpError(t.Context(), tc.err), &status)
		require.Equal(t, tc.status, status.GetStatus())
	}
}

func TestSheetPaginationBindsCursorAndPropagatesStoredVersionFailures(t *testing.T) {
	m, db, projects, _ := harness(t)
	ctx := testContext(t, 2)
	for _, key := range []string{"first", "second"} {
		out, err := m.createSheetHTTP(ctx, &CreateInput[SheetDraft]{EntityID: "3", Key: key, Body: SheetDraft{Title: key, SignerID: 4, RequirementIDs: []guid.ID{10}}})
		require.NoError(t, err)
		require.Positive(t, out.Body.Data.ID)
		require.Equal(t, 1, out.Body.Data.Version)
	}
	first, err := m.listSheetsHTTP(ctx, &ListInput{EntityID: "3", Limit: 1})
	require.NoError(t, err)
	require.True(t, first.Body.Data.HasMore)
	second, err := m.listSheetsHTTP(ctx, &ListInput{EntityID: "3", Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, err)
	require.False(t, second.Body.Data.HasMore)
	require.NotEqual(t, first.Body.Data.Items[0].ID, second.Body.Data.Items[0].ID)
	projects.deny = true
	_, err = m.listSheetsHTTP(ctx, &ListInput{EntityID: "3", Limit: 1})
	require.Error(t, err)
	projects.deny = false
	_, err = m.listSheetsHTTP(ctx, &ListInput{EntityID: "3", Limit: 1, Cursor: cursor(cursorScope(ctx, 3, "sheets:false"), 0)})
	require.Error(t, err)
	for _, table := range []string{m.names.Sheets.String(), m.names.Revisions.String()} {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("sheet_page_failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(ErrUnavailable)
			}
		}))
		out, e := m.listSheetsHTTP(ctx, &ListInput{EntityID: "3", Limit: 1})
		require.Error(t, e)
		require.Nil(t, out)
		require.NoError(t, db.Callback().Query().Remove("sheet_page_failure"))
	}
}

func TestCommandPreflightNeverInvokesEffectsWithoutCurrentAuthority(t *testing.T) {
	for _, stage := range []string{"draft_authorizer", "auto_provider", "auto_roster", "auto_context", "stop_id", "stop_read", "stop_provider", "triage_provider", "triage_id", "delegated_provider", "delegated_id", "invalid_key"} {
		t.Run(stage, func(t *testing.T) {
			m, db, _ := dispatchFixture(t)
			ctx := testContext(t, 2)
			var request any = &ClarificationDraftInput{}
			key := "preflight-authority"
			want := 503
			switch stage {
			case "draft_authorizer":
				m.questionDrafts.AuthorizeOn = nil
			case "auto_provider", "auto_roster", "auto_context":
				request = &DispatchAutoInput{}
				m.dispatch.AutoLockOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []guid.ID) error { return nil }
				m.dispatchFleet.PrincipalsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return nil, nil }
				if stage == "auto_provider" {
					m.dispatch.AutoLockOn = nil
				}
				if stage == "auto_roster" {
					m.dispatchFleet.PrincipalsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) {
						return nil, errors.New("private roster failure")
					}
					want = 500
				}
				if stage == "auto_context" {
					want = 403
				}
			case "stop_id":
				request = &DispatchStopInput{ID: "bad"}
				want = 422
			case "stop_read":
				request = &DispatchStopInput{ID: "999"}
				want = 404
			case "stop_provider":
				created, err := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "stop-provider", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
				require.NoError(t, err)
				request = &DispatchStopInput{ID: created.Body.Data.ID.String()}
				m.dispatch.LockStopOn = nil
			case "triage_provider", "triage_id":
				request = &TriageRequestInput{ID: "bad"}
				if stage == "triage_provider" {
					m.triageTarget.AuthorizeRequestOn = nil
				} else {
					m.triageTarget.AuthorizeRequestOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
					want = 422
				}
			case "delegated_provider", "delegated_id":
				request = &DelegatedNewInput{EditInput: EditInput[IntakeNewConversion]{ID: "bad"}}
				if stage == "delegated_provider" {
					m.delegatedConversion.AuthorizeOn = nil
				} else {
					m.delegatedConversion.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
					want = 422
				}
			case "invalid_key":
				key = ""
				want = 422
			}
			before := collaborationSnapshot(t, m, db)
			called := false
			out, err := m.command(ctx, 3, "clientwork.preflight", "POST", "/preflight", key, 201, request, "scope_change", false, nil, func(*gorm.DB) (Receipt, error) { called = true; return Receipt{ID: 50}, nil })
			require.Nil(t, out)
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, want, status.GetStatus())
			require.False(t, called)
			require.Equal(t, before, collaborationSnapshot(t, m, db))
		})
	}
}

func TestCommandReauthenticationFailureKeepsPurposeAndTarget(t *testing.T) {
	m, db, _, _ := harness(t)
	for _, request := range []any{&IntakeProjectConversionInput{EditInput: EditInput[IntakeProjectConversion]{ID: "50"}}, &ReworkDispositionInput{EditInput: EditInput[ReworkDispositionBody]{Body: ReworkDispositionBody{RequirementID: 10, Append: &delivery.ClientScopeChange{}}}}} {
		_, err := m.command(testContext(t, 2), 3, "clientwork.reauth", "POST", "/reauth", "reauth-purpose", 201, request, "scope_change", false, func(*gorm.DB) error { return authn.ErrInvalidCredentials }, func(*gorm.DB) (Receipt, error) { t.Fatal("reauth must precede effects"); return Receipt{}, nil })
		var status huma.StatusError
		require.ErrorAs(t, err, &status)
		require.Equal(t, 403, status.GetStatus())
	}
	var count int64
	require.NoError(t, db.Table(m.names.CommandDecision.String()).Count(&count).Error)
	require.Zero(t, count)
}

func TestIntakeConversionCommandReplaysAcrossRenewedCredentialsButBindsBusinessInput(t *testing.T) {
	m, _, _, _ := harness(t)
	ctx := testContext(t, 2)
	in := &IntakeConversionInput{EditInput: EditInput[IntakeConversionBody]{EntityID: "3", ID: "50", IfMatch: "\"1\"", Key: "intake-renewed", Body: IntakeConversionBody{CardRevision: 1, ProposalHash: strings.Repeat("a", 64), ReauthProof: "initial-proof"}}, Cookie: "initial-session"}
	calls := 0
	invoke := func(ctx context.Context, request *IntakeConversionInput) (*CommandOutput, error) {
		return m.command(ctx, 3, "clientwork.intake.convert", "POST", entityBase+"/intake-changes/50/conversion", request.Key, 201, request, "scope_change", false, nil, func(*gorm.DB) (Receipt, error) {
			calls++
			require.Equal(t, "initial-session", request.Cookie)
			require.Equal(t, "initial-proof", request.Body.ReauthProof)
			return Receipt{ID: 50, State: "CONVERTED", Version: 2}, nil
		})
	}
	first, e := invoke(ctx, in)
	require.NoError(t, e)
	// Hash normalization must not erase the credentials used by the first effect.
	require.Equal(t, "initial-session", in.Cookie)
	require.Equal(t, "initial-proof", in.Body.ReauthProof)
	renewed := *in
	renewed.Cookie, renewed.Body.ReauthProof = "renewed-session", "renewed-proof"
	replayed, e := invoke(ctx, &renewed)
	require.NoError(t, e)
	require.Equal(t, "true", replayed.Replayed)
	require.Equal(t, first.Body.Data, replayed.Body.Data)
	for _, mutate := range []func(*IntakeConversionInput){
		func(v *IntakeConversionInput) { v.Body.CardRevision++ },
		func(v *IntakeConversionInput) { v.Body.ProposalHash = strings.Repeat("b", 64) },
		func(v *IntakeConversionInput) { v.IfMatch = "\"2\"" },
	} {
		changed := renewed
		mutate(&changed)
		_, e = invoke(ctx, &changed)
		require.Error(t, e)
	}
	require.Equal(t, 1, calls)
}

func TestHTTPExactSheetCommandsReplayAndCurrentRevocation(t *testing.T) {
	m, _, _, clients := harness(t)
	ctx := testContext(t, 2)
	create := &CreateInput[SheetDraft]{EntityID: "3", Key: "sheet-create", Body: SheetDraft{Title: "Scope", SignerID: 4, RequirementIDs: []guid.ID{10}}}
	draft, e := m.createSheetHTTP(ctx, create)
	require.NoError(t, e)
	id := draft.Body.Data.ID.String()
	again, e := m.createSheetHTTP(ctx, create)
	require.NoError(t, e)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, id, again.Body.Data.ID.String())
	create.Body.Title = "different"
	_, e = m.createSheetHTTP(ctx, create)
	require.Error(t, e)
	published, e := m.publishSheetHTTP(ctx, &EditInput[ClientworkPublishBody]{EntityID: "3", ID: id, Key: "publish", IfMatch: "\"1\""})
	require.NoError(t, e)
	require.Equal(t, 2, published.Body.Data.Version)
	sheets, e := m.listSheetsHTTP(testContext(t, 4), &ListInput{EntityID: "3", Limit: 1})
	require.NoError(t, e)
	require.Len(t, sheets.Body.Data.Items, 1)
	view := sheets.Body.Data.Items[0]
	input := &EditInput[SignoffBody]{EntityID: "3", ID: id, Key: "sign", IfMatch: "\"2\"", Body: SignoffBody{Revision: 1, ContentHash: view.ContentHash, Decision: "SIGN", Reason: "Reviewed"}}
	signed, e := m.signSheetHTTP(testContext(t, 4), input)
	require.NoError(t, e)
	require.Equal(t, "SIGNED", signed.Body.Data.State)
	replay, e := m.signSheetHTTP(testContext(t, 4), input)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	clients.revoked = true
	_, e = m.signSheetHTTP(testContext(t, 4), input)
	require.Error(t, e)
}
func TestHTTPValidationErrorMappingsAndBoundCursors(t *testing.T) {
	ctx := context.Background()
	for _, e := range []error{ErrInvalid, ErrConflict, ErrNotFound, ErrForbidden, ErrUnavailable, errors.New("private database detail"), idempotency.ErrInvalidKey, idempotency.ErrKeyReused, delivery.ErrRequirementVersion} {
		require.Error(t, httpError(ctx, e))
	}
	for _, raw := range []string{"1", "", "\"0\"", "\"-1\"", "\"x\""} {
		_, e := version(raw)
		require.Error(t, e)
	}
	v, e := version("\"1\"")
	require.NoError(t, e)
	require.Equal(t, 1, v)
	scope := cursorScope(testContext(t, 4), 3, "sheets")
	vID, e := position(cursor(scope, 9), scope)
	require.NoError(t, e)
	require.Equal(t, guid.ID(9), vID)
	_, e = position(cursor(scope, 9), cursorScope(testContext(t, 5), 3, "sheets"))
	require.Error(t, e)
	_, e = position("invalid", scope)
	require.Error(t, e)
	m, _, _, _ := harness(t)
	_, e = m.createSheetHTTP(testContext(t, 2), &CreateInput[SheetDraft]{EntityID: "bad"})
	require.Error(t, e)
	_, e = m.listSheetsHTTP(testContext(t, 2), &ListInput{EntityID: "3", Limit: 0})
	require.Error(t, e)
}

func TestHTTPRevisionHistoryNeverMutatesPublishedContent(t *testing.T) {
	m, _, _, _ := harness(t)
	ctx := testContext(t, 2)
	body := SheetDraft{Title: "First", SignerID: 4, RequirementIDs: []guid.ID{10}}
	draft, e := m.createSheetHTTP(ctx, &CreateInput[SheetDraft]{EntityID: "3", Key: "first", Body: body})
	require.NoError(t, e)
	body.Title = "Second"
	revised, e := m.reviseSheetHTTP(ctx, &EditInput[SheetDraft]{EntityID: "3", ID: draft.Body.Data.ID.String(), Key: "second", IfMatch: "\"1\"", Body: body})
	require.NoError(t, e)
	require.Equal(t, 2, revised.Body.Data.Revision)
	old, e := m.getSheetHTTP(ctx, &ObjectInput{EntityID: "3", ID: draft.Body.Data.ID.String(), Revision: 1})
	require.NoError(t, e)
	require.Equal(t, "First", old.Body.Data.Title)
	require.Error(t, func() error {
		_, e := m.reviseSheetHTTP(ctx, &EditInput[SheetDraft]{EntityID: "3", ID: draft.Body.Data.ID.String(), Key: "stale", IfMatch: "\"1\"", Body: body})
		return e
	}())
	_, e = m.reviseSheetHTTP(ctx, &EditInput[SheetDraft]{EntityID: "bad"})
	require.Error(t, e)
}

func TestSheetHTTPInvalidScopeAndUnavailableNeverReturnStaleData(t *testing.T) {
	m, _, _, _ := harness(t)
	ctx := testContext(t, 2)
	for _, in := range []*ObjectInput{{EntityID: "bad", ID: "1"}, {EntityID: "3", ID: "bad"}, {EntityID: "3", ID: "999"}} {
		_, e := m.getSheetHTTP(ctx, in)
		require.Error(t, e)
	}
	_, e := m.publishSheetHTTP(ctx, &EditInput[ClientworkPublishBody]{EntityID: "bad"})
	require.Error(t, e)
	_, e = m.signSheetHTTP(ctx, &EditInput[SignoffBody]{EntityID: "bad"})
	require.Error(t, e)
	_, e = m.listSheetsHTTP(ctx, &ListInput{EntityID: "bad", Limit: 1})
	require.Error(t, e)
	_, e = m.listSheetsHTTP(ctx, &ListInput{EntityID: "3", Limit: 1, Cursor: "bad!"})
	require.Error(t, e)
	_, e = m.command(ctx, 3, "test", "POST", "/test", "test", 200, make(chan int), "project_read", false, nil, func(*gorm.DB) (Receipt, error) { t.Fatal("invalid request must not execute"); return Receipt{}, nil })
	require.Error(t, e)
	m.runtime = nil
	_, e = m.getSheetHTTP(ctx, &ObjectInput{EntityID: "3", ID: "1"})
	require.Error(t, e)
	_, e = m.listSheetsHTTP(ctx, &ListInput{EntityID: "3", Limit: 1})
	require.Error(t, e)
	_, e = m.createSheetHTTP(ctx, &CreateInput[SheetDraft]{EntityID: "3", Key: "unavailable"})
	require.Error(t, e)
	require.False(t, validStrings([]string{" "}))
	require.False(t, validStrings(make([]string, 51)))
}

func TestCanonicalCommandErrorsKeepBusinessHTTPStatuses(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{delivery.ErrProjectCodeTaken, 409}, {delivery.ErrProjectLimit, 422}, {delivery.ErrParentNotClient, 422}, {delivery.ErrContactNotFound, 422}, {delivery.ErrIntakeNotFound, 422}, {delivery.ErrProjectForbidden, 403}, {delivery.ErrProjectNonDelegable, 403}, {delivery.ErrResultIndependent, 403},
		{delivery.ErrRequirementStage, 422}, {delivery.ErrRequirementLimit, 422},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			var status huma.StatusError
			require.ErrorAs(t, httpError(context.Background(), tc.err), &status)
			require.Equal(t, tc.status, status.GetStatus())
		})
	}
	m, db, _, _ := harness(t)
	m.intakeProjects.Prepare = func(context.Context, *gorm.DB, guid.ID, delivery.IntakeProjectInput) ([]guid.ID, error) {
		return nil, delivery.ErrProjectCodeTaken
	}
	_, err := m.convertIntakeProjectHTTP(testContext(t, 2), &IntakeProjectConversionInput{EditInput: EditInput[IntakeProjectConversion]{EntityID: "3", ID: "50", IfMatch: `"1"`, Key: "duplicate-project-code"}})
	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	require.Equal(t, 409, status.GetStatus())
	var decisions int64
	require.NoError(t, db.Table(m.names.CommandDecision.String()).Count(&decisions).Error)
	require.Zero(t, decisions, "rejected provisioning cannot record a successful business decision")
}
