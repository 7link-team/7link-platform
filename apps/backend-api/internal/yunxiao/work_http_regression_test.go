package yunxiao

import (
	"context"
	"testing"

	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
)

func TestWorklogHTTPUpdateSubmitApproveReplayAndCurrentOwnership(t *testing.T) {
	m, _, ctx := workHarness(t)
	path := WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}
	created, err := m.createWorklogHTTP(ctx, &worklogCreateInput{WorkProjectPath: path, WriteHeaders: WriteHeaders{IdempotencyKey: "new-worklog"}, Body: workInput()})
	require.NoError(t, err)
	ref := WorklogPath{WorkProjectPath: path, WorklogID: created.Body.Data.ID.String()}
	update := worklogUpdateInput{WorklogPath: ref, WriteHeaders: WriteHeaders{IdempotencyKey: "update-worklog", IfMatch: `"1"`}, Body: workInput()}
	update.Body.Minutes = 75
	updated, err := m.updateWorklogHTTP(ctx, &update)
	require.NoError(t, err)
	require.EqualValues(t, 75, updated.Body.Data.Minutes)
	replay, err := m.updateWorklogHTTP(ctx, &update)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, updated.Body.Data, replay.Body.Data)
	change := worklogStateInput{WorklogPath: ref, WriteHeaders: WriteHeaders{IdempotencyKey: "submit-worklog", IfMatch: `"2"`}}
	change.Body.State = "SUBMITTED"
	submitted, err := m.stateWorklogHTTP(ctx, &change)
	require.NoError(t, err)
	require.Equal(t, "SUBMITTED", submitted.Body.Data.State)
	change.IdempotencyKey, change.IfMatch, change.Body.State = "approve-worklog", `"3"`, "APPROVED"
	_, err = m.stateWorklogHTTP(ctx, &change)
	require.Error(t, err, "writer cannot approve own worklog")
	reviewer := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
	approved, err := m.stateWorklogHTTP(reviewer, &change)
	require.NoError(t, err)
	require.Equal(t, "APPROVED", approved.Body.Data.State)
	summary, err := m.summaryWorklogsHTTP(ctx, &path)
	require.NoError(t, err)
	require.EqualValues(t, 75, summary.Body.Data.ApprovedMinutes)
	update.IfMatch, update.IdempotencyKey = `"4"`, "approved-edit"
	_, err = m.updateWorklogHTTP(ctx, &update)
	require.Error(t, err)
}

func TestWorklogHTTPRejectsInvalidSelectorsBeforeMutation(t *testing.T) {
	m := &Module{}
	ctx := context.Background()
	for _, fault := range []string{"tenant", "project", "worklog", "version"} {
		p := WorklogPath{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}, WorklogID: "1"}
		h := WriteHeaders{IfMatch: `"1"`, IdempotencyKey: "invalid-selector"}
		switch fault {
		case "tenant":
			p.TenantID = "0"
		case "project":
			p.ProjectID = "overflow"
		case "worklog":
			p.WorklogID = "0"
		case "version":
			h.IfMatch = "invalid"
		}
		_, err := m.updateWorklogHTTP(ctx, &worklogUpdateInput{WorklogPath: p, WriteHeaders: h})
		require.Error(t, err)
		_, err = m.stateWorklogHTTP(ctx, &worklogStateInput{WorklogPath: p, WriteHeaders: h})
		require.Error(t, err)
	}
}
