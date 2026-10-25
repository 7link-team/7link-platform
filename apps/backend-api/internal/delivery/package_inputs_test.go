package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestPackageInputReadFailuresCannotProducePublishableFacts(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	_, e = h.check(result.ID, 12, "PASSED")
	require.NoError(t, e)
	_, e = h.accept(result.ID, 12)
	require.NoError(t, e)
	h.m.service.entities = core.EntityProvisioner{GetOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (core.Entity, error) {
		return core.Entity{ID: 99, TenantID: 1, Type: "project", Name: "Fixture"}, nil
	}}
	h.m.principals.ActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id, DisplayName: "Reviewer"}, true, nil
	}
	refs := []ClientArtifactRef{{WorkItemID: h.work, ResultID: result.ID, EvidenceID: result.Evidence[0].ID}}
	injected := errors.New("package inputs unavailable")
	reads, failAt := 0, 0
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("package-input-failure", func(tx *gorm.DB) {
		reads++
		if reads == failAt {
			tx.AddError(injected)
		}
	}))
	defer h.db.Callback().Query().Remove("package-input-failure")
	read := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.packageInputsOn(h.ctx, tx, 1, 99, refs); return e })
	}
	require.NoError(t, read())
	total := reads
	require.Greater(t, total, 10)
	for i := 1; i <= total; i++ {
		reads, failAt = 0, i
		require.Error(t, read(), "package input read %d", i)
	}
	failAt = 0
	for _, fault := range []string{"entity", "entity_missing", "wrong_entity", "principal_missing", "principal", "agent"} {
		t.Run(fault, func(t *testing.T) {
			get := h.m.service.entities.GetOn
			active := h.m.principals.ActiveRow
			agent := h.m.agents.ByPrincipalOn
			defer func() {
				h.m.service.entities.GetOn = get
				h.m.principals.ActiveRow = active
				h.m.agents.ByPrincipalOn = agent
			}()
			want := injected
			switch fault {
			case "entity":
				h.m.service.entities.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (core.Entity, error) { return core.Entity{}, injected }
			case "entity_missing":
				h.m.service.entities.GetOn = nil
				want = ErrUnavailable
			case "wrong_entity":
				h.m.service.entities.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (core.Entity, error) { return core.Entity{}, nil }
				want = ErrProjectNotFound
			case "principal_missing":
				h.m.principals.ActiveRow = nil
				want = ErrUnavailable
			case "principal":
				h.m.principals.ActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, injected
				}
			case "agent":
				h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, injected
				}
			}
			require.ErrorIs(t, read(), want)
		})
	}
}

func TestPackagePublicOverrideRetainsHumanRoleSupportAndFailedReceipt(t *testing.T) {
	evidence := []resultEvidenceView{{ID: 31, Name: "review.txt", VersionID: 41, SHA256: strings.Repeat("a", 64)}}
	out := clientVerificationOverrides([]verificationOverrideView{{ActorID: 22, Reason: "Known issue", Risk: "Follow up", EvidenceIDs: []guid.ID{31}, Original: []overriddenVerdict{{ID: 55, Verdict: "FAILED", ReceiptHash: strings.Repeat("b", 64)}}}}, evidence)
	require.Len(t, out, 1)
	require.Equal(t, "HUMAN", out[0].Actor.Kind)
	require.Len(t, out[0].SupportingEvidence, 1)
	require.Equal(t, "review.txt", out[0].SupportingEvidence[0].Name)
	require.Equal(t, guid.ID(41), out[0].SupportingEvidence[0].VersionID)
	require.Equal(t, "FAILED", out[0].Original[0].Verdict)
	raw, e := json.Marshal(out)
	require.NoError(t, e)
	require.NotContains(t, string(raw), "actor_id")
	require.NotContains(t, string(raw), `"id":`)
}

func TestPackageInputsFreezeCurrentAcceptedDecisionPolicyAndIdentityTerms(t *testing.T) {
	h := newResultFixture(t)
	h.m.service.entities = core.EntityProvisioner{GetOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (core.Entity, error) {
		return core.Entity{ID: 99, TenantID: 1, Type: "project", Name: "Delivery fixture project"}, nil
	}}
	result, e := h.submit()
	require.NoError(t, e)
	_, e = h.check(result.ID, 12, "PASSED")
	require.NoError(t, e)
	_, e = h.accept(result.ID, 12)
	require.NoError(t, e)
	h.m.principals.ActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id, DisplayName: "AI", LoginName: "é"}, true, nil
	}
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{Name: "CI"}, true, nil
	}
	refs := []ClientArtifactRef{{WorkItemID: h.work, ResultID: result.ID, EvidenceID: result.Evidence[0].ID}}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		facts, e := h.m.packageInputsOn(h.ctx, tx, 1, 99, refs)
		require.NoError(t, e)
		require.Len(t, facts.Items, 1)
		require.Positive(t, int64(facts.Items[0].AcceptedDecisionID))
		require.Contains(t, facts.RedactionTerms, "AI")
		require.Contains(t, facts.RedactionTerms, "é")
		require.Contains(t, facts.RedactionTerms, "CI")
		require.Equal(t, "Delivery fixture project", facts.Project.Name)
		require.Len(t, facts.WorkItems, 1)
		require.Len(t, facts.WorkItems[0].Evidence, 1)
		require.Equal(t, "PASSED", facts.WorkItems[0].Evidence[0].Verification[0].Result)
		return e
	}))
	q := query.BindWorkItems(h.db, h.m.service.names)
	_, e = q.WithContext(h.ctx).Where(q.ID.Eq(h.work)).UpdateSimple(q.BaselineStale.Value(true))
	require.NoError(t, e)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.packageInputsOn(h.ctx, tx, 1, 99, refs); return e }), ErrResultState)
}
