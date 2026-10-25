package notification

import (
	"7link.com/platform/internal/notification/model"
	"context"
	"encoding/json"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProjectControlNoticesRecheckExactEpisodeAndCurrentVisibility(t *testing.T) {
	for _, typ := range []string{"project.andon_pulled", "project.mode_changed", "project.taken_over"} {
		t.Run(typ, func(t *testing.T) {
			f := newFixture(t)
			found, readable := true, true
			occurrence := guid.ID(901)
			source := func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ProjectControlFact, bool, error) {
				return delivery.ProjectControlFact{ProjectID: 9, OccurrenceID: occurrence, ToMode: "HYBRID"}, found, nil
			}
			f.m.projectControls = delivery.ProjectControlNotifications{AndonOn: source, ModeOn: source, ReadableOn: func(_ context.Context, _ *gorm.DB, _, _, actor guid.ID) (bool, error) {
				return readable && actor == 12, nil
			}}
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 9, SubjectKind: "project", Type: typ, Data: []byte(`{"mode_change_id":"901","takeover_id":"901"}`)}
			for range 2 {
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
			}
			q := query.BindNotifications(f.db, f.m.names)
			rows, err := q.WithContext(t.Context()).Where(q.Type.Eq(typ)).Find()
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
			require.Equal(t, 1, rows[0].MergeCount)
			yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
			require.NoError(t, err)
			require.True(t, yes)
			found = false
			yes, err = f.m.visibleOn(t.Context(), f.db, rows[0], 12)
			require.NoError(t, err)
			require.False(t, yes)
			found = true
			readable = false
			yes, err = f.m.visibleOn(t.Context(), f.db, rows[0], 12)
			require.NoError(t, err)
			require.False(t, yes)
			readable = true
			occurrence = 902
			yes, err = f.m.visibleOn(t.Context(), f.db, rows[0], 12)
			require.NoError(t, err)
			require.False(t, yes)
		})
	}
}

func TestAutonomyNoticesDeduplicateTransitionsAndSuppressObsoleteState(t *testing.T) {
	for _, typ := range []string{"project.autonomy_lost", "project.autonomy_restored"} {
		t.Run(typ, func(t *testing.T) {
			f := newFixture(t)
			held, readable := typ == "project.autonomy_lost", true
			currentVersion := int64(2)
			f.m.projectControls = delivery.ProjectControlNotifications{
				AutonomyOn: func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, expected bool, version int64) (bool, error) {
					return tenant == 7 && project == 9 && expected == held && version == currentVersion, nil
				},
				ReadableOn: func(_ context.Context, _ *gorm.DB, _, _, actor guid.ID) (bool, error) {
					return readable && actor == 12, nil
				},
			}
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 9, SubjectKind: "project", Type: typ, Data: []byte(`{"autonomy_version":2}`)}
			for range 2 {
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
			}
			q := query.BindNotifications(f.db, f.m.names)
			rows, err := q.WithContext(t.Context()).Where(q.Type.Eq(typ)).Find()
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, 1, rows[0].MergeCount)
			var params map[string]string
			require.NoError(t, json.Unmarshal([]byte(rows[0].Params), &params))
			require.Equal(t, "1001", params["occurrence_id"], "the actual transition event ID owns deduplication")
			require.Equal(t, "9", params["subject_id"])
			require.Equal(t, "2", params["autonomy_version"])
			visible, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
			require.NoError(t, err)
			require.True(t, visible)
			originalParams := rows[0].Params
			for _, revision := range []string{"", "0", "-1", "invalid"} {
				params["autonomy_version"] = revision
				raw, marshalErr := json.Marshal(params)
				require.NoError(t, marshalErr)
				rows[0].Params = string(raw)
				visible, err = f.m.visibleOn(t.Context(), f.db, rows[0], 12)
				require.NoError(t, err)
				require.False(t, visible, "missing or invalid old notice revisions fail closed")
			}
			rows[0].Params = originalParams
			for _, payload := range []string{`{}`, `{"autonomy_version":0}`, `{"autonomy_version":-1}`, `{"autonomy_version":"2"}`, `{"autonomy_version":2.5}`} {
				invalid := e
				invalid.ID, invalid.Data = 1009, []byte(payload)
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, invalid) }))
			}
			var projectedCount int64
			require.NoError(t, f.db.Table(f.m.names.Notifications.String()).Where("type=?", typ).Count(&projectedCount).Error)
			require.Equal(t, int64(1), projectedCount, "legacy and invalid events create no fabricated episode")
			readable = false
			visible, err = f.m.visibleOn(t.Context(), f.db, rows[0], 12)
			require.NoError(t, err)
			require.False(t, visible)
			readable = true
			held = !held
			visible, err = f.m.visibleOn(t.Context(), f.db, rows[0], 12)
			require.NoError(t, err)
			require.False(t, visible, "recovered/lost current source suppresses the obsolete transition notice")
			e.ID = 1002
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
			var count int64
			require.NoError(t, f.db.Table(f.m.names.Notifications.String()).Where("type=?", typ).Count(&count).Error)
			require.Equal(t, int64(1), count, "stale event projection cannot create a second notice")
		})
	}
}

func TestAutonomyNoticesNeverReviveEarlierEpisodeAfterABA(t *testing.T) {
	for _, typ := range []string{"project.autonomy_lost", "project.autonomy_restored"} {
		for _, projected := range []bool{false, true} {
			name := typ + map[bool]string{false: "/delayed_projection", true: "/existing_notice"}[projected]
			t.Run(name, func(t *testing.T) {
				f := newFixture(t)
				held := typ == "project.autonomy_lost"
				currentVersion := int64(2)
				f.m.projectControls = delivery.ProjectControlNotifications{
					AutonomyOn: func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, expected bool, version int64) (bool, error) {
						return tenant == 7 && project == 9 && expected == held && version == currentVersion, nil
					},
					ReadableOn: func(_ context.Context, _ *gorm.DB, _, _, actor guid.ID) (bool, error) {
						return actor == 12, nil
					},
				}
				old := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 9, SubjectKind: "project", Type: typ, Data: []byte(`{"autonomy_version":2}`)}
				if projected {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, old) }))
				}
				// An opposite transition and then a new transition of the same type
				// restore the held boolean without restoring the old episode.
				held = !held
				held = !held
				currentVersion = 4
				current := old
				current.ID, current.Data = 1003, []byte(`{"autonomy_version":4}`)
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, current) }))
				q := query.BindNotifications(f.db, f.m.names)
				if projected {
					rows, err := q.WithContext(t.Context()).Where(q.Type.Eq(typ)).Order(q.ID).Find()
					require.NoError(t, err)
					require.Len(t, rows, 2)
					visible, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
					require.NoError(t, err)
					require.False(t, visible, "the first episode cannot revive after ABA")
					visible, err = f.m.visibleOn(t.Context(), f.db, rows[1], 12)
					require.NoError(t, err)
					require.True(t, visible, "the actual current episode remains eligible")
				} else {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, old) }))
					var count int64
					require.NoError(t, f.db.Table(f.m.names.Notifications.String()).Where("type=?", typ).Count(&count).Error)
					require.Equal(t, int64(1), count, "delayed first-episode projection stays suppressed after ABA")
				}
			})
		}
	}
}

func TestProjectControlProjectionSourceAndAudienceFailuresLeaveNoNotice(t *testing.T) {
	for _, mode := range []string{"facts", "candidates", "order", "readable", "notify", "missing", "invalid", "takeover"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.projectControls = delivery.ProjectControlNotifications{ModeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ProjectControlFact, bool, error) {
				if mode == "facts" {
					return delivery.ProjectControlFact{}, false, ErrUnavailable
				}
				return delivery.ProjectControlFact{ProjectID: 9, OccurrenceID: 91}, true, nil
			}, ReadableOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) {
				if mode == "readable" {
					return false, ErrUnavailable
				}
				return true, nil
			}}
			f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) {
				if mode == "candidates" {
					return nil, ErrUnavailable
				}
				if mode == "order" {
					return []guid.ID{0}, nil
				}
				return []guid.ID{11}, nil
			}
			if mode == "missing" {
				f.m.projectControls.ModeOn = nil
			}
			if mode == "notify" {
				f.ids.err = ErrUnavailable
			}
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectKind: "project", SubjectID: 9, Type: "project.mode_changed", Data: []byte(`{"mode_change_id":"91"}`)}
			if mode == "invalid" {
				e.Data = []byte("[")
			}
			if mode == "takeover" {
				e.Data = []byte(`{"trigger":"TAKEOVER"}`)
			}
			err := f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectControlEventOn(t.Context(), tx, e) })
			if mode == "takeover" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			n, x := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, x)
			require.Zero(t, n)
		})
	}
	f := newFixture(t)
	row := &model.Notification{TenantID: 7, ProjectID: 9, Type: "project.mode_changed", Params: `{"subject_id":"91"}`}
	_, e := f.m.projectControlAudienceOn(t.Context(), f.db, row, 11)
	require.ErrorIs(t, e, ErrUnavailable)
	f.m.projectControls.ReadableOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
	for _, params := range []string{"[", `{"subject_id":"bad"}`} {
		row.Params = params
		_, e = f.m.projectControlAudienceOn(t.Context(), f.db, row, 11)
		require.ErrorIs(t, e, ErrInvalid)
	}
	row.Params = `{"subject_id":"91"}`
	for _, typ := range []string{"project.mode_changed", "project.andon_pulled", "project.autonomy_lost"} {
		row.Type = typ
		_, e = f.m.projectControlAudienceOn(t.Context(), f.db, row, 11)
		require.ErrorIs(t, e, ErrUnavailable)
	}
}
