package clientwork

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTriageRequestChecksCurrentAuthorityBeforeReplay(t *testing.T) {
	m, db, projects, _ := clarificationHarness(t)
	projects.deny = true // The ordinary Human/scope_change adapter must not run.
	allowed, configured, fail := true, true, true
	calls, checks := 0, 0
	m.triageTarget.AuthorizeRequestOn = func(_ context.Context, _ *gorm.DB, tenant, project, source guid.ID) error {
		checks++
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 3, project)
		require.EqualValues(t, 50, source)
		if !allowed {
			return ErrForbidden
		}
		if !configured {
			return intake.ErrInvalidArgument
		}
		return nil
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	m.triageTarget.RequestOn = func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID, note string) (intake.TriageRequest, error) {
		calls++
		require.Equal(t, "check current details", note)
		if err := tx.Exec("INSERT INTO baseline_probe VALUES (100)").Error; err != nil {
			return intake.TriageRequest{}, err
		}
		if fail {
			return intake.TriageRequest{}, errors.New("outbox failed")
		}
		return intake.TriageRequest{RequestedAt: at, Version: 2, Coalesced: calls > 2}, nil
	}
	in := &TriageRequestInput{EntityID: "3", ID: "50", Key: "retriage", Body: TriageRequestBody{Note: "check current details"}}
	_, err := m.requestTriageHTTP(testContext(t, 25), in)
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Table("baseline_probe").Count(&count).Error)
	require.Zero(t, count)
	fail = false
	first, err := m.requestTriageHTTP(testContext(t, 25), in)
	require.NoError(t, err)
	require.Equal(t, 201, first.Status)
	require.NotNil(t, first.Body.Data.RequestedAt)
	replay, err := m.requestTriageHTTP(testContext(t, 25), in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	require.Equal(t, 2, calls)
	allowed = false
	_, err = m.requestTriageHTTP(testContext(t, 25), in)
	require.Error(t, err)
	allowed = true
	configured = false
	_, err = m.requestTriageHTTP(testContext(t, 25), in)
	require.Error(t, err)
	configured = true
	in.Key = "another-request"
	coalesced, err := m.requestTriageHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, 202, coalesced.Status)
	require.True(t, coalesced.Body.Data.Coalesced)
	require.Equal(t, 6, checks)
}
