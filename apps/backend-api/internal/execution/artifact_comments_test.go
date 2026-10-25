package execution

import (
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/files"
	filesquery "7link.com/platform/internal/files/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestArtifactCommentBindingRegistersActualOperationsAndPropagatesSourceErrors(t *testing.T) {
	for _, fault := range []string{"none", "catalog_missing_callback", "catalog_error", "bind_error"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			s, _ := executionModuleServices(t, f, 19)
			failure := errors.New("comment connector failed")
			var source delivery.ArtifactComments
			require.NoError(t, module.Provide(s, delivery.ArtifactCommentsConnector{Bind: func(v delivery.ArtifactComments) error {
				source = v
				if fault == "bind_error" {
					return failure
				}
				return nil
			}}))
			registered := []string{}
			catalog := agents.MountedOperationCatalog{Register: func(ops ...agents.MountedOperation) error {
				for _, op := range ops {
					registered = append(registered, op.Name)
				}
				if fault == "catalog_error" {
					return failure
				}
				return nil
			}}
			if fault == "catalog_missing_callback" {
				catalog.Register = nil
			}
			require.NoError(t, module.Provide(s, catalog))
			require.NoError(t, s.Seal())
			e := f.m.injectArtifactComments(s)
			if fault == "catalog_missing_callback" {
				require.ErrorIs(t, e, ErrCapabilityUnavailable)
				return
			}
			if fault == "catalog_error" || fault == "bind_error" {
				require.ErrorIs(t, e, failure)
				return
			}
			require.NoError(t, e)
			require.Contains(t, registered, "artifact_comment.reply")
			require.Contains(t, registered, "evidence.create")
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, e := source.ScopeOn(f.human, tx, 7, 999)
				require.ErrorIs(t, e, delivery.ErrProjectNotFound)
				_, e = source.GetOn(f.human, tx, 7, 999)
				require.ErrorIs(t, e, delivery.ErrProjectNotFound)
				e = source.MarkConvertedOn(f.human, tx, 7, 999, "WORK_ITEM", 1000, 11)
				require.Error(t, e)
				return nil
			}))
		})
	}
}

type artifactAnchorReader struct {
	io.Reader
	closeErr error
	closed   int
}

func (r *artifactAnchorReader) Close() error { r.closed++; return r.closeErr }

func TestArtifactCodeAnchorClosesOnReadFailureAndRejectsCloseFailure(t *testing.T) {
	for _, broken := range []string{"read", "close"} {
		t.Run(broken, func(t *testing.T) {
			failure := errors.New("pinned stream failed")
			r := &artifactAnchorReader{Reader: strings.NewReader("one\ntwo")}
			if broken == "read" {
				r.Reader = iotest.ErrReader(failure)
			} else {
				r.closeErr = failure
			}
			digest := hash([]byte("one\ntwo"))
			pin := files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 1, FileID: 2, VersionID: 3, SHA256: digest}, Reader: r}
			anchor, err := artifactCodeAnchor(pin, ArtifactFact{BindingID: 1, FileID: 2, VersionID: 3, SHA256: digest}, 1, 1)
			require.ErrorIs(t, err, failure)
			require.Empty(t, anchor.Kind, "a failed pinned stream cannot produce a trusted anchor")
			require.Equal(t, 1, r.closed)
		})
	}
}

type commentMemberCheck func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)

func (f commentMemberCheck) IsMemberActive(ctx context.Context, t, p guid.ID) (bool, error) {
	return f(ctx, nil, t, p)
}
func (f commentMemberCheck) IsMemberActiveOn(ctx context.Context, tx *gorm.DB, t, p guid.ID) (bool, error) {
	return f(ctx, tx, t, p)
}

func TestArtifactCommentsFreezeRealPinnedBytesAndSeparateAgentAuthority(t *testing.T) {
	f := verifierRegistryFixture(t)
	_, pins, _, _ := bindRealArtifactFiles(t, f, true)
	f.m.commentFiles = pins
	f.m.commentMembers = commentMemberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	f.m.commentActivities = delivery.WorkItemActivityRecorder{RecordOn: func(_ context.Context, tx *gorm.DB, tenant, work guid.ID, in delivery.WorkActivityInput) (int64, error) {
		require.Contains(t, []string{"COMMENT_ADDED", "EVIDENCE_ADDED", "STATE_CHANGED"}, in.Kind)
		return 1, tx.Exec("INSERT INTO effects(kind) VALUES (?)", in.Kind).Error
	}}
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: "Source", Upload: &ArtifactUpload{Name: "source.go", Data: []byte("package sample\nfunc broken() bool { return false }\n"), Coverage: "COVERED"}})
	require.NoError(t, err)
	q := query.BindEvidence(f.db, f.m.names)
	evidence, err := q.WithContext(t.Context()).Where(q.ExecutionID.Eq(v.ID), q.Kind.Eq("FILE")).First()
	require.NoError(t, err)
	var out ArtifactCommentView
	f.human = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 11, EntityID: 9, SubjectType: authn.SubjectTypePrincipal})
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = f.m.CreateArtifactCommentOn(f.human, tx, 7, v.ID, ArtifactCommentCreate{EvidenceID: evidence.ID, StartLine: 2, EndLine: 2, Body: "Returns the wrong value"})
		return e
	}))
	require.Equal(t, "func broken() bool { return false }", out.Anchor.Excerpt)
	source, err := f.m.artifactCommentSourceHTTP(f.human, &artifactCommentSourceInput{ExecutionInput: ExecutionInput{ExecutionID: v.ID.String()}, EvidenceID: evidence.ID.String()})
	require.NoError(t, err)
	require.Equal(t, "package sample\nfunc broken() bool { return false }\n", source.Body.Data.Text)
	require.Equal(t, out.Anchor.VersionID, source.Body.Data.VersionID)
	require.Equal(t, out.Anchor.SHA256, source.Body.Data.SHA256)
	check := &artifactCommentMutationInput{ThreadID: out.ID.String(), IfMatch: "\"1\"", IdempotencyKey: "check-original", Body: ArtifactCommentMutation{EvidenceID: evidence.ID}}
	current, err := f.m.artifactCommentReanchorHTTP(f.human, check)
	require.NoError(t, err)
	require.Equal(t, "CURRENT", current.Body.Data.AnchorStatus)
	replay, err := f.m.artifactCommentReanchorHTTP(f.human, check)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	check.IdempotencyKey = "stale-check"
	_, err = f.m.artifactCommentReanchorHTTP(f.human, check)
	require.Error(t, err)
	_, err = f.m.artifactCommentSourceHTTP(f.human, &artifactCommentSourceInput{ExecutionInput: ExecutionInput{ExecutionID: v.ID.String()}, EvidenceID: "1"})
	require.Error(t, err)
	f.authorized = false
	_, err = f.m.artifactCommentSourceHTTP(f.human, &artifactCommentSourceInput{ExecutionInput: ExecutionInput{ExecutionID: v.ID.String()}, EvidenceID: evidence.ID.String()})
	require.Error(t, err)
	f.authorized = true

	fileQuery := filesquery.Bind(f.db, "ex_")
	fileRow, err := fileQuery.Node.WithContext(t.Context()).Where(fileQuery.Node.ID.Eq(out.Anchor.FileID)).First()
	require.NoError(t, err)
	require.Equal(t, fileRow.Name, out.Anchor.Path)
	require.True(t, strings.HasSuffix(out.Anchor.Path, "-source.go"))
	require.Contains(t, out.Anchor.VersionRef, "file-version:")
	require.NotContains(t, out.Anchor.VersionRef, "commit")
	require.Equal(t, "INTERNAL", out.Visibility)
	require.Positive(t, out.Anchor.VersionID)
	var frozen model.ArtifactCommentThread
	require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Where("id = ?", out.ID).First(&frozen).Error)
	require.Contains(t, frozen.AnchorSnapshot, out.Anchor.SHA256)
	agentCtx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 22, SubjectType: authn.SubjectTypePrincipal, EntityID: 9})
	f.m.agents = fleet.AgentDirectory{ByPrincipalOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 220, TenantID: 7, PrincipalID: 22, State: "ACTIVE"}, true, nil
	}}
	f.m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, id == 11, nil
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		source, e := f.m.artifactCommentSourceOn(agentCtx, tx, 7, out.ID)
		require.Equal(t, out.Anchor, source.Anchor)
		return e
	}))
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.humanAuthority(agentCtx, tx, 7, 9, "execution_read") }), ErrForbidden)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.CreateArtifactCommentOn(agentCtx, tx, 7, v.ID, ArtifactCommentCreate{SeenSeq: new(int64), EvidenceID: evidence.ID, StartLine: 1, EndLine: 1, Body: "Agent source comment"})
		return e
	}))
	f.authorized = false
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.artifactCommentSourceOn(agentCtx, tx, 7, out.ID); return e }), ErrForbidden)
}

func TestArtifactCodeAnchorRejectsWrongIdentityAndBounds(t *testing.T) {
	data := "one\ntwo\n"
	fact := ArtifactFact{BindingID: 1, FileID: 2, VersionID: 3, SHA256: hash([]byte(data))}
	for _, bad := range []string{"hash", "file", "version", "binding", "bytes", "lines", "binary"} {
		t.Run(bad, func(t *testing.T) {
			pin := files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 1, FileID: 2, VersionID: 3, SHA256: fact.SHA256, Name: "a.go"}}
			payload := data
			start, end := 1, 2
			switch bad {
			case "hash":
				pin.SHA256 = strings.Repeat("a", 64)
			case "file":
				pin.FileID = 4
			case "version":
				pin.VersionID = 4
			case "binding":
				pin.BindingID = 4
			case "bytes":
				payload = "altered"
			case "lines":
				end = 100
			case "binary":
				payload = "\x00"
			}
			pin.Reader = io.NopCloser(strings.NewReader(payload))
			_, err := artifactCodeAnchor(pin, fact, start, end)
			require.Error(t, err)
		})
	}
}

func TestArtifactCommentSourceMarkRollbackAndTargetKindUniqueness(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	f.m.commentMembers = commentMemberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	row := model.ArtifactCommentThread{ID: 900, TenantID: 7, ProjectID: 9, ExecutionID: v.ID, WorkItemID: validCreate().WorkItemID, EvidenceID: 901, Visibility: "INTERNAL", State: "OPEN", AnchorSnapshot: `{"kind":"CODE_LINES","version_id":"3"}`, FirstBody: "Issue", CreatedBy: 11, Version: 1}
	require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Create(&row).Error)
	fail := errors.New("rollback")
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, f.m.markArtifactCommentConvertedOn(f.human, tx, 7, 900, "DEFECT", 600, 11))
		return fail
	}), fail)
	var count int64
	require.NoError(t, f.db.Table(f.m.names.CommentConversions.String()).Count(&count).Error)
	require.Zero(t, count)
	for _, kind := range []string{"FEEDBACK", "DEFECT"} {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.markArtifactCommentConvertedOn(f.human, tx, 7, 900, kind, 600, 11) }))
	}
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.markArtifactCommentConvertedOn(f.human, tx, 7, 900, "DEFECT", 601, 11)
	}))
	require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Where("id = ?", 900).First(&row).Error)
	require.Equal(t, "RESOLVED", row.State)
}

func TestArtifactCommentFeedbackPlanSnapshotIsServerOwnedAndFailureIsAtomic(t *testing.T) {
	f := executionHarness(t)
	const canonical = `{"feedback":[{"id":"80","comment_thread_id":"90","location":"src/a.go:2","detail":"repair actual failure"}],"omitted_count":0}`
	f.m.commentFeedback = func(_ context.Context, tx *gorm.DB, tenant, project, work, actor guid.ID) (string, error) {
		require.NotNil(t, tx)
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(9), project)
		require.Equal(t, guid.ID(3), work)
		require.Equal(t, guid.ID(11), actor)
		return canonical, nil
	}
	in := validCreate()
	in.Plan.CommentFeedback = "forged caller context"
	var out ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; out, e = f.m.createOn(f.human, tx, 7, in); return e }))
	require.Equal(t, canonical, out.Plan.CommentFeedback) //nolint:testifylint // Serialized bytes are frozen by this regression contract.
	f2 := executionHarness(t)
	f2.m.commentFeedback = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (string, error) {
		return "", errors.New("feedback source unavailable")
	}
	err := f2.db.Transaction(func(tx *gorm.DB) error { _, e := f2.m.createOn(f2.human, tx, 7, validCreate()); return e })
	require.ErrorContains(t, err, "feedback source unavailable")
	var count int64
	require.NoError(t, f2.db.Table(f2.m.names.Executions.String()).Count(&count).Error)
	require.Zero(t, count)
}

func TestArtifactCommentsCreateEveryMVPAnchorFromRealPinnedEvidence(t *testing.T) {
	for _, tc := range []struct{ kind, name, data, excerpt string }{
		{"CODE_LINES", "source.go", "first\nsecond\n", "second"},
		{"TABLE_ROW", "report.csv", "name,result\nBuild,failed\n", "[\"Build\",\"failed\"]"},
		{"TEXT_PARAGRAPH", "source.txt", "First paragraph\n\nSecond paragraph", "First paragraph"},
		{"HTML_REGION", "preview.html", "<html><body><p>Rejected region</p></body></html>", "Rejected region"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			f := verifierRegistryFixture(t)
			f.human = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 11, EntityID: 9, SubjectType: authn.SubjectTypePrincipal})
			_, pins, _, _ := bindRealArtifactFiles(t, f, true)
			f.m.commentFiles = pins
			f.m.commentMembers = commentMemberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
			f.m.commentActivities = delivery.WorkItemActivityRecorder{RecordOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.WorkActivityInput) (int64, error) {
				return 1, nil
			}}
			v := f.create(t)
			f.pull(t, v.ID)
			_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, err)
			_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: "Source", Upload: &ArtifactUpload{Name: tc.name, Data: []byte(tc.data), Coverage: "COVERED"}})
			require.NoError(t, err)
			q := query.BindEvidence(f.db, f.m.names)
			evidence, err := q.WithContext(t.Context()).Where(q.ExecutionID.Eq(v.ID), q.Kind.Eq("FILE")).First()
			require.NoError(t, err)
			index := 0
			in := ArtifactCommentCreate{EvidenceID: evidence.ID, AnchorKind: tc.kind, StartLine: 2, EndLine: 2, RowIndex: &index, ParagraphIndex: &index, Body: strings.Repeat("x", 8000)}
			if tc.kind == "HTML_REGION" {
				in.TextQuote = tc.excerpt
				in.Rect = &ClientAnchorRect{W: 100, H: 30}
				in.Viewport = &ClientAnchorViewport{W: 800, H: 600, DPR: 1, Device: "DESKTOP"}
				in.Scroll = &ClientAnchorPoint{}
			}
			var out ArtifactCommentView
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				out, e = f.m.CreateArtifactCommentOn(f.human, tx, 7, v.ID, in)
				return e
			}))
			require.Equal(t, tc.kind, out.Anchor.Kind)
			require.Equal(t, tc.excerpt, out.Anchor.Excerpt)
			require.Positive(t, out.Anchor.VersionID)
			require.Equal(t, hash([]byte(tc.data)), out.Anchor.SHA256)
			if tc.kind != "CODE_LINES" {
				require.NotEmpty(t, out.Anchor.Locator)
			}
		})
	}
}

func pinnedCommentFixture(t *testing.T) (*executionFixture, guid.ID, guid.ID) {
	t.Helper()
	f := verifierRegistryFixture(t)
	f.human = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 11, EntityID: 9, SubjectType: authn.SubjectTypePrincipal})
	_, pins, _, _ := bindRealArtifactFiles(t, f, true)
	f.m.commentFiles = pins
	f.m.commentMembers = commentMemberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	f.m.commentActivities = delivery.WorkItemActivityRecorder{RecordOn: func(_ context.Context, tx *gorm.DB, _, _ guid.ID, _ delivery.WorkActivityInput) (int64, error) {
		return 1, tx.Exec("INSERT INTO effects(kind) VALUES ('comment.activity')").Error
	}}
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: "Observed source", Upload: &ArtifactUpload{Name: "source.go", Data: []byte("first\nsecond\n"), Coverage: "COVERED"}})
	require.NoError(t, err)
	q := query.BindEvidence(f.db, f.m.names)
	e, err := q.WithContext(t.Context()).Where(q.ExecutionID.Eq(v.ID), q.Kind.Eq("FILE")).First()
	require.NoError(t, err)
	return f, v.ID, e.ID
}

func TestArtifactCommentEveryStorageFailurePreservesPinnedThreadAndActivity(t *testing.T) {
	for _, command := range []string{"apply"} {
		for _, kind := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				f, execution, evidence := pinnedCommentFixture(t)
				in := ArtifactCommentCreate{EvidenceID: evidence, StartLine: 2, EndLine: 2, Body: "Review observed source"}
				snapshot := func() map[string][]map[string]any {
					out := map[string][]map[string]any{}
					for _, table := range []string{f.m.names.Executions.String(), f.m.names.Receipts.String(), f.m.names.Logs.String(), f.m.names.Evidence.String(), f.m.names.Approvals.String(), f.m.names.CommentThreads.String(), f.m.names.CommentMessages.String(), f.m.names.CommentEvents.String(), "slots", "effects"} {
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
				err := f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CreateArtifactCommentOn(f.human, tx, 7, execution, in); return e })
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

func TestArtifactCommentCurrentAuthorityAndSourceFailuresNeverPublishThread(t *testing.T) {
	for _, fault := range []string{"policy_missing", "policy_source", "policy_denied", "members_missing", "members_source", "membership_revoked", "human_missing", "human_source", "agent_missing", "agent_source", "agent_retired", "lock", "files_missing", "activity_missing", "activity_source", "id_error", "id_zero", "blank_body", "oversized_body", "missing_evidence", "wrong_evidence"} {
		t.Run(fault, func(t *testing.T) {
			f, execution, evidence := pinnedCommentFixture(t)
			in := ArtifactCommentCreate{EvidenceID: evidence, StartLine: 2, EndLine: 2, Body: "Review actual source"}
			failure := errors.New("comment current source unavailable")
			var effectsBefore int64
			require.NoError(t, f.db.Table("effects").Count(&effectsBefore).Error)
			switch fault {
			case "policy_missing":
				f.m.policy = nil
			case "policy_source":
				f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			case "policy_denied":
				f.authorized = false
			case "members_missing":
				f.m.commentMembers = nil
			case "members_source":
				f.m.commentMembers = commentMemberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, failure })
			case "membership_revoked":
				f.m.commentMembers = commentMemberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
			case "human_missing":
				f.m.principals.ActiveHumanByID = nil
			case "human_source":
				f.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, failure
				}
			case "agent_missing", "agent_source", "agent_retired":
				f.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
				f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					if fault == "agent_source" {
						return fleet.AgentIdentity{}, false, failure
					}
					return fleet.AgentIdentity{TenantID: 7, PrincipalID: 11, State: "RETIRED"}, fault != "agent_missing", nil
				}
			case "lock":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "files_missing":
				f.m.commentFiles = nil
			case "activity_missing":
				f.m.commentActivities.RecordOn = nil
			case "activity_source":
				f.m.commentActivities.RecordOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.WorkActivityInput) (int64, error) {
					return 0, failure
				}
			case "id_error":
				f.m.ids = failedExecutionIDs{err: failure}
			case "id_zero":
				f.m.ids = failedExecutionIDs{}
			case "blank_body":
				in.Body = " "
			case "oversized_body":
				in.Body = strings.Repeat("x", 8001)
			case "missing_evidence":
				in.EvidenceID = 0
			case "wrong_evidence":
				in.EvidenceID = 999
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
				out, e := f.m.CreateArtifactCommentOn(f.human, tx, 7, execution, in)
				require.Zero(t, out.ID)
				return e
			}))
			for _, table := range []string{f.m.names.CommentThreads.String(), f.m.names.CommentMessages.String(), f.m.names.CommentEvents.String()} {
				var n int64
				require.NoError(t, f.db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
			var effectsAfter int64
			require.NoError(t, f.db.Table("effects").Count(&effectsAfter).Error)
			require.Equal(t, effectsBefore, effectsAfter)
		})
	}
}

func TestInjectArtifactCommentsRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.injectArtifactComments(module.NewServices()), module.ErrServicesProviding)
}

func TestArtifactCommentConversionStorageFailuresPreserveSourceAndChronology(t *testing.T) {
	for _, command := range []string{"scope", "source", "convert"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f, _, id := replyFixture(t)
				return f, func(tx *gorm.DB) error {
					switch command {
					case "scope":
						out, e := f.m.artifactCommentScopeOn(f.human, tx, 7, id)
						if e == nil {
							require.EqualValues(t, 9, out.ProjectID)
						}
						return e
					case "source":
						out, e := f.m.artifactCommentSourceOn(f.human, tx, 7, id)
						if e == nil {
							require.Equal(t, id, out.ID)
						}
						return e
					default:
						return f.m.markArtifactCommentConvertedOn(f.human, tx, 7, id, "DEFECT", 888, 11)
					}
				}
			})
		})
	}
}
