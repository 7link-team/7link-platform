package core

import (
	"testing"

	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
)

type menuTestContribution struct{ enabled bool }

func (menuTestContribution) ModuleName() string { return "menu_test" }
func (m menuTestContribution) Enabled() bool    { return m.enabled }
func (menuTestContribution) MenuPages() []MenuPage {
	return []MenuPage{{Key: "test", Version: 1}}
}

type menuTestSink struct{ owners []string }

func (s *menuTestSink) ContributeMenus(owner string, _ ...MenuPage) error {
	s.owners = append(s.owners, owner)
	return nil
}

func TestMenuContributionsDiscoverOnlyActivatedModules(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		r := module.NewRegistry()
		require.NoError(t, r.Register(menuTestContribution{enabled}))
		require.NoError(t, r.Activate())
		sink := &menuTestSink{}
		require.NoError(t, r.Wire(func(s *module.Services) error { return module.Provide[MenuSink](s, sink) }))
		require.NoError(t, WireMenuContributions(r))
		if enabled {
			require.Equal(t, []string{"menu_test"}, sink.owners)
		} else {
			require.Empty(t, sink.owners)
		}
	}
}
