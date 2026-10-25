package planning

import (
	"testing"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
)

func TestAcceptanceCalendarUsesCanonicalTenantSettings(t *testing.T) {
	h := loopHarness(t)
	s := module.NewServices()
	var read clientwork.AcceptanceCalendar
	require.NoError(t, module.Provide[clientwork.AcceptanceCalendarConnector](s, clientwork.AcceptanceCalendarConnector{Bind: func(fn clientwork.AcceptanceCalendar) error { read = fn; return nil }}))
	require.NoError(t, s.Seal())
	require.NoError(t, h.m.bindAcceptanceCalendar(s))
	zone, version, e := read(t.Context(), h.db, 1)
	require.NoError(t, e)
	require.Equal(t, "Asia/Shanghai", zone)
	require.Equal(t, 1, version)
}
