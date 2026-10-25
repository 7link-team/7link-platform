package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

type upstreamFiles struct {
	*resultFileFixture
	hash        string
	unavailable bool
}

func (f upstreamFiles) InspectOn(ctx context.Context, tx *gorm.DB, id guid.ID) (files.PinnedCandidate, error) {
	if f.unavailable {
		return files.PinnedCandidate{}, errors.New("source unavailable")
	}
	p, e := f.resultFileFixture.InspectOn(ctx, tx, id)
	p.SHA256 = f.hash
	return p, e
}
func TestUpstreamArtifactSnapshotReconcilePropagationAndRollback(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(result.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(result.ID, 13)
	require.NoError(t, err)
	_, _, _, body := workHTTPHarness(t)
	var consumer, descendant workDetailView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		consumer, e = h.m.createWorkOn(h.ctx, tx, 1, 99, 11, body)
		if e != nil {
			return e
		}
		descendant, e = h.m.createWorkOn(h.ctx, tx, 1, 99, 11, body)
		return e
	}))
	d := query.BindDependencies(h.db, h.m.service.names)
	require.NoError(t, d.WithContext(h.ctx).Create(&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: consumer.ID, DependsOnID: h.work}))
	require.NoError(t, d.WithContext(h.ctx).Create(&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: descendant.ID, DependsOnID: consumer.ID}))
	digest := sha256.Sum256([]byte("exact"))
	hash := hex.EncodeToString(digest[:])
	e := query.BindResultEvidence(h.db, h.m.service.names)
	evidence, err := e.WithContext(h.ctx).Where(e.ResultID.Eq(result.ID)).First()
	require.NoError(t, err)
	_, err = e.WithContext(h.ctx).Where(e.ID.Eq(evidence.ID)).Update(e.SHA256, hash)
	require.NoError(t, err)
	h.m.resultFiles = upstreamFiles{h.files, hash, false}
	input := consumptionBody{Version: consumer.Version, AcceptedArtifactID: evidence.ID}
	var row model.ArtifactConsumption
	consume := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			row, e = h.m.consumeArtifactOn(h.ctx, tx, 1, 99, consumer.ID, 11, 11, input)
			return e
		})
	}
	require.NoError(t, consume())
	first := row.ID
	require.NoError(t, consume())
	require.Equal(t, first, row.ID, "same accepted artifact deduplicates")
	require.Equal(t, hash, row.SHA256)
	require.Equal(t, evidence.VersionID, row.VersionID)
	reconcile := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error { return h.m.reconcileConsumptionOn(h.ctx, tx, &row, 11, "HUMAN") })
	}
	h.m.resultFiles = upstreamFiles{h.files, hash, false}
	require.NoError(t, reconcile())
	var current model.ArtifactConsumption
	load := func() {
		require.NoError(t, query.BindArtifactConsumptions(h.db, h.m.service.names).Where("id = ?", first).First(&current).Error)
	}
	load()
	require.Equal(t, "CURRENT", current.State)
	var probes int64
	require.NoError(t, h.db.Table("provision_probe").Count(&probes).Error)
	require.NoError(t, reconcile())
	var after int64
	require.NoError(t, h.db.Table("provision_probe").Count(&after).Error)
	require.Equal(t, probes, after, "same hash has no duplicate audit or outbox")
	h.m.resultFiles = upstreamFiles{h.files, "different", false}
	h.fail = "outbox"
	require.Error(t, reconcile())
	load()
	require.Equal(t, "CURRENT", current.State, "failed outbox rolls back applicability")
	h.fail = ""
	require.NoError(t, reconcile())
	load()
	require.Equal(t, "STALE", current.State)
	require.Equal(t, hash, current.SHA256, "consumed hash immutable")
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		w := query.BindWorkItems(tx, h.m.service.names)
		r, e := w.WithContext(h.ctx).Where(w.ID.Eq(descendant.ID)).First()
		require.NoError(t, e)
		require.ErrorIs(t, h.m.requireCurrentUpstreamOn(h.ctx, tx, r), ErrUpstreamStale)
		return nil
	}))
	h.m.resultFiles = upstreamFiles{h.files, hash, true}
	require.NoError(t, reconcile())
	load()
	require.Equal(t, "INCONCLUSIVE", current.State)
	h.m.resultFiles = upstreamFiles{h.files, hash, false}
	require.NoError(t, reconcile())
	load()
	require.Equal(t, "CURRENT", current.State)
}

func TestUpstreamReplacementPreservesOriginalAndRequiresCurrentEvidence(t *testing.T) {
	h := newResultFixture(t)
	second := h.input.Evidence[0]
	second.FileID = 601
	h.input.Evidence = append(h.input.Evidence, second)
	result, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(result.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(result.ID, 13)
	require.NoError(t, err)
	_, _, _, body := workHTTPHarness(t)
	var consumer workDetailView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		consumer, e = h.m.createWorkOn(h.ctx, tx, 1, 99, 11, body)
		return e
	}))
	require.NoError(t, query.BindDependencies(h.db, h.m.service.names).WithContext(h.ctx).Create(&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: consumer.ID, DependsOnID: h.work}))
	digest := sha256.Sum256([]byte("exact"))
	hash := hex.EncodeToString(digest[:])
	e := query.BindResultEvidence(h.db, h.m.service.names)
	_, err = e.WithContext(h.ctx).Where(e.ResultID.Eq(result.ID)).Update(e.SHA256, hash)
	require.NoError(t, err)
	evidence, err := e.WithContext(h.ctx).Where(e.ResultID.Eq(result.ID)).Order(e.ID).Find()
	require.NoError(t, err)
	require.Len(t, evidence, 2)
	h.m.resultFiles = upstreamFiles{h.files, hash, false}
	var original model.ArtifactConsumption
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		original, e = h.m.consumeArtifactOn(h.ctx, tx, 1, 99, consumer.ID, 11, 11, consumptionBody{Version: consumer.Version, AcceptedArtifactID: evidence[0].ID})
		return e
	}))
	h.m.resultFiles = upstreamFiles{h.files, "changed", false}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.reconcileConsumptionOn(h.ctx, tx, &original, 11, "HUMAN") }))
	replacement := consumptionBody{Version: consumer.Version, AcceptedArtifactID: evidence[1].ID, ReplacesID: &original.ID, Reason: "Use independently accepted replacement evidence"}
	replace := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.consumeArtifactOn(h.ctx, tx, 1, 99, consumer.ID, 11, 11, replacement)
			return e
		})
	}
	require.ErrorIs(t, replace(), ErrUpstreamStale, "cannot retire a blocker in favor of stale new evidence")
	h.m.resultFiles = upstreamFiles{h.files, hash, false}
	require.NoError(t, replace())
	var stored model.ArtifactConsumption
	require.NoError(t, query.BindArtifactConsumptions(h.db, h.m.service.names).Where("id = ?", original.ID).First(&stored).Error)
	require.Equal(t, "SUPERSEDED", stored.State)
	require.Equal(t, original.SHA256, stored.SHA256)
	require.Equal(t, original.VersionID, stored.VersionID)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.reconcileConsumptionOn(h.ctx, tx, &original, 0, "SYSTEM") }))
	require.NoError(t, query.BindArtifactConsumptions(h.db, h.m.service.names).Where("id = ?", original.ID).First(&stored).Error)
	require.Equal(t, "SUPERSEDED", stored.State, "old sweep cannot reactivate a superseded snapshot")
}
