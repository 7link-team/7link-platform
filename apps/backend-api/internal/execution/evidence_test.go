package execution

import (
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/files"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func manualEvidenceFixture(t *testing.T) (*executionFixture, context.Context, ExecutionView) {
	f := verifierRegistryFixture(t)
	f.m.commentMembers = commentMemberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	f.m.agents = fleet.AgentDirectory{ByPrincipalOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 220, TenantID: 7, PrincipalID: 22, State: "ACTIVE"}, true, nil
	}}
	f.m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, id == 11, nil
	}
	f.m.commentContext.CheckOn = func(_ context.Context, _ *gorm.DB, tenant, work guid.ID, seen *int64, required bool) error {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 3, work)
		if required && seen == nil {
			return delivery.ErrWorkSeenSeqRequired
		}
		if seen != nil && *seen < 1 {
			return &delivery.WorkWriteInterrupted{State: "interrupted", Stream: "work_item:3", CurrentSeq: 1, NewerEvents: []delivery.WorkActivityView{{Seq: 1, Kind: "COMMENT_ADDED"}}}
		}
		return nil
	}
	v := f.create(t)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, EntityID: 9, PrincipalID: 22, SubjectType: authn.SubjectTypePrincipal})
	return f, ctx, v
}

func TestManualArtifactEvidencePinsExactSourceAndHashesFrozenRepresentation(t *testing.T) {
	f, ctx, v := manualEvidenceFixture(t)
	_, pins, _, _ := bindRealArtifactFiles(t, f, true)
	f.m.commentFiles = pins
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	bytes := []byte("package example\n")
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: "Runtime artifact", Upload: &ArtifactUpload{Name: "source.go", Data: bytes, Coverage: "COVERED"}})
	require.NoError(t, err)
	q := query.BindEvidence(f.db, f.m.names)
	source, err := q.WithContext(ctx).Where(q.ExecutionID.Eq(v.ID), q.Kind.Eq("FILE")).First()
	require.NoError(t, err)
	fresh := int64(1)
	var created EvidenceView
	err = f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		created, e = f.m.CreateEvidenceOn(ctx, tx, 7, v.ID, EvidenceCreate{SeenSeq: &fresh, EvidenceType: "ARTIFACT", ArtifactID: source.ID, Title: "Referenced artifact"})
		return e
	})
	require.NoError(t, err)
	require.NotEqual(t, source.ID, created.ID)
	require.Equal(t, hash([]byte(created.Content)), created.ContentHash)
	var fact, prior ArtifactFact
	require.NoError(t, json.Unmarshal([]byte(created.Content), &fact))
	require.NoError(t, json.Unmarshal([]byte(source.Content), &prior))
	require.Equal(t, prior.VersionID, fact.VersionID)
	require.Equal(t, hash(bytes), fact.SHA256)
	require.NotEqual(t, prior.BindingID, fact.BindingID)
}

func TestPullRequestEvidencePreservesUserLimitationsAndStableUnverifiedCode(t *testing.T) {
	f, ctx, v := manualEvidenceFixture(t)
	fresh := int64(1)
	var created EvidenceView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		created, e = f.m.CreateEvidenceOn(ctx, tx, 7, v.ID, EvidenceCreate{SeenSeq: &fresh, EvidenceType: "PULL_REQUEST", Title: "External review", URL: "https://example.test/changes/42", LimitationsExtra: "Waiting for the maintainer"})
		return e
	}))
	require.Equal(t, "Waiting for the maintainer", *created.Limitations)
	require.Equal(t, []string{"GIT_CONNECTOR_UNVERIFIED"}, created.LimitationCodes)
	require.Contains(t, created.Content, `"verification":"GIT_CONNECTOR_UNVERIFIED"`)
}

func TestManualEvidenceAgentCursorRefreshReplayAndActualClaimFreeze(t *testing.T) {
	f, ctx, v := manualEvidenceFixture(t)
	in := &evidenceCreateInput{ExecutionInput: ExecutionInput{ExecutionID: v.ID.String()}, IdempotencyKey: "same-observation", Body: EvidenceCreate{EvidenceType: "OBSERVATION", Title: "Observed behavior", ScopeText: "Task screen", Text: "The submit action displays a confirmation."}}
	_, err := f.m.createEvidenceHTTP(ctx, in)
	require.Error(t, err)
	stale := int64(0)
	in.Body.SeenSeq = &stale
	_, err = f.m.createEvidenceHTTP(ctx, in)
	require.Error(t, err)
	var count int64
	require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Count(&count).Error)
	require.Zero(t, count)
	fresh := int64(1)
	in.Body.SeenSeq = &fresh
	created, err := f.m.createEvidenceHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "DRAFT", created.Body.Data.State)
	require.Equal(t, "AGENT", created.Body.Data.ProducerKind)
	fresh = 2
	replay, err := f.m.createEvidenceHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, created.Body.Data.ID, replay.Body.Data.ID)
	title := "Updated scope title"
	update := &evidenceUpdateInput{EvidenceID: created.Body.Data.ID.String(), IdempotencyKey: "same-metadata", Body: EvidenceUpdate{SeenSeq: &stale, Title: &title}}
	_, err = f.m.updateEvidenceHTTP(ctx, update)
	require.Error(t, err)
	update.Body.SeenSeq = &fresh
	changed, err := f.m.updateEvidenceHTTP(ctx, update)
	require.NoError(t, err)
	require.Equal(t, title, changed.Body.Data.Title)
	replayUpdate, err := f.m.updateEvidenceHTTP(ctx, update)
	require.NoError(t, err)
	require.Equal(t, "true", replayUpdate.Replayed)
	f.pull(t, v.ID)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	commit := strings.Repeat("a", 40)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "COMMIT", EvidenceTitle: "Actual runtime commit", Content: commit})
	require.NoError(t, err)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "COMPLETED", HeadCommit: commit})
	require.NoError(t, err)
	q := query.BindEvidence(f.db, f.m.names)
	attached, err := q.WithContext(ctx).Where(q.ID.Eq(created.Body.Data.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ATTACHED", attached.State)
	require.Equal(t, v.ID, *attached.ClaimID)
	update.IdempotencyKey = "after-attach"
	_, err = f.m.updateEvidenceHTTP(ctx, update)
	require.Error(t, err)
	f.authorized = false
	_, err = f.m.createEvidenceHTTP(ctx, in)
	require.Error(t, err, "replay must check current authority")
}

func TestManualEvidenceWithdrawRestoreAndOutboxRollback(t *testing.T) {
	f, ctx, v := manualEvidenceFixture(t)
	fresh := int64(1)
	create := func(text string) (EvidenceView, error) {
		var out EvidenceView
		err := f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = f.m.CreateEvidenceOn(ctx, tx, 7, v.ID, EvidenceCreate{SeenSeq: &fresh, EvidenceType: "OBSERVATION", Title: "Observation", ScopeText: "Scoped behavior", Text: text})
			return e
		})
		return out, err
	}
	evidence, err := create("First observed outcome")
	require.NoError(t, err)
	state := "WITHDRAWN"
	change := func() error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			_, e := f.m.UpdateEvidenceOn(ctx, tx, 7, evidence.ID, EvidenceUpdate{SeenSeq: &fresh, State: &state})
			return e
		})
	}
	require.NoError(t, change())
	state = "DRAFT"
	require.NoError(t, change())
	f.pub.fail = true
	_, err = create("Second observed outcome")
	require.Error(t, err)
	var count int64
	require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
	for _, kind := range []string{"COMMIT", "TEST_REPORT", "LOG"} {
		require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
			_, e := f.m.CreateEvidenceOn(ctx, tx, 7, v.ID, EvidenceCreate{SeenSeq: &fresh, EvidenceType: kind, Title: "Fabricated runtime report", ScopeText: "source", Text: "claimed"})
			return e
		}), ErrInvalidArgument)
	}
}

func TestManualScreenshotEvidenceUsesRealPinsAndRejectsNonImage(t *testing.T) {
	f, ctx, v := manualEvidenceFixture(t)
	_, pins, _, _ := bindRealArtifactFiles(t, f, true)
	f.m.commentFiles = pins
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	fresh := int64(1)
	for i, fixture := range []struct {
		name     string
		data     []byte
		accepted bool
	}{
		{"readme.txt", []byte("not a screenshot"), false},
		{"screen.png", encoded.Bytes(), true},
	} {
		_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: int64(i + 2), Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: fixture.name, Upload: &ArtifactUpload{Name: fixture.name, Data: fixture.data, Coverage: "COVERED"}})
		require.NoError(t, err)
		q := query.BindEvidence(f.db, f.m.names)
		source, err := q.WithContext(ctx).Where(q.ExecutionID.Eq(v.ID), q.Title.Eq(fixture.name)).First()
		require.NoError(t, err)
		var prior ArtifactFact
		require.NoError(t, json.Unmarshal([]byte(source.Content), &prior))
		var created EvidenceView
		err = f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			created, e = f.m.CreateEvidenceOn(ctx, tx, 7, v.ID, EvidenceCreate{SeenSeq: &fresh, EvidenceType: "SCREENSHOT", FileID: prior.FileID, Title: "Manual screenshot"})
			return e
		})
		if !fixture.accepted {
			require.ErrorIs(t, err, ErrInvalidArgument)
			count, e := q.WithContext(ctx).Where(q.Kind.Eq("SCREENSHOT")).Count()
			require.NoError(t, e)
			require.Zero(t, count)
			continue
		}
		require.NoError(t, err)
		var fact ArtifactFact
		require.NoError(t, json.Unmarshal([]byte(created.Content), &fact))
		require.Equal(t, prior.VersionID, fact.VersionID)
		require.Equal(t, hash(fixture.data), fact.SHA256)
		require.NotEqual(t, prior.BindingID, fact.BindingID)
		require.Equal(t, hash([]byte(created.Content)), created.ContentHash)
	}
}

func TestManualEvidenceEditsValidateFieldsAndPreserveImmutablePayload(t *testing.T) {
	for _, fault := range []string{"none", "state", "title", "limitations", "empty", "evidence_missing", "execution_corrupt", "terminal", "id"} {
		t.Run(fault, func(t *testing.T) {
			f, _, v := manualEvidenceFixture(t)
			var created EvidenceView
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				created, e = f.m.CreateEvidenceOn(f.human, tx, 7, v.ID, EvidenceCreate{Title: "Original", EvidenceType: "OBSERVATION", ScopeText: "Actual screen", Text: "Observed state"})
				return e
			}))
			state, title, limitations := "WITHDRAWN", "Revised title", "Only one viewport was observed"
			in := EvidenceUpdate{State: &state, Title: &title, LimitationsExtra: &limitations}
			expected := ErrInvalidArgument
			switch fault {
			case "state":
				state = "ATTACHED"
			case "title":
				title = strings.Repeat("x", 201)
			case "limitations":
				limitations = strings.Repeat("x", 1001)
			case "empty":
				in = EvidenceUpdate{}
			case "evidence_missing":
				created.ID++
				expected = gorm.ErrRecordNotFound
			case "execution_corrupt":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("plan_json", "{").Error)
				expected = ErrUnavailable
			case "terminal":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("state", "FAILED").Error)
				expected = ErrEvidenceAttached
			case "id":
				created.ID = 0
			}
			var before []map[string]any
			require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Find(&before).Error)
			e := f.db.Transaction(func(tx *gorm.DB) error {
				out, e := f.m.UpdateEvidenceOn(f.human, tx, 7, created.ID, in)
				if fault == "none" && e == nil {
					require.Equal(t, state, out.State)
					require.Equal(t, title, out.Title)
				}
				return e
			})
			if fault == "none" {
				require.NoError(t, e)
			} else {
				require.ErrorIs(t, e, expected)
				var after []map[string]any
				require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Find(&after).Error)
				require.Equal(t, before, after)
			}
		})
	}
}

func TestPinnedManualEvidenceEveryStorageFailureRollsBackDraftAndActivity(t *testing.T) {
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f, _, v := manualEvidenceFixture(t)
		f.m.commentFiles = manualEvidencePins{candidate: files.PinnedCandidate{PinnedResource: files.PinnedResource{FileID: 2, VersionID: 3, SHA256: hash(nil), MIME: "image/png", Size: 4}}}
		return f, func(tx *gorm.DB) error {
			out, e := f.m.CreateEvidenceOn(f.human, tx, 7, v.ID, EvidenceCreate{Title: "Actual screen", EvidenceType: "SCREENSHOT", FileID: 2})
			if e == nil {
				require.Equal(t, "DRAFT", out.State)
			}
			return e
		}
	})
}

func TestManualEvidenceEveryStorageFailurePreservesDraftAndActivity(t *testing.T) {
	for _, command := range []string{"create", "update"} {
		for _, kind := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				f, _, v := manualEvidenceFixture(t)
				in := EvidenceCreate{Title: "Current observation", EvidenceType: "OBSERVATION", ScopeText: "Current screen", Text: "Observed submission confirmation"}
				var existing EvidenceView
				if command == "update" {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
						var e error
						existing, e = f.m.CreateEvidenceOn(f.human, tx, 7, v.ID, in)
						return e
					}))
				}
				snapshot := func() map[string][]map[string]any {
					out := map[string][]map[string]any{}
					for _, table := range []string{f.m.names.Executions.String(), f.m.names.Receipts.String(), f.m.names.Logs.String(), f.m.names.Evidence.String(), f.m.names.Approvals.String(), "slots", "effects"} {
						var rows []map[string]any
						require.NoError(t, f.db.Table(table).Order("rowid").Find(&rows).Error)
						out[table] = rows
					}
					return out
				}
				before := snapshot()
				count := 0
				injected := errors.New("execution transactional storage unavailable")
				hook := func(tx *gorm.DB) {
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch kind {
				case "query":
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("execution_transaction_failure", hook))
				case "create":
					require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("execution_transaction_failure", hook))
				case "update":
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("execution_transaction_failure", hook))
				}
				err := f.db.Transaction(func(tx *gorm.DB) error {
					if command == "create" {
						_, e := f.m.CreateEvidenceOn(f.human, tx, 7, v.ID, in)
						return e
					}
					title := "Updated observation"
					_, e := f.m.UpdateEvidenceOn(f.human, tx, 7, existing.ID, EvidenceUpdate{Title: &title})
					return e
				})
				require.NoError(t, f.db.Callback().Query().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Create().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Update().Remove("execution_transaction_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				require.Equal(t, before, snapshot(), "failed command must roll back execution, journals, slots and effects")
				return count
			}
			total := 0
			t.Run(command+"/"+kind+"/success", func(t *testing.T) { total = exercise(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(fmt.Sprintf("%s/%s/failure_%d", command, kind, at), func(t *testing.T) { require.GreaterOrEqual(t, exercise(t, at), at) })
			}
		}
	}
}

type manualEvidencePins struct {
	files.PinnedResources
	candidate            files.PinnedCandidate
	inspectErr, errorPin error
}

func (p manualEvidencePins) InspectOn(context.Context, *gorm.DB, guid.ID) (files.PinnedCandidate, error) {
	return p.candidate, p.inspectErr
}
func (p manualEvidencePins) PinOn(context.Context, *gorm.DB, files.ResourceBindingInput) (files.PinnedResource, error) {
	return files.PinnedResource{}, p.errorPin
}
func TestManualEvidencePinnedSourcesRejectChangedOrUnavailableContent(t *testing.T) {
	for _, fault := range []string{"missing", "inspect", "pin", "type", "size", "version", "hash", "source_json", "source_file"} {
		t.Run(fault, func(t *testing.T) {
			f, ctx, v := manualEvidenceFixture(t)
			fresh := int64(1)
			failure := errors.New("evidence pin unavailable")
			pins := manualEvidencePins{candidate: files.PinnedCandidate{PinnedResource: files.PinnedResource{FileID: 2, VersionID: 3, SHA256: hash(nil), MIME: "image/png", Size: 4}}, errorPin: failure}
			in := EvidenceCreate{SeenSeq: &fresh, EvidenceType: "SCREENSHOT", Title: "Observed screen", FileID: 2}
			expected := ErrInvalidArgument
			switch fault {
			case "missing":
				expected = ErrCapabilityUnavailable
			case "inspect":
				pins.inspectErr = failure
				expected = failure
			case "pin":
				expected = failure
			case "type":
				pins.candidate.MIME = "text/plain"
			case "size":
				pins.candidate.Size = 20*1024*1024 + 1
			default:
				in.EvidenceType = "ARTIFACT"
				in.FileID = 0
				in.ArtifactID = 900
				fact := ArtifactFact{FileID: 2, VersionID: 3, SHA256: hash(nil)}
				if fault == "version" {
					fact.VersionID = 4
					expected = ErrConflict
				}
				if fault == "hash" {
					fact.SHA256 = hash([]byte("changed"))
					expected = ErrConflict
				}
				if fault == "source_file" {
					fact.FileID = 0
					expected = ErrUnavailable
				}
				raw, e := json.Marshal(fact)
				require.NoError(t, e)
				if fault == "source_json" {
					raw = []byte("{")
					expected = ErrUnavailable
				}
				require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Create(&model.Evidence{ID: 900, TenantID: 7, ProjectID: 9, ExecutionID: v.ID, Kind: "FILE", Title: "Pinned source", Content: string(raw), ContentHash: hash(raw)}).Error)
			}
			if fault != "missing" {
				f.m.commentFiles = pins
			} else {
				f.m.commentFiles = nil
			}
			before := f.read(t, v.ID)
			e := f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CreateEvidenceOn(ctx, tx, 7, v.ID, in); return e })
			require.ErrorIs(t, e, expected)
			require.Equal(t, before, f.read(t, v.ID))
			var count int64
			require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Where("state=? AND id<>?", "DRAFT", 900).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
