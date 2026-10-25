package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUpstreamHTTPUsesAcceptedEvidenceAndReconciliationRollsBackWithAudit(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(result.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(result.ID, 13)
	require.NoError(t, err)
	_, _, _, body := workHTTPHarness(t)
	var consumer workDetailView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		consumer, err = h.m.createWorkOn(h.ctx, tx, 1, 99, 11, body)
		return err
	}))
	require.NoError(t, query.BindDependencies(h.db, h.m.service.names).WithContext(h.ctx).Create(&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: consumer.ID, DependsOnID: h.work}))
	e := query.BindResultEvidence(h.db, h.m.service.names)
	evidence, err := e.WithContext(h.ctx).Where(e.ResultID.Eq(result.ID)).First()
	require.NoError(t, err)
	digest := sha256.Sum256([]byte("exact"))
	hash := hex.EncodeToString(digest[:])
	_, err = e.WithContext(h.ctx).Where(e.ID.Eq(evidence.ID)).Update(e.SHA256, hash)
	require.NoError(t, err)
	h.m.resultFiles = upstreamFiles{h.files, hash, false}
	path := ResultWorkInput{EntityID: "99", WorkItemID: consumer.ID.String()}
	in := &consumptionInput{ResultWorkInput: path, Key: "n-consume-accepted", Body: consumptionBody{Version: consumer.Version, AcceptedArtifactID: evidence.ID}}
	created, err := h.m.consumeArtifactHTTP(h.ctx, in)
	require.NoError(t, err)
	require.Equal(t, "CURRENT", created.Body.Data.State)
	replay, err := h.m.consumeArtifactHTTP(h.ctx, in)
	require.NoError(t, err)
	require.Equal(t, created.Body.Data.ID, replay.Body.Data.ID)
	list, err := h.m.listConsumptionsHTTP(h.ctx, &path)
	require.NoError(t, err)
	require.Len(t, list.Body.Data, 1)
	require.Equal(t, hash, list.Body.Data[0].SHA256)
	unchanged, err := h.m.reconcileArtifactsHTTP(h.ctx, &path)
	require.NoError(t, err)
	require.Equal(t, "CURRENT", unchanged.Body.Data[0].State)
	h.m.resultFiles = upstreamFiles{h.files, hash, true}
	h.fail = "audit"
	_, err = h.m.reconcileArtifactsHTTP(h.ctx, &path)
	require.Error(t, err)
	list, err = h.m.listConsumptionsHTTP(h.ctx, &path)
	require.NoError(t, err)
	require.Equal(t, "CURRENT", list.Body.Data[0].State)
	h.fail = ""
	inconclusive, err := h.m.reconcileArtifactsHTTP(h.ctx, &path)
	require.NoError(t, err)
	require.Equal(t, "INCONCLUSIVE", inconclusive.Body.Data[0].State)
	require.Equal(t, hash, inconclusive.Body.Data[0].SHA256, "reconciliation preserves the accepted snapshot")
	h.m.resultFiles = upstreamFiles{h.files, hash, false}
	job := h.m.artifactReconcileHandler()
	require.NoError(t, job.Run(h.ctx, jobs.Job{TenantID: 1}))
	list, err = h.m.listConsumptionsHTTP(h.ctx, &path)
	require.NoError(t, err)
	require.Equal(t, "CURRENT", list.Body.Data[0].State)
	require.Error(t, job.Run(h.ctx, jobs.Job{}))
	lock := h.m.principals.Lock
	h.m.principals.Lock = nil
	require.Error(t, job.Run(h.ctx, jobs.Job{TenantID: 1}))
	h.m.principals.Lock = lock
	h.m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, _ string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = h.m.listConsumptionsHTTP(h.ctx, &path)
	require.Error(t, err)
	_, err = h.m.reconcileArtifactsHTTP(h.ctx, &path)
	require.Error(t, err)
	_, err = h.m.consumeArtifactHTTP(h.ctx, in)
	require.Error(t, err, "accepted snapshot receipt never bypasses current permission")
}
