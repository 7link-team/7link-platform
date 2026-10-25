package execution

import (
	"context"
	"gorm.io/gorm"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
)

func TestEvidenceHTTPAgentMissingStaleFreshSameKeyAndReplay(t *testing.T) {
	f, ctx, v := manualEvidenceFixture(t)
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "evidence.create", Method: "POST", Path: evidenceCreatePath, DefaultStatus: 201}, func(_ context.Context, in *evidenceCreateInput) (*evidenceOutput, error) {
		return f.m.createEvidenceHTTP(ctx, in)
	})
	path := "/executions/" + v.ID.String() + "/evidence"
	body := `"evidence_type":"OBSERVATION","title":"Observed action","scope_text":"Task view","text":"The action is visible."`
	missing := api.Post(path, "Idempotency-Key: observed", strings.NewReader(`{`+body+`}`))
	require.Equal(t, 422, missing.Code)
	require.Contains(t, missing.Body.String(), "130475")
	stale := api.Post(path, "Idempotency-Key: observed", strings.NewReader(`{`+body+`,"seen_seq":0}`))
	require.Equal(t, 409, stale.Code)
	require.Contains(t, stale.Body.String(), "130474")
	require.Contains(t, stale.Body.String(), "work_item:3")
	fresh := api.Post(path, "Idempotency-Key: observed", strings.NewReader(`{`+body+`,"seen_seq":1}`))
	require.Equal(t, 201, fresh.Code)
	replay := api.Post(path, "Idempotency-Key: observed", strings.NewReader(`{`+body+`,"seen_seq":2}`))
	require.Equal(t, 201, replay.Code)
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	require.Equal(t, 422, api.Post(path, "Idempotency-Key: fabricated", strings.NewReader(`{"evidence_type":"COMMIT","title":"Fake machine result","seen_seq":1}`)).Code)
}

func TestEvidenceOwnerRejectsMissingWriterAndRecord(t *testing.T) {
	for _, missingWriter := range []bool{false, true} {
		f := executionHarness(t)
		if missingWriter {
			f.m.runtime = nil
		}
		_, found, e := f.m.evidenceOwner(f.human, 999)
		require.Error(t, e)
		require.False(t, found)
	}
}

func TestEvidenceHTTPStorageFailuresKeepDraftAndIdempotencyAtomic(t *testing.T) {
	for _, command := range []string{"create", "update"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f, ctx, v := manualEvidenceFixture(t)
				fresh := int64(1)
				input := &evidenceCreateInput{ExecutionInput: ExecutionInput{ExecutionID: v.ID.String()}, IdempotencyKey: "draft-storage", Body: EvidenceCreate{SeenSeq: &fresh, EvidenceType: "OBSERVATION", Title: "Actual observation", ScopeText: "Source view", Text: "Observed behavior"}}
				var update evidenceUpdateInput
				if command == "update" {
					out, e := f.m.createEvidenceHTTP(ctx, input)
					require.NoError(t, e)
					title := "Reviewed observation"
					update = evidenceUpdateInput{EvidenceID: out.Body.Data.ID.String(), IdempotencyKey: "update-storage", Body: EvidenceUpdate{SeenSeq: &fresh, Title: &title}}
				}
				return f, func(*gorm.DB) error {
					if command == "create" {
						out, e := f.m.createEvidenceHTTP(ctx, input)
						if e == nil {
							require.Equal(t, "DRAFT", out.Body.Data.State)
						}
						return e
					}
					out, e := f.m.updateEvidenceHTTP(ctx, &update)
					if e == nil {
						require.Equal(t, "Reviewed observation", out.Body.Data.Title)
					}
					return e
				}
			}, true)
		})
	}
}
