package model

import (
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
	"sync"
	"testing"
)

func TestIngressObservationModelPreservesSourceIdentityAndExplicitTime(t *testing.T) {
	s, err := schema.Parse(&IngressObservation{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)
	require.Equal(t, "integration_ingress_observations", s.Table)
	for _, name := range []string{"id", "tenant_id", "project_id", "mapping_id", "event_id", "created_at", "source", "detail", "content_hash"} {
		require.NotNil(t, s.LookUpField(name), name)
	}
	require.EqualValues(t, 0, s.LookUpField("created_at").AutoCreateTime)
	require.EqualValues(t, 0, s.LookUpField("created_at").AutoUpdateTime)
}
