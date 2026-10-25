package clientwork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIntakeSuggestionCreationRejectsAuthorityAndStorageLossWithoutEvidence(t *testing.T) {
	for _, stage := range []string{"missing_source", "unknown_actor", "authority_changed", "source", "count", "create", "ids", "oversize"} {
		t.Run(stage, func(t *testing.T) {
			m, db, _, _ := clarificationHarness(t)
			failure := errors.New("suggestion dependency unavailable")
			calls := 0
			m.questionDrafts.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (string, error) {
				calls++
				if stage == "unknown_actor" {
					return "SERVICE", nil
				}
				if stage == "authority_changed" && calls > 1 {
					return "", failure
				}
				return "HUMAN", nil
			}
			m.questionDrafts.SourceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) error {
				if stage == "source" {
					return failure
				}
				return nil
			}
			in := &IntakeSuggestionInput{EntityID: "3", Key: "suggestion-rejected", Body: IntakeSuggestionBody{IntakeID: 50, IntakeVersion: 1, Assessment: intake.TriageDraft{Summary: "Export", RequirementDraft: "Export rows", Criteria: []string{"CSV"}, Size: "S"}, Recommendation: "NEW_REQUIREMENT", Reason: "Client request"}}
			before := collaborationSnapshot(t, m, db)
			switch stage {
			case "missing_source":
				m.questionDrafts.SourceOn = nil
			case "count":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_suggestion_write", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.IntakeSuggestion.String() {
						tx.AddError(failure)
					}
				}))
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("regression_suggestion_write", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.IntakeSuggestion.String() {
						tx.AddError(failure)
					}
				}))
			case "ids":
				m.ids = zeroIDs{}
			case "oversize":
				in.Body.Assessment.Criteria = make([]string, 100)
				for i := range in.Body.Assessment.Criteria {
					in.Body.Assessment.Criteria[i] = strings.Repeat("x", 2000)
				}
			}
			out, err := m.createIntakeSuggestionHTTP(testContext(t, 2), in)
			require.Error(t, err)
			require.Nil(t, out)
			require.NoError(t, db.Callback().Query().Remove("regression_suggestion_write"))
			require.NoError(t, db.Callback().Create().Remove("regression_suggestion_write"))
			require.Equal(t, before, collaborationSnapshot(t, m, db), "failed suggestion must create neither record nor audit/outbox evidence")
		})
	}
}

func TestIntakeSuggestionReadsRejectInvalidRoutesMissingAuthorityAndStorageFailures(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	ctx := testContext(t, 2)
	for _, read := range []func(*ObjectInput) error{
		func(in *ObjectInput) error { _, err := m.getIntakeSuggestionSourceHTTP(ctx, in); return err },
		func(in *ObjectInput) error { _, err := m.getIntakeSuggestionHTTP(ctx, in); return err },
		func(in *ObjectInput) error { _, err := m.listIntakeSuggestionsHTTP(ctx, in); return err },
	} {
		for _, in := range []*ObjectInput{{EntityID: "bad", ID: "50"}, {EntityID: "3", ID: "bad"}} {
			require.Error(t, read(in))
		}
		require.Error(t, read(&ObjectInput{EntityID: "3", ID: "50"}), "missing current draft authority must fail closed")
		runtime := m.runtime
		m.runtime = nil
		require.Error(t, read(&ObjectInput{EntityID: "3", ID: "50"}))
		m.runtime = runtime
	}
	m.questionDrafts.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (string, error) { return "HUMAN", nil }
	m.questionDrafts.SourceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) error { return nil }
	failure := errors.New("suggestion source unavailable")
	m.intakeSources.SuggestionSourceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.SuggestionSource, error) {
		return intake.SuggestionSource{}, failure
	}
	_, err := m.getIntakeSuggestionSourceHTTP(ctx, &ObjectInput{EntityID: "3", ID: "50"})
	require.Error(t, err)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_suggestion_read", func(tx *gorm.DB) { tx.AddError(failure) }))
	_, err = m.getIntakeSuggestionHTTP(ctx, &ObjectInput{EntityID: "3", ID: "50"})
	require.Error(t, err)
	_, err = m.listIntakeSuggestionsHTTP(ctx, &ObjectInput{EntityID: "3", ID: "50"})
	require.Error(t, err)
	require.NoError(t, db.Callback().Query().Remove("regression_suggestion_read"))
	// Provider failure after authorization never yields a partial source page.
	m.intakeSources.SuggestionSourcesOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (intake.SuggestionSourcePage, error) {
		return intake.SuggestionSourcePage{}, failure
	}
	_, err = m.listIntakeSuggestionSourcesHTTP(ctx, &ListInput{EntityID: "3", Limit: 1})
	require.Error(t, err)
}

func TestIntakeSuggestionProjectCandidateUsesCurrentAuthorityBeforeReplay(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	allowed := true
	var checked []guid.ID
	m.questionDrafts.AuthorizeOn = func(_ context.Context, tx *gorm.DB, tenant, project guid.ID) (string, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Implements(t, (*gorm.TxCommitter)(nil), tx.Statement.ConnPool)
		checked = append(checked, project)
		if project == 3 || (project == 8 && allowed) {
			return "AGENT", nil
		}
		return "", ErrForbidden
	}
	m.questionDrafts.SourceOn = func(_ context.Context, _ *gorm.DB, tenant, project, source guid.ID, version int) error {
		if tenant != 1 || project != 3 || source != 50 || version != 1 {
			return ErrConflict
		}
		return nil
	}
	candidate := guid.ID(8)
	in := &IntakeSuggestionInput{EntityID: "3", Key: "candidate-suggestion", Body: IntakeSuggestionBody{SuggestedProjectID: &candidate, IntakeID: 50, IntakeVersion: 1, Assessment: intake.TriageDraft{Summary: "CSV request", RequirementDraft: "Export selected rows", Criteria: []string{"Exactly selected rows"}, Size: "S"}, Recommendation: "NEW_REQUIREMENT", Reason: "Candidate project has the relevant export workflow"}}
	created, err := m.createIntakeSuggestionHTTP(testContext(t, 25), in)
	require.NoError(t, err)
	require.Contains(t, checked, candidate)
	view, err := m.getIntakeSuggestionHTTP(testContext(t, 25), &ObjectInput{EntityID: "3", ID: created.Body.Data.ID.String()})
	require.NoError(t, err)
	require.Equal(t, &candidate, view.Body.Data.SuggestedProjectID)
	allowed = false
	_, err = m.createIntakeSuggestionHTTP(testContext(t, 25), in)
	require.Error(t, err, "revoked candidate authority must be checked before replay")
	for _, target := range []guid.ID{0, -1, 9} {
		in.Body.SuggestedProjectID = &target
		in.Key = fmt.Sprintf("candidate-invalid-%d", target)
		_, err = m.createIntakeSuggestionHTTP(testContext(t, 25), in)
		require.Error(t, err)
	}
	var count int64
	require.NoError(t, db.Table(m.names.IntakeSuggestion.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
	var source struct {
		ProjectID guid.ID
		Status    string
		Version   int
	}
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Take(&source).Error)
	require.Equal(t, guid.ID(3), source.ProjectID)
	require.Equal(t, "RECEIVED", source.Status)
	require.Equal(t, 1, source.Version)
}

func TestIntakeSuggestionAgentProvenanceReplayAndNoCanonicalEffects(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	allowed := true
	m.questionDrafts.AuthorizeOn = func(ctx context.Context, _ *gorm.DB, tenant, project guid.ID) (string, error) {
		if !allowed || tenant != 1 || project != 3 {
			return "", ErrForbidden
		}
		switch authn.PrincipalIDFromContext(ctx) {
		case 25:
			return "AGENT", nil
		case 2:
			return "HUMAN", nil
		default:
			return "", ErrForbidden
		}
	}
	m.questionDrafts.SourceOn = func(_ context.Context, tx *gorm.DB, tenant, project, source guid.ID, version int) error {
		var n int64
		err := tx.Table("g_intake_records").Where("tenant_id=? AND project_id=? AND id=? AND version=? AND status IN ('RECEIVED','TRIAGED')", tenant, project, source, version).Count(&n).Error
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	}
	in := &IntakeSuggestionInput{EntityID: "3", Key: "agent-triage-suggestion", Body: IntakeSuggestionBody{IntakeID: 50, IntakeVersion: 1, Assessment: intake.TriageDraft{Summary: "Export request", RequirementDraft: "Export project rows", Criteria: []string{"Export contains authorized project rows"}, Questions: []string{"Which export format?"}, Size: "UNKNOWN"}, Recommendation: "NEEDS_CLARIFICATION", Reason: "Format is not specified in the source"}}
	var before struct {
		Status        string
		Version       int
		RequirementID *guid.ID
	}
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Take(&before).Error)
	source, err := m.getIntakeSuggestionSourceHTTP(testContext(t, 25), &ObjectInput{EntityID: "3", ID: "50"})
	require.NoError(t, err)
	require.Equal(t, 1, source.Body.Data.Version)
	require.Equal(t, guid.ID(50), source.Body.Data.ID)
	_, err = m.getIntakeSuggestionSourceHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: "50"})
	require.Error(t, err)
	original := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	_, err = m.createIntakeSuggestionHTTP(testContext(t, 25), in)
	require.Error(t, err)
	var n int64
	require.NoError(t, db.Table(m.names.IntakeSuggestion.String()).Count(&n).Error)
	require.Zero(t, n)
	m.audit = original
	first, err := m.createIntakeSuggestionHTTP(testContext(t, 25), in)
	require.NoError(t, err)
	require.Equal(t, "SUGGESTED", first.Body.Data.State)
	replay, err := m.createIntakeSuggestionHTTP(testContext(t, 25), in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	view, err := m.getIntakeSuggestionHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: first.Body.Data.ID.String()})
	require.NoError(t, err)
	require.Equal(t, "AGENT", view.Body.Data.ActorKind)
	require.Equal(t, guid.ID(25), view.Body.Data.ActorID)
	require.Equal(t, in.Body, view.Body.Data.IntakeSuggestionBody)
	list, err := m.listIntakeSuggestionsHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: "50"})
	require.NoError(t, err)
	require.Len(t, list.Body.Data, 1)
	require.Equal(t, view.Body.Data, list.Body.Data[0])
	other, err := m.listIntakeSuggestionsHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: "51"})
	require.NoError(t, err)
	require.Empty(t, other.Body.Data)
	var after struct {
		Status        string
		Version       int
		RequirementID *guid.ID
	}
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Take(&after).Error)
	require.Equal(t, before, after)
	require.NoError(t, db.Table(m.names.ClarificationRound.String()).Count(&n).Error)
	require.Zero(t, n, "suggestion never sends external questions")
	_, err = m.getIntakeSuggestionHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: first.Body.Data.ID.String()})
	require.Error(t, err)
	in.Key = "stale-suggestion"
	in.Body.IntakeVersion = 2
	_, err = m.createIntakeSuggestionHTTP(testContext(t, 25), in)
	require.Error(t, err)
	in.Key = "invalid-suggestion"
	in.Body.IntakeVersion = 1
	in.Body.Recommendation = "EXECUTED"
	_, err = m.createIntakeSuggestionHTTP(testContext(t, 25), in)
	require.Error(t, err)
	// MySQL TEXT accepts 65535 bytes, including the serialized proposal's
	// field names and punctuation. SQLite must enforce that same API bound.
	originalBody := in.Body
	originalBody.Recommendation = "NEEDS_CLARIFICATION"
	for _, size := range []int{65535, 65536} {
		large := originalBody
		large.Assessment.Criteria = make([]string, 30)
		for i := range large.Assessment.Criteria {
			large.Assessment.Criteria[i] = strings.Repeat("c", 2000)
		}
		large.Assessment.RequirementDraft = "d"
		raw, err := json.Marshal(large)
		require.NoError(t, err)
		large.Assessment.RequirementDraft = strings.Repeat("d", size-len(raw)+1)
		raw, err = json.Marshal(large)
		require.NoError(t, err)
		require.Len(t, raw, size)
		_, err = m.createIntakeSuggestionHTTP(testContext(t, 25), &IntakeSuggestionInput{EntityID: "3", Key: strings.Repeat("b", size-65534), Body: large})
		if size == 65535 {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
	in.Key = "agent-triage-suggestion"
	in.Body.Recommendation = "NEEDS_CLARIFICATION"
	allowed = false
	_, err = m.createIntakeSuggestionHTTP(testContext(t, 25), in)
	require.Error(t, err, "current authorization precedes replay")
	_, err = m.getIntakeSuggestionHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: first.Body.Data.ID.String()})
	require.Error(t, err)
}

func TestIntakeSuggestionValidation(t *testing.T) {
	in := IntakeSuggestionBody{IntakeID: 50, IntakeVersion: 1, Recommendation: "NEW_REQUIREMENT", Reason: "Explicit recommendation", Assessment: intake.TriageDraft{Summary: "Summary", RequirementDraft: "Draft", Criteria: []string{"Check"}, Size: "S"}}
	require.True(t, validIntakeSuggestion(in))
	for _, mutate := range []func(*IntakeSuggestionBody){func(x *IntakeSuggestionBody) { x.IntakeID = 0 }, func(x *IntakeSuggestionBody) { x.Reason = " " }, func(x *IntakeSuggestionBody) { x.Assessment.Size = "GUESS" }, func(x *IntakeSuggestionBody) { x.Assessment.Criteria = nil }, func(x *IntakeSuggestionBody) { x.Assessment.Questions = []string{" "} }} {
		bad := in
		mutate(&bad)
		require.False(t, validIntakeSuggestion(bad))
	}
}

func TestIntakeSuggestionSourceDiscoveryUsesRealEligibilityAndCurrentAgentAuthority(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	allowed := true
	m.questionDrafts.AuthorizeOn = func(ctx context.Context, _ *gorm.DB, tenant, project guid.ID) (string, error) {
		if !allowed || tenant != 1 || project != 3 {
			return "", ErrForbidden
		}
		switch authn.PrincipalIDFromContext(ctx) {
		case 25:
			return "AGENT", nil
		case 2:
			return "HUMAN", nil
		default:
			return "", ErrForbidden
		}
	}
	for i, seed := range []struct {
		kind, state string
		project     int
	}{
		{"REWORK", "TRIAGED", 3}, {"NEW", "CONVERTED", 3}, {"CHANGE", "RECEIVED", 3}, {"NEW", "RECEIVED", 9}, {"NEW", "CLARIFYING", 3},
	} {
		at := time.Now().UTC()
		row := map[string]any{"id": 51 + i, "tenant_id": 1, "ref_no": 2 + i, "channel_id": 3, "channel_kind": "SHARED_CHANNEL", "contact_id": 1, "title": "Source", "raw_content": "private source body", "raw_content_hash": strings.Repeat("b", 64), "received_at": at, "reply_token": fmt.Sprintf("source-%d", 51+i), "kind": seed.kind, "status": seed.state, "project_id": seed.project, "requirement_id": 10, "created_at": at, "updated_at": at}
		if seed.kind == "CHANGE" {
			row["change_of_intake_id"], row["target_requirement_id"] = 50, 10
		}
		require.NoError(t, db.Table("g_intake_records").Create(row).Error)
	}
	ctx := testContext(t, 25)
	first, err := m.listIntakeSuggestionSourcesHTTP(ctx, &ListInput{EntityID: "3", Limit: 1})
	require.NoError(t, err)
	require.True(t, first.Body.Data.HasMore)
	require.Equal(t, guid.ID(51), first.Body.Data.Items[0].ID)
	require.Equal(t, "REWORK", first.Body.Data.Items[0].Kind)
	require.Nil(t, first.Body.Data.Items[0].Content)
	require.NotEmpty(t, first.Body.Data.NextCursor)
	last, err := m.listIntakeSuggestionSourcesHTTP(ctx, &ListInput{EntityID: "3", Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, err)
	require.False(t, last.Body.Data.HasMore)
	require.Equal(t, guid.ID(50), last.Body.Data.Items[0].ID)
	raw, err := json.Marshal(first.Body.Data)
	require.NoError(t, err)
	for _, private := range []string{"private source body", "contact_id", "reply_token", "approval", "execution_id"} {
		require.NotContains(t, string(raw), private)
	}
	for _, in := range []*ListInput{
		{EntityID: "3", Limit: 0}, {EntityID: "3", Limit: 101}, {EntityID: "bad", Limit: 1},
		{EntityID: "3", Limit: 1, Cursor: "bad"},
		{EntityID: "3", Limit: 1, Cursor: cursor(cursorScope(testContext(t, 2), 3, "intake-suggestion-sources"), 51)},
		{EntityID: "3", Limit: 1, Cursor: cursor(cursorScope(ctx, 3, "intakes"), 51)},
		{EntityID: "9", Limit: 1, Cursor: first.Body.Data.NextCursor},
	} {
		_, err := m.listIntakeSuggestionSourcesHTTP(ctx, in)
		require.Error(t, err)
	}
	_, err = m.listIntakeSuggestionSourcesHTTP(testContext(t, 4), &ListInput{EntityID: "3", Limit: 1})
	require.Error(t, err, "external client cannot discover internal triage")
	allowed = false
	_, err = m.listIntakeSuggestionSourcesHTTP(ctx, &ListInput{EntityID: "3", Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.Error(t, err, "continuation never retains revoked authority")
	allowed = true
	m.intakeSources.SuggestionSourcesOn = nil
	_, err = m.listIntakeSuggestionSourcesHTTP(ctx, &ListInput{EntityID: "3", Limit: 1})
	require.Error(t, err)
}

func TestIntakeSuggestionInboxLookupChecksCurrentProjectBeforeReturningSource(t *testing.T) {
	m, _, _, _ := clarificationHarness(t)
	allowed := true
	m.questionDrafts.AuthorizeOn = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID) (string, error) {
		if !allowed || tenant != 1 || project != 3 {
			return "", ErrForbidden
		}
		return "AGENT", nil
	}
	in := &IntakeSuggestionLookup{TenantID: "1", ID: "50"}
	got, err := m.lookupIntakeSuggestionHTTP(testContext(t, 25), in)
	require.NoError(t, err)
	require.Equal(t, guid.ID(3), got.Body.Data.ProjectID)
	require.NotNil(t, got.Body.Data.Content)
	allowed = false
	_, err = m.lookupIntakeSuggestionHTTP(testContext(t, 25), in)
	require.Error(t, err)
	in.TenantID = "2"
	_, err = m.lookupIntakeSuggestionHTTP(testContext(t, 25), in)
	require.Error(t, err)
}
