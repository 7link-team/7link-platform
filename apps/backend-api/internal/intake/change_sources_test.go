package intake

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func changeSourceHarness(t *testing.T) (*Module, *gorm.DB, context.Context, *bool, *briefTestPublisher) {
	m, db := referencesHarness(t)
	require.NoError(t, db.Exec("CREATE TABLE brief_probe(kind TEXT)").Error)
	at := time.Now().UTC()
	require.NoError(t, db.Table(m.names.Records.String()).Create(map[string]any{"id": 10, "tenant_id": 7, "ref_no": 1, "channel_id": 3, "channel_kind": "SHARED_CHANNEL", "contact_id": 1, "title": "Original", "raw_content_hash": strings.Repeat("a", 64), "received_at": at, "reply_token": "source-10", "status": "CONVERTED", "project_id": 20, "requirement_id": 100, "created_at": at, "updated_at": at}).Error)
	allowed := true
	pub := &briefTestPublisher{}
	m.brief = signedBriefServices{ids: &briefTestIDs{next: 900}, principals: identity.PrincipalStateStore{Lock: func(context.Context, *gorm.DB, guid.ID) error { return nil }, ActiveHumanByID: func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}}, explain: func(_ context.Context, _ *gorm.DB, in core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.Equal(t, "scope_change", in.Permission)
		return authz.Explanation{Allowed: allowed}, nil
	}, audit: func(_ context.Context, tx *gorm.DB, _ audit.DomainEvent) error {
		return tx.Exec("INSERT INTO brief_probe VALUES ('audit')").Error
	}, events: pub}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 2, SubjectType: authn.SubjectTypePrincipal})
	return m, db, ctx, &allowed, pub
}
func sourceDraft() ChangeSourceDraft {
	return ChangeSourceDraft{OriginalIntakeID: 10, RequirementID: 100, ChangeID: 55, ProposalHash: strings.Repeat("a", 64), ProposalJSON: `{"requirement_id":"100"}`, ImpactJSON: `{"breached":[]}`, Title: "Approved change"}
}

func TestIntakeLedgerKeepsOriginalSourceAndFrozenCardScoped(t *testing.T) {
	m, db, ctx, allowed, _ := changeSourceHarness(t)
	raw := "Original immutable request <script>untrusted</script>"
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": "NEW", "raw_content": raw}).Error)
	var frozen ChangeSourceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		frozen, e = m.draftChangeSourceOn(ctx, tx, 7, 20, sourceDraft())
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		first, e := m.listIntakeLedgerOn(ctx, tx, 7, 20, 0, 1)
		require.NoError(t, e)
		require.True(t, first.HasMore)
		require.Len(t, first.Items, 1)
		require.Equal(t, frozen.ID, first.Items[0].ID)
		last, e := m.listIntakeLedgerOn(ctx, tx, 7, 20, frozen.ID, 1)
		require.NoError(t, e)
		require.False(t, last.HasMore)
		require.Equal(t, guid.ID(10), last.Items[0].ID)
		source, e := m.readIntakeLedgerSourceOn(ctx, tx, 7, 20, 10)
		require.NoError(t, e)
		require.Equal(t, raw, *source.RawContent)
		require.Equal(t, strings.Repeat("a", 64), source.RawContentHash)
		require.Nil(t, source.Card, "NEW source must not invent an assessed card")
		card, e := m.readIntakeLedgerSourceOn(ctx, tx, 7, 20, frozen.ID)
		require.NoError(t, e)
		require.Equal(t, frozen, *card.Card)
		empty, e := m.listIntakeLedgerOn(ctx, tx, 7, 21, 0, 100)
		require.NoError(t, e)
		require.Empty(t, empty.Items)
		_, e = m.readIntakeLedgerSourceOn(ctx, tx, 7, 21, frozen.ID)
		require.ErrorIs(t, e, gorm.ErrRecordNotFound)
		_, e = m.readIntakeLedgerSourceOn(ctx, tx, 8, 20, frozen.ID)
		require.ErrorIs(t, e, ErrForbidden)
		return nil
	}))
	*allowed = false
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.listIntakeLedgerOn(ctx, tx, 7, 20, 0, 1)
		require.ErrorIs(t, e, ErrForbidden)
		out, e := m.readIntakeLedgerSourceOn(ctx, tx, 7, 20, frozen.ID)
		require.ErrorIs(t, e, ErrForbidden)
		require.Zero(t, out.ID)
		return nil
	}))
}

func TestIntakeLedgerRejectsUnboundedOrFailedReads(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	for _, limit := range []int{0, 101} {
		_, e := m.listIntakeLedgerOn(ctx, nil, 7, 20, 0, limit)
		require.ErrorIs(t, e, ErrInvalidArgument)
	}
	_, e := m.listIntakeLedgerOn(ctx, nil, 7, 20, -1, 1)
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = m.readIntakeLedgerSourceOn(ctx, nil, 7, 20, 0)
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = m.readIntakeLedgerSourceOn(ctx, nil, 7, 20, 10)
	require.ErrorIs(t, e, ErrInvalidArgument)
	failure := errors.New("source storage unavailable")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("ledger_failure", func(tx *gorm.DB) { _ = tx.AddError(failure) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.listIntakeLedgerOn(ctx, tx, 7, 20, 0, 1)
		require.ErrorIs(t, e, failure)
		require.Empty(t, page.Items)
		out, e := m.readIntakeLedgerSourceOn(ctx, tx, 7, 20, 10)
		require.ErrorIs(t, e, failure)
		require.Zero(t, out.ID)
		return nil
	}))
}

func TestIntakeLedgerMissingFrozenCardDoesNotLeakPartialSource(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	var source ChangeSourceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		source, e = m.draftChangeSourceOn(ctx, tx, 7, 20, sourceDraft())
		return e
	}))
	require.NoError(t, db.Table(m.names.TriageCards.String()).Where("intake_id=?", source.ID).Delete(&model.TriageCard{}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.readIntakeLedgerSourceOn(ctx, tx, 7, 20, source.ID)
		require.ErrorIs(t, e, gorm.ErrRecordNotFound)
		require.Nil(t, out.RawContent)
		require.Nil(t, out.Card)
		require.Zero(t, out.ID)
		return nil
	}))
}

type sourceTestIDs struct{ calls, failAt, zeroAt int }

func (s *sourceTestIDs) Next() (int64, error) {
	s.calls++
	if s.calls == s.failAt {
		return 0, errors.New("identifier allocator unavailable")
	}
	if s.calls == s.zeroAt {
		return 0, nil
	}
	return int64(900 + s.calls), nil
}

func TestSourceAllocationFailuresLeaveNoSourceOrCard(t *testing.T) {
	for _, scenario := range []string{"change_first_id", "change_card_id", "change_zero_id", "rework_id", "rework_zero_id"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, _ := changeSourceHarness(t)
			ids := &sourceTestIDs{failAt: 1}
			if scenario == "change_card_id" {
				ids.failAt = 2
			}
			if strings.Contains(scenario, "zero") {
				ids.failAt = 0
				ids.zeroAt = 1
			}
			m.brief.ids = ids
			require.NoError(t, db.Table(m.names.Contacts.String()).Where("id=1").Update("principal_id", 4).Error)
			e := db.Transaction(func(tx *gorm.DB) error {
				if strings.HasPrefix(scenario, "rework") {
					_, _, e := m.createReworkSourceOn(ctx, tx, 7, 20, ReworkDraft{PackageID: 70, ReceiptID: 80, RecipientID: 4, ContentHash: strings.Repeat("a", 64), Items: []ReworkItem{{ItemID: strings.Repeat("b", 64), WorkItemID: 11, ResultID: 20, EvidenceID: 30, RequirementID: 100, SourceIntakeID: 10, FeedbackCategory: "OTHER", Comment: "Detailed rejection feedback"}}})
					return e
				}
				_, e := m.draftChangeSourceOn(ctx, tx, 7, 20, sourceDraft())
				return e
			})
			require.Error(t, e)
			var count int64
			require.NoError(t, db.Table(m.names.Records.String()).Where("kind IN ?", []string{"CHANGE", "REWORK"}).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, db.Table(m.names.TriageCards.String()).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, db.Table("brief_probe").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestReworkReaderRejectsCorruptRecordsAndInvalidTransactions(t *testing.T) {
	for _, scenario := range []string{"nil_transaction", "missing_origin", "missing_content", "corrupt_content", "query_failure"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, _ := changeSourceHarness(t)
			if scenario == "nil_transaction" {
				_, _, e := m.readReworkSourceOn(ctx, nil, 7, 20, 80)
				require.ErrorIs(t, e, ErrInvalidArgument)
				return
			}
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": "REWORK", "origin_delivery_id": 70, "origin_acceptance_id": 80, "raw_content": "{}"}).Error)
			switch scenario {
			case "missing_origin":
				require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Update("origin_delivery_id", nil).Error)
			case "missing_content":
				require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Update("raw_content", nil).Error)
			case "corrupt_content":
				require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Update("raw_content", "{").Error)
			case "query_failure":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("rework_read_failure", func(tx *gorm.DB) { _ = tx.AddError(errors.New("read unavailable")) }))
			}
			e := db.Transaction(func(tx *gorm.DB) error {
				out, found, e := m.readReworkSourceOn(ctx, tx, 7, 20, 80)
				require.False(t, found)
				require.Zero(t, out.ID, "failed read cannot return a usable partial source")
				return e
			})
			require.Error(t, e)
		})
	}
}

func TestChangeSourceCardAndConversionDatabaseFailuresPreserveFrozenState(t *testing.T) {
	for _, scenario := range []string{"invalid_id", "missing_card", "card_query_failure", "missing_binding", "conversion_read_failure", "conversion_update_failure"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, _ := changeSourceHarness(t)
			var source ChangeSourceView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				source, e = m.draftChangeSourceOn(ctx, tx, 7, 20, sourceDraft())
				return e
			}))
			failure := errors.New("database unavailable")
			switch scenario {
			case "missing_card":
				require.NoError(t, db.Table(m.names.TriageCards.String()).Where("intake_id=?", source.ID).Delete(&model.TriageCard{}).Error)
			case "card_query_failure":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("card_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.TriageCards.String() {
						_ = tx.AddError(failure)
					}
				}))
			case "missing_binding":
				// Model a corrupt legacy read without bypassing the live schema constraint.
				require.NoError(t, db.Callback().Query().After("gorm:query").Register("missing_binding", func(tx *gorm.DB) {
					if row, ok := tx.Statement.Dest.(*model.Record); ok && row.ID == source.ID {
						row.TargetRequirementID = nil
					}
				}))
			case "conversion_update_failure":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("conversion_failure", func(tx *gorm.DB) { _ = tx.AddError(failure) }))
			}
			e := db.Transaction(func(tx *gorm.DB) error {
				if scenario == "conversion_read_failure" {
					return m.convertChangeSourceOn(ctx, tx, 7, 20, 999, 1, 2, 55)
				}
				if scenario == "conversion_update_failure" {
					return m.convertChangeSourceOn(ctx, tx, 7, 20, source.ID, 1, 2, 55)
				}
				id := source.ID
				if scenario == "invalid_id" {
					id = 0
				}
				_, e := m.readChangeSourceOn(ctx, tx, 7, 20, id)
				return e
			})
			require.Error(t, e)
			if scenario == "card_query_failure" || scenario == "conversion_update_failure" {
				require.ErrorIs(t, e, failure)
			}
			var state struct {
				Status  string
				Version int
			}
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=?", source.ID).Scan(&state).Error)
			require.Equal(t, "TRIAGED", state.Status)
			require.Equal(t, 1, state.Version)
		})
	}
}

func TestReworkSourceRequiresGenuineCompleteOriginsAndFreezesReceipt(t *testing.T) {
	m, db, ctx, _, pub := changeSourceHarness(t)
	require.NoError(t, db.Table(m.names.Contacts.String()).Where("id=1").Update("principal_id", 4).Error)
	in := ReworkDraft{PackageID: 70, ReceiptID: 80, RecipientID: 4, ContentHash: strings.Repeat("a", 64), Items: []ReworkItem{{ItemID: strings.Repeat("b", 64), WorkItemID: 11, ResultID: 20, EvidenceID: 30, RequirementID: 100, SourceIntakeID: 10, FeedbackCategory: "FUNCTIONAL_DEFECT", Comment: "The delivered file is defective"}}}
	create := func(input ReworkDraft) (ReworkSourceView, bool, error) {
		var out ReworkSourceView
		var available bool
		e := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, available, e = m.createReworkSourceOn(ctx, tx, 7, 20, input)
			return e
		})
		return out, available, e
	}
	for _, mutate := range []func(*ReworkDraft){
		func(v *ReworkDraft) { v.Items[0].SourceIntakeID = 999 },
		func(v *ReworkDraft) { v.Items[0].RequirementID = 999 },
		func(v *ReworkDraft) { v.Items[0].SourceIntakeID = 0 },
		func(v *ReworkDraft) { v.RecipientID = 5 },
		func(v *ReworkDraft) { v.Items[0].Comment = strings.Repeat("x", 65536) },
	} {
		missing := in
		missing.Items = append([]ReworkItem(nil), in.Items...)
		mutate(&missing)
		_, available, e := create(missing)
		require.NoError(t, e)
		require.False(t, available)
	}
	pub.fail = true
	_, _, e := create(in)
	require.Error(t, e)
	var count int64
	require.NoError(t, db.Table(m.names.Records.String()).Where("kind='REWORK'").Count(&count).Error)
	require.Zero(t, count)
	pub.fail = false
	first, available, e := create(in)
	require.NoError(t, e)
	require.True(t, available)
	require.Equal(t, "RECEIVED", first.State)
	require.Equal(t, in, first.Rejection)
	again, available, e := create(in)
	require.NoError(t, e)
	require.True(t, available)
	require.Equal(t, first, again)
	in.Items[0].Comment = "Different immutable rejection detail"
	_, _, e = create(in)
	require.ErrorIs(t, e, ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, e := m.readReworkSourceOn(ctx, tx, 7, 21, 80)
		require.NoError(t, e)
		require.False(t, found)
		_, found, e = m.readReworkSourceOn(ctx, tx, 8, 20, 80)
		require.NoError(t, e)
		require.False(t, found)
		ref, found, e := m.GetOn(ctx, tx, 7, first.ID)
		require.NoError(t, e)
		require.True(t, found)
		require.Equal(t, "REWORK", ref.Kind)
		require.EqualValues(t, 80, *ref.OriginAcceptanceID)
		return nil
	}))
	_, _, e = m.createReworkSourceOn(ctx, db, 7, 20, in)
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, _, e = m.readReworkSourceOn(ctx, db, 7, 20, 80)
	require.ErrorIs(t, e, ErrInvalidArgument)
}

func TestReworkSourceFailuresNeverCreatePartialRecords(t *testing.T) {
	for _, scenario := range []string{"invalid-receipt", "invalid-item", "duplicate-item", "no-events", "blocked-contact", "source-query-failure", "contact-query-failure", "audit-failure", "insert-failure", "no-tenant-source"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, _ := changeSourceHarness(t)
			require.NoError(t, db.Table(m.names.Contacts.String()).Where("id=1").Update("principal_id", 4).Error)
			in := ReworkDraft{PackageID: 70, ReceiptID: 80, RecipientID: 4, ContentHash: strings.Repeat("a", 64), Items: []ReworkItem{{ItemID: strings.Repeat("b", 64), WorkItemID: 11, ResultID: 20, EvidenceID: 30, RequirementID: 100, SourceIntakeID: 10, FeedbackCategory: "OTHER", Comment: "Detailed rejection comment"}}}
			tenant := guid.ID(7)
			switch scenario {
			case "invalid-receipt":
				in.ReceiptID = 0
			case "invalid-item":
				in.Items[0].EvidenceID = 0
			case "duplicate-item":
				in.Items = append(in.Items, in.Items[0])
			case "no-events":
				m.brief.events = nil
			case "blocked-contact":
				require.NoError(t, db.Table(m.names.Contacts.String()).Where("id=1").Update("state", "BLOCKED").Error)
			case "no-tenant-source":
				tenant = 999
			case "source-query-failure", "contact-query-failure":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("rework-query-failure", func(tx *gorm.DB) {
					if scenario == "source-query-failure" || tx.Statement.Table == m.names.Contacts.String() {
						_ = tx.AddError(errors.New("database unavailable"))
					}
				}))
			case "audit-failure":
				m.brief.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			case "insert-failure":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("rework-insert-failure", func(tx *gorm.DB) { _ = tx.AddError(errors.New("insert unavailable")) }))
			}
			e := db.Transaction(func(tx *gorm.DB) error {
				_, available, e := m.createReworkSourceOn(ctx, tx, tenant, 20, in)
				if scenario == "blocked-contact" || scenario == "no-tenant-source" {
					require.NoError(t, e)
					require.False(t, available)
				} else {
					require.Error(t, e)
				}
				return e
			})
			if scenario == "blocked-contact" || scenario == "no-tenant-source" {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
			if scenario != "source-query-failure" && scenario != "contact-query-failure" {
				var count int64
				require.NoError(t, db.Table(m.names.Records.String()).Where("kind='REWORK'").Count(&count).Error)
				require.Zero(t, count)
			}
		})
	}
}

func TestIntakeReferenceAllocationIsTenantWideAndTransactional(t *testing.T) {
	m, db, ctx, _, pub := changeSourceHarness(t)
	at := time.Now().UTC()
	require.NoError(t, db.Table(m.names.Records.String()).Create(map[string]any{"id": 11, "tenant_id": 7, "ref_no": 25, "channel_id": 3, "channel_kind": "SHARED_CHANNEL", "contact_id": 1, "title": "Other project original", "raw_content_hash": strings.Repeat("b", 64), "received_at": at, "reply_token": "source-11", "status": "CONVERTED", "project_id": 21, "requirement_id": 101, "created_at": at, "updated_at": at}).Error)
	var fenced bool
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("tenant_reference_fence", func(tx *gorm.DB) {
		if tx.Statement.Table != m.names.Records.String() {
			return
		}
		if strings.Contains(tx.Statement.SQL.String(), "ORDER BY `"+m.names.Records.String()+"`.`id`") {
			_, fenced = tx.Statement.Clauses["FOR"]
		} else if _, ok := tx.Statement.Clauses["FOR"]; ok {
			require.True(t, fenced, "tenant anchor must lock before source/max rows")
		}
	}))
	draft := func(project guid.ID, in ChangeSourceDraft) error {
		fenced = false
		return db.Transaction(func(tx *gorm.DB) error { _, e := m.draftChangeSourceOn(ctx, tx, 7, project, in); return e })
	}
	pub.fail = true
	require.Error(t, draft(20, sourceDraft()))
	pub.fail = false
	require.NoError(t, draft(20, sourceDraft()))
	in := sourceDraft()
	in.OriginalIntakeID, in.RequirementID, in.ChangeID = 11, 101, 56
	require.NoError(t, draft(21, in))
	var refs []int
	require.NoError(t, db.Table(m.names.Records.String()).Where("kind = ?", "CHANGE").Order("ref_no").Pluck("ref_no", &refs).Error)
	require.Equal(t, []int{26, 27}, refs, "cross-project sources share numbering; rolled back allocation leaves no row")
}

func TestIntakeChangeSourceIsGenuineImmutableAndAtomic(t *testing.T) {
	m, db, ctx, allowed, pub := changeSourceHarness(t)
	in := sourceDraft()
	draft := func() (ChangeSourceView, error) {
		var out ChangeSourceView
		e := db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.draftChangeSourceOn(ctx, tx, 7, 20, in); return e })
		return out, e
	}
	first, e := draft()
	require.NoError(t, e)
	require.Equal(t, "TRIAGED", first.State)
	require.Equal(t, 1, first.CardRevision)
	second, e := draft()
	require.NoError(t, e)
	require.Equal(t, first, second)
	in.ProposalHash = strings.Repeat("b", 64)
	_, e = draft()
	require.ErrorIs(t, e, ErrConflict)
	in = sourceDraft()
	*allowed = false
	_, e = draft()
	require.ErrorIs(t, e, ErrForbidden)
	*allowed = true
	pub.fail = true
	in.ChangeID = 56
	_, e = draft()
	require.Error(t, e)
	pub.fail = false
	var n int64
	require.NoError(t, db.Table(m.names.TriageCards.String()).Count(&n).Error)
	require.EqualValues(t, 1, n)
	convert := func(version int) error {
		return db.Transaction(func(tx *gorm.DB) error { return m.convertChangeSourceOn(ctx, tx, 7, 20, first.ID, version, 2, 88) })
	}
	require.ErrorIs(t, convert(2), ErrConflict)
	pub.fail = true
	require.Error(t, convert(1))
	pub.fail = false
	var row ChangeSourceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		row, e = m.readChangeSourceOn(ctx, tx, 7, 20, first.ID)
		return e
	}))
	require.Equal(t, first, row)
	require.NoError(t, convert(1))
	require.ErrorIs(t, convert(1), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		r, e := m.readChangeSourceOn(ctx, tx, 7, 20, first.ID)
		require.Equal(t, "CONVERTED", r.State)
		require.Equal(t, 2, *r.ResultVersionNo)
		return e
	}))
	require.NoError(t, db.Table("brief_probe").Count(&n).Error)
	require.EqualValues(t, 4, n, "only committed triage+conversion audit/outbox pairs")
}

func TestIntakeSourceRejectsInvalidAndForeignSources(t *testing.T) {
	for _, scenario := range []string{"missing_origin", "wrong_requirement", "wrong_project", "tenant", "invalid_hash", "invalid_json", "oversize", "nil_tx", "plain_db", "nonhuman", "no_ids", "no_events", "query_failure", "audit_failure"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, _ := changeSourceHarness(t)
			in := sourceDraft()
			tenant, project := guid.ID(7), guid.ID(20)
			switch scenario {
			case "missing_origin":
				in.OriginalIntakeID = 11
			case "wrong_requirement":
				in.RequirementID = 101
			case "wrong_project":
				project = 21
			case "tenant":
				tenant = 8
			case "invalid_hash":
				in.ProposalHash = strings.Repeat("z", 64)
			case "invalid_json":
				in.ProposalJSON = "bad"
			case "oversize":
				in.Title = strings.Repeat("x", 201)
			case "nonhuman":
				m.brief.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "no_ids":
				m.brief.ids = nil
			case "no_events":
				m.brief.events = nil
			case "query_failure":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("source_query", func(tx *gorm.DB) { _ = tx.AddError(errors.New("query failed")) }))
			case "audit_failure":
				m.brief.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit failed") }
			}
			e := db.Transaction(func(tx *gorm.DB) error {
				if scenario == "nil_tx" {
					tx = nil
				}
				if scenario == "plain_db" {
					tx = db
				}
				_, e := m.draftChangeSourceOn(ctx, tx, tenant, project, in)
				return e
			})
			require.Error(t, e)
			if scenario != "query_failure" {
				var n int64
				require.NoError(t, db.Table(m.names.TriageCards.String()).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
}

func TestIntakeSourceAuthorityAndCardFailuresDoNotWrite(t *testing.T) {
	for _, scenario := range []string{"lock", "principal", "authorization", "missing_lock", "missing_explain", "actor", "project", "missing_card", "missing_binding", "invalid_conversion"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, _ := changeSourceHarness(t)
			failure := errors.New("authority failed")
			project := guid.ID(20)
			switch scenario {
			case "lock":
				m.brief.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "principal":
				m.brief.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, failure
				}
			case "authorization":
				m.brief.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, failure
				}
			case "missing_lock":
				m.brief.principals.Lock = nil
			case "missing_explain":
				m.brief.explain = nil
			case "actor":
				ctx = t.Context()
			case "project":
				project = 0
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				if scenario == "invalid_conversion" {
					return m.convertChangeSourceOn(ctx, tx, 7, project, 10, 1, 1, 0)
				}
				if scenario == "missing_card" {
					_, e := m.readChangeSourceOn(ctx, tx, 7, project, 999)
					return e
				}
				if scenario == "missing_binding" {
					if e := tx.Table(m.names.Records.String()).Where("id = ?", 10).Update("kind", "REWORK").Error; e != nil {
						return e
					}
					_, e := m.readChangeSourceOn(ctx, tx, 7, project, 10)
					return e
				}
				_, e := m.draftChangeSourceOn(ctx, tx, 7, project, sourceDraft())
				return e
			})
			require.Error(t, err)
			if scenario == "lock" || scenario == "principal" || scenario == "authorization" {
				require.ErrorIs(t, err, failure)
			}
			var n int64
			require.NoError(t, db.Table(m.names.TriageCards.String()).Count(&n).Error)
			require.Zero(t, n)
		})
	}
}
