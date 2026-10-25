package agents

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetirementForcedSummaryHasNullProducerAndFailureDoesNotUndoRevocation(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "summary", true: "unavailable"}[fail], func(t *testing.T) {
			m, db, _ := agentHarness(t)
			retirementWorkFixture(m)
			m.forcedRetirementProof = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil }
			m.retirementRuntime.ForceOn = func(_ context.Context, tx *gorm.DB, _, agent, id guid.ID, v int, _ string, _ fleet.RetirementLoss) (fleet.AgentRetirementBinding, error) {
				err := tx.Exec("INSERT INTO audit_probe VALUES ('credential_revoked')").Error
				return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "RETIRED", Version: v + 1}, err
			}
			m.retirementHandover.SystemOn = func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID, content string) (HandoverArtifact, error) {
				require.Contains(t, content, "Forced retirement")
				require.NoError(t, tx.Exec("INSERT INTO audit_probe VALUES ('artifact')").Error)
				if fail {
					return HandoverArtifact{}, errors.New("artifact store unavailable")
				}
				return HandoverArtifact{ID: 900, Source: "SYSTEM", Content: content, SHA256: "source digest"}, nil
			}
			var r model.Retirement
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				r, err = m.startRetirementOn(agentContext(t, 2), tx, 1, 7, 5, RetirementStart{Mode: "FORCED", Reason: "Offline machine requires retirement", ConfirmName: "Agent", ReauthProof: "fresh"})
				return err
			}))
			require.Equal(t, "COMPLETED", r.State)
			stored, err := query.BindRetirement(db, m.names).WithContext(t.Context()).Where(query.BindRetirement(db, m.names).ID.Eq(r.ID)).First()
			require.NoError(t, err)
			p := stored.Precheck.Progress
			require.NotNil(t, p)
			var count int64
			require.NoError(t, db.Table("audit_probe").Where("event_type='credential_revoked'").Count(&count).Error)
			require.EqualValues(t, 1, count)
			if fail {
				require.Equal(t, "ARTIFACT_UNAVAILABLE", p.HandoverErrorCode)
				require.Nil(t, p.HandoverArtifactID)
				require.Empty(t, p.HandoverSource)
				require.NoError(t, db.Table("audit_probe").Where("event_type='artifact'").Count(&count).Error)
				require.Zero(t, count)
			} else {
				require.Equal(t, "SYSTEM", p.HandoverSource)
				require.EqualValues(t, 900, *p.HandoverArtifactID)
				require.Nil(t, p.HandoverExecutionID)
			}
		})
	}
}
