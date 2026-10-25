package knowledge

import (
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestReportExperiencePreservesScopeKindProvenanceAndRollback(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	project := ptr(3)
	create := func(tx *gorm.DB) error {
		v, err := m.proposeReportExperienceOn(knowledgeContext(t, 2), tx, 1, 44, Scope{Scope: "PROJECT", ProjectID: project}, "Check boundary values before delivery")
		if err != nil {
			return err
		}
		require.Equal(t, "EXPERIENCE", v.Kind)
		require.Equal(t, "ACTIVE", v.State)
		require.Equal(t, project, v.ProjectID)
		require.Contains(t, v.Provenance, "44")
		return nil
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		if err := create(tx); err != nil {
			return err
		}
		return errors.New("confirmation failed")
	}))
	var count int64
	require.NoError(t, db.Table(m.names.Entries.String()).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Transaction(create))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.proposeReportExperienceOn(knowledgeContext(t, 2), tx, 1, 44, Scope{Scope: "TEAM"}, "Private human improvement")
		return err
	}), ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.proposeReportExperienceOn(knowledgeContext(t, 2), tx, 2, 44, Scope{Scope: "PROJECT", ProjectID: project}, "Cross tenant")
		return err
	}), ErrForbidden)
}
