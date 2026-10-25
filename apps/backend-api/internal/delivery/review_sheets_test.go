package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRequirementSheetReworkBoundsCombinedFeedbackWithoutLosingHistory(t *testing.T) {
	for _, scenario := range []struct {
		name                    string
		reviewers, commentRunes int
		excerpt                 bool
	}{{"verification_method_overflow", 2, 1500, false}, {"description_overflow", 12, 2000, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			m, db, ctx, req := sheetHarness(t)
			reviewers := make([]guid.ID, scenario.reviewers)
			for i := range reviewers {
				reviewers[i] = guid.ID(11 + i)
			}
			var sheet RequirementReviewSheetView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				sheet, e = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: reviewers, Minimum: len(reviewers)})
				return e
			}))
			criterion := sheet.Document.Criteria[0]
			comments := map[guid.ID]string{}
			for _, reviewer := range reviewers {
				prefix := reviewer.String() + ": "
				comment := prefix + strings.Repeat("界", scenario.commentRunes-len(prefix))
				comments[reviewer] = comment
				actor := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: reviewer, SubjectType: authn.SubjectTypePrincipal})
				input := &sheetActionInput{}
				input.Body.Version = sheet.Version
				input.Body.Action = "OPINION"
				input.Body.Opinion = RequirementReviewOpinion{Decision: "REJECT", Criteria: map[string]string{criterion.ID.String(): comment}}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var e error
					sheet, e = m.actionSheetOn(actor, tx, 99, sheet.ID, input)
					return e
				}))
			}
			closeInput := &sheetActionInput{}
			closeInput.Body.Version = sheet.Version
			closeInput.Body.Action = "CLOSE"
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				sheet, e = m.actionSheetOn(ctx, tx, 99, sheet.ID, closeInput)
				return e
			}))
			require.Equal(t, "REJECTED", sheet.State)
			require.Len(t, sheet.Document.ReworkIDs, 1)
			w := query.BindWorkItems(db, m.service.names)
			work, e := w.WithContext(ctx).Where(w.ID.Eq(sheet.Document.ReworkIDs[0])).First()
			require.NoError(t, e)
			require.LessOrEqual(t, utf8.RuneCountInString(work.Description), 20000)
			require.Contains(t, work.Description, "Review sheet "+sheet.ID.String())
			require.Contains(t, work.Description, "requirement "+req.ID.String())
			require.Contains(t, work.Description, "criterion "+criterion.ID.String())
			if scenario.excerpt {
				require.Contains(t, work.Description, "[Feedback excerpt; full opinions retained in review sheet history.]")
			} else {
				for _, comment := range comments {
					require.Contains(t, work.Description, comment)
				}
			}
			c := query.BindWorkCriteria(db, m.service.names)
			clauses, e := c.WithContext(ctx).Where(c.WorkItemID.Eq(work.ID)).Find()
			require.NoError(t, e)
			require.Len(t, clauses, 1)
			require.Equal(t, criterion.Text, clauses[0].VerificationMethod)
			h := query.BindReviewOpinions(db, m.service.names)
			history, e := h.WithContext(ctx).Where(h.SheetID.Eq(sheet.ID)).Find()
			require.NoError(t, e)
			require.Len(t, history, len(reviewers))
			for _, row := range history {
				var opinion RequirementReviewOpinion
				require.NoError(t, json.Unmarshal([]byte(row.Document), &opinion))
				require.Equal(t, comments[row.ReviewerID], opinion.Criteria[criterion.ID.String()])
			}
		})
	}
}

func TestRequirementSheetInboxUsesCurrentInvitationsAndAuthority(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	var sheet RequirementReviewSheetView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		sheet, e = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
		return e
	}))
	request := governance.InboxRequest{TenantID: 1, PrincipalID: 11, Kind: "REVIEW_SHEET", Limit: 1}
	items, e := m.reviewSheetInbox(ctx, request)
	require.NoError(t, e)
	require.Len(t, items, 1)
	require.Equal(t, sheet.ID, items[0].ObjectID)
	request.After = &items[0].InboxKey
	items, e = m.reviewSheetInbox(ctx, request)
	require.NoError(t, e)
	require.Empty(t, items)
	request.After = nil
	request.ProjectID = 100
	items, e = m.reviewSheetInbox(ctx, request)
	require.NoError(t, e)
	require.Empty(t, items)
	request.ProjectID = 0
	m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
	items, e = m.reviewSheetInbox(ctx, request)
	require.NoError(t, e)
	require.Empty(t, items)
	request.PrincipalID = 12
	_, e = m.reviewSheetInbox(ctx, request)
	require.ErrorIs(t, e, ErrWorkForbidden)
}

func TestRequirementSheetTriageBindingUsesActualAgentIDAndCurrentState(t *testing.T) {
	m, db, ctx, _ := sheetHarness(t)
	m.triageAgents.AgentOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: id, TenantID: tenant, PrincipalID: 12, State: "ACTIVE"}, true, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.validateTriageAgentOn(ctx, tx, 1, 77) }))
	m.triageAgents.AgentOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: id, TenantID: tenant, PrincipalID: 12, State: "REVOKED"}, true, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.validateTriageAgentOn(ctx, tx, 1, 77) }), ErrWorkForbidden)
}

func TestRequirementSheetOpinionHistoryIsAppendOnlyScopedAndAtomic(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	created, e := m.createSheetHTTP(ctx, &sheetCreateInput{EntityID: "99", Key: "history-open", Body: RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1}})
	require.NoError(t, e)
	id := created.Body.Data.ID
	input := &sheetActionInput{RequirementReviewSheetPath: RequirementReviewSheetPath{EntityID: "99", SheetID: id.String()}, Key: "history-first"}
	input.Body.Version = 1
	input.Body.Action = "OPINION"
	input.Body.Opinion = RequirementReviewOpinion{Decision: "PASS", Comment: "Original", Criteria: map[string]string{}}
	_, e = m.actionSheetHTTP(ctx, input)
	require.NoError(t, e)
	_, e = m.actionSheetHTTP(ctx, input)
	require.NoError(t, e, "idempotent replay never appends a second history row")
	input.Key = "history-second"
	input.Body.Version = 2
	input.Body.Opinion.Comment = "Replacement"
	_, e = m.actionSheetHTTP(ctx, input)
	require.NoError(t, e)
	history := &requirementReviewHistoryInput{RequirementReviewSheetPath: input.RequirementReviewSheetPath, Limit: 1}
	page, e := m.listReviewOpinionsHTTP(ctx, history)
	require.NoError(t, e)
	require.Len(t, page.Body.Data, 1)
	require.Equal(t, "Replacement", page.Body.Data[0].Opinion.Comment)
	previous := page.Body.Data[0].SupersedesID
	require.NotNil(t, previous)
	history.Offset = 1
	page, e = m.listReviewOpinionsHTTP(ctx, history)
	require.NoError(t, e)
	require.Len(t, page.Body.Data, 1)
	require.Equal(t, *previous, page.Body.Data[0].ID)
	require.Equal(t, "Original", page.Body.Data[0].Opinion.Comment)
	require.Nil(t, page.Body.Data[0].SupersedesID)
	latest := readSheetTest(t, m, db, ctx, id)
	require.Len(t, latest.Document.Opinions, 1)
	require.Equal(t, "Replacement", latest.Document.Opinions[0].Comment)
	input.Key = "history-failed"
	input.Body.Version = 3
	failure := errors.New("outbox unavailable")
	m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
	_, e = m.actionSheetHTTP(ctx, input)
	require.Error(t, e)
	q := query.BindReviewOpinions(db, m.service.names)
	count, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Equal(t, int64(2), count)
	history.EntityID = "100"
	_, e = m.listReviewOpinionsHTTP(ctx, history)
	require.Error(t, e, "another project cannot read the sheet history")
	history.EntityID = "99"
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = m.listReviewOpinionsHTTP(ctx, history)
	require.Error(t, e, "current requirement visibility fences historical opinions")
}

func sheetHarness(t *testing.T) (*Module, *gorm.DB, context.Context, RequirementView) {
	m, db, ctx, body := requirementHarness(t)
	m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:reviewer"}, nil
	}
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	var req RequirementView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		req, e = m.createRequirementOn(ctx, tx, 1, 99, 11, body)
		return e
	}))
	return m, db, ctx, req
}
func TestRequirementSheetLatestOpinionMinimumCancellationAndRework(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	in := &sheetCreateInput{EntityID: "99", Key: "review-create", Body: RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Requirements", Reviewers: []guid.ID{11, 12}, Minimum: 2}}
	created, e := m.createSheetHTTP(ctx, in)
	require.NoError(t, e)
	v := readSheetTest(t, m, db, ctx, created.Body.Data.ID)
	require.Equal(t, "OPEN", v.State)
	replay, e := m.createSheetHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, v.ID, replay.Body.Data.ID)
	_, e = m.createSheetHTTP(ctx, &sheetCreateInput{EntityID: "99", Key: "duplicate", Body: in.Body})
	require.Error(t, e)
	action := func(actor guid.ID, kind, decision string, criteria map[string]string) error {
		call := &sheetActionInput{}
		call.Body.Version = v.Version
		call.Body.Action = kind
		call.Body.Opinion = RequirementReviewOpinion{Decision: decision, Criteria: criteria}
		call.Body.Reason = "No longer needed"
		subject := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal})
		return db.Transaction(func(tx *gorm.DB) error {
			next, err := m.actionSheetOn(subject, tx, 99, v.ID, call)
			if err == nil {
				v = next
			}
			return err
		})
	}
	require.ErrorIs(t, action(11, "CLOSE", "", nil), ErrWorkVersion)
	require.ErrorIs(t, action(13, "OPINION", "PASS", nil), ErrWorkForbidden)
	require.ErrorIs(t, action(11, "OPINION", "REJECT", nil), ErrInvalidArgument)
	require.NoError(t, action(11, "OPINION", "PASS", nil))
	require.NoError(t, action(11, "OPINION", "SUGGEST", nil))
	require.Len(t, v.Document.Opinions, 1)
	require.Equal(t, "SUGGEST", v.Document.Opinions[0].Decision)
	criterion := v.Document.Criteria[0].ID.String()
	require.NoError(t, action(12, "OPINION", "REJECT", map[string]string{criterion: "Clarify the expected output"}))
	require.NoError(t, action(11, "CLOSE", "", nil))
	require.Equal(t, "REJECTED", v.State)
	require.Len(t, v.Document.ReworkIDs, 1)
	q := query.BindWorkItems(db, m.service.names)
	work, e := q.WithContext(ctx).Where(q.ID.Eq(v.Document.ReworkIDs[0])).First()
	require.NoError(t, e)
	require.Equal(t, "TASK", work.Type)
	require.Equal(t, "DRAFT", work.State)
	require.ErrorIs(t, action(11, "OPINION", "PASS", nil), ErrWorkVersion)
	in.Key = "second-review"
	current, e := m.readRequirement(ctx, db, 1, 99, req.ID, 11)
	require.NoError(t, e)
	in.Body.RequirementVersion = current.Version
	req.Version = current.Version
	created, e = m.createSheetHTTP(ctx, in)
	require.NoError(t, e)
	v = readSheetTest(t, m, db, ctx, created.Body.Data.ID)
	req.Version = v.Document.RequirementVersion
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		title := "Updated requirement"
		_, e := m.patchRequirementOn(ctx, tx, 1, 99, req.ID, 11, req.Version, requirementPatchBody{Title: &title})
		return e
	}))
	sq := query.BindReviewSheets(db, m.service.names)
	r, e := sq.WithContext(ctx).Where(sq.ID.Eq(v.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "CANCELLED", r.State) //nolint:misspell // Frozen persisted protocol state.
	require.Nil(t, r.OpenRequirementID)
}
func TestRequirementSheetCurrentAuthorityAndOutboxFailureRollBack(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	body := RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1}
	failure := errors.New("outbox unavailable")
	m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createSheetOn(ctx, tx, 99, body); return e }), failure)
	q := query.BindReviewSheets(db, m.service.names)
	n, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Zero(t, n)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createSheetOn(ctx, tx, 99, body); return e }), ErrWorkForbidden)
}
func TestRequirementSheetOpinionRejectReferencesOnlyFrozenCriteria(t *testing.T) {
	d := SheetDocumentForTest()
	require.ErrorIs(t, validateRequirementReviewOpinion(d, RequirementReviewOpinion{Decision: "REJECT", Criteria: map[string]string{"999": "No"}}), ErrInvalidArgument)
	require.NoError(t, validateRequirementReviewOpinion(d, RequirementReviewOpinion{Decision: "REJECT", Criteria: map[string]string{"1": "Mismatch"}}))
}
func SheetDocumentForTest() RequirementReviewDocument {
	return RequirementReviewDocument{Criteria: []requirementCriterionView{{ID: 1}}}
}

func TestRequirementSheetAgentSuggestionsNeverCountAsHumanVotes(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, id != 20, nil
	}
	m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 200, TenantID: tenant, PrincipalID: id, State: "ACTIVE"}, id == 20, nil
	}
	body := RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11, 20}, Minimum: 1}
	var v RequirementReviewSheetView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; v, e = m.createSheetOn(ctx, tx, 99, body); return e }))
	agent := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 20, SubjectType: authn.SubjectTypePrincipal})
	action := &sheetActionInput{}
	action.Body.Version = v.Version
	action.Body.Action = "OPINION"
	action.Body.Opinion = RequirementReviewOpinion{Decision: "PASS"}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.actionSheetOn(agent, tx, 99, v.ID, action); return e }), ErrWorkForbidden)
	action.Body.Opinion.Decision = "SUGGEST"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; v, e = m.actionSheetOn(agent, tx, 99, v.ID, action); return e }))
	require.Equal(t, "AGENT", v.Document.Opinions[0].ActorKind)
	action.Body.Version = v.Version
	action.Body.Action = "CLOSE"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.actionSheetOn(ctx, tx, 99, v.ID, action); return e }), ErrWorkVersion)
	body.Minimum = 2
	body.RequirementVersion = v.Document.RequirementVersion
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createSheetOn(ctx, tx, 99, body); return e }), ErrInvalidArgument)
}

func TestRequirementSheetHTTPExtractsBothPathIDsWithoutServerOwnedOpinionFields(t *testing.T) {
	_, api := humatest.New(t)
	path := "/entities/{entity_id}/review-sheets/{sheet_id}/actions"
	huma.Register(api, huma.Operation{OperationID: "sheet-contract", Method: "POST", Path: path}, func(_ context.Context, in *sheetActionInput) (*struct{ Body string }, error) {
		require.Equal(t, "99", in.EntityID)
		require.Equal(t, "101", in.SheetID)
		return &struct{ Body string }{Body: in.Body.Action}, nil
	})
	op := api.OpenAPI().Paths[path].Post
	names := map[string]bool{}
	for _, param := range op.Parameters {
		names[param.Name] = true
	}
	require.True(t, names["entity_id"])
	require.True(t, names["sheet_id"])
	require.True(t, names["If-Match"])
	missing := api.Post("/entities/99/review-sheets/101/actions", "Idempotency-Key: missing-match", strings.NewReader(`{"version":1,"action":"CLOSE"}`))
	require.Equal(t, 422, missing.Code, "missing concurrency header fails schema validation before the command")

	for _, body := range []string{`{"version":1,"action":"CLOSE"}`, `{"version":1,"action":"CANCEL","reason":"Superseded"}`, `{"version":1,"action":"OPINION","opinion":{"decision":"PASS","comment":"Ready","criteria":{}}}`} {
		response := api.Post("/entities/99/review-sheets/101/actions", `If-Match: "1"`, "Idempotency-Key: test-key", strings.NewReader(body))
		require.Equal(t, 200, response.Code, response.Body.String())
	}
}

func readSheetTest(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, id guid.ID) RequirementReviewSheetView {
	t.Helper()
	q := query.BindReviewSheets(db, m.service.names)
	r, e := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	v, e := sheetView(r)
	require.NoError(t, e)
	return v
}

func TestRequirementSheetLargeSnapshotsUseReplayableBoundedReceipts(t *testing.T) {
	m, db, ctx, _ := sheetHarness(t)
	body := requirementCreateBody{Title: "Large requirement", Criteria: make([]requirementCriterionInput, 50)}
	for i := range body.Criteria {
		body.Criteria[i] = requirementCriterionInput{Text: strings.Repeat("x", 2000), Required: true}
	}
	var req RequirementView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		req, e = m.createRequirementOn(ctx, tx, 1, 99, 11, body)
		return e
	}))
	in := &sheetCreateInput{EntityID: "99", Key: "large-sheet", Body: RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Requirements", Reviewers: []guid.ID{11}, Minimum: 1}}
	created, e := m.createSheetHTTP(ctx, in)
	require.NoError(t, e)
	encoded, e := json.Marshal(created.Body.Data)
	require.NoError(t, e)
	require.Less(t, len(encoded), 1024)
	stored := readSheetTest(t, m, db, ctx, created.Body.Data.ID)
	document, e := json.Marshal(stored.Document)
	require.NoError(t, e)
	require.Greater(t, len(document), 65536)
	replay, e := m.createSheetHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, created.Body.Data, replay.Body.Data)
}

func TestReviewSheetPersistenceFailuresCannotLeavePartialOpinionOrClosure(t *testing.T) {
	for _, action := range []string{"OPINION", "CLOSE", "CANCEL"} {
		t.Run(action, func(t *testing.T) {
			total := 0
			for failAt := 0; failAt <= total; failAt++ {
				m, db, ctx, req := sheetHarness(t)
				var sheet RequirementReviewSheetView
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var e error
					sheet, e = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
					return e
				}))
				in := &sheetActionInput{}
				in.Body.Action = "OPINION"
				in.Body.Version = sheet.Version
				in.Body.Opinion = RequirementReviewOpinion{Decision: "PASS", Comment: "Verified against frozen criteria"}
				if action == "CLOSE" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; sheet, e = m.actionSheetOn(ctx, tx, 99, sheet.ID, in); return e }))
				}
				before := readSheetTest(t, m, db, ctx, sheet.ID)
				in.Body.Action = action
				in.Body.Version = sheet.Version
				in.Body.Reason = "Superseded by current requirement"
				failure := errors.New("sheet persistence unavailable")
				calls := 0
				probe := func(tx *gorm.DB) {
					calls++
					if failAt > 0 && calls == failAt {
						tx.AddError(failure)
					}
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("sheet-current-failure", probe))
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("sheet-current-failure", probe))
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("sheet-current-failure", probe))
				err := db.Transaction(func(tx *gorm.DB) error { _, e := m.actionSheetOn(ctx, tx, 99, sheet.ID, in); return e })
				require.NoError(t, db.Callback().Query().Remove("sheet-current-failure"))
				require.NoError(t, db.Callback().Create().Remove("sheet-current-failure"))
				require.NoError(t, db.Callback().Update().Remove("sheet-current-failure"))
				if failAt == 0 {
					require.NoError(t, err)
					total = calls
					require.Greater(t, total, 3)
				} else {
					require.ErrorIs(t, err, failure, "%s persistence step %d", action, failAt)
					require.Equal(t, before, readSheetTest(t, m, db, ctx, sheet.ID))
				}
			}
		})
	}
}
