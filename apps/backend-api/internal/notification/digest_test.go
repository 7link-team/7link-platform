package notification

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDailyDigestCombinesDueMessages(t *testing.T) {
	f := newFixture(t)
	c, messages := smtpFixture(t, 250)
	enableEmail(t, f, c)
	f.m.principal.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{Email: "reader@example.test"}, true, nil
	}
	p := PreferenceView{InApp: true, Email: "DAILY_DIGEST", Timezone: "UTC", DigestHour: 13}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.savePreferenceOn(t.Context(), tx, 11, 7, "*", p, 0); return err }))
	for _, subject := range []string{"91", "92"} {
		n := sample()
		n.DedupeKey = "machine:" + subject
		n.Params["subject_id"] = subject
		f.notify(t, n)
	}
	q := query.BindDeliveries(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Order(q.ID).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	start := f.m.now()
	f.m.now = func() time.Time { return start.Add(time.Hour) }
	require.NoError(t, f.m.deliverJob(t.Context(), deliveryJob(t, rows[0])))
	require.NoError(t, f.m.deliverJob(t.Context(), deliveryJob(t, rows[1])))
	require.Len(t, messages(), 1)
	require.Contains(t, messages()[0], "91")
	require.Contains(t, messages()[0], "92")
	rows, err = q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	for _, row := range rows {
		require.Equal(t, "SENT", row.State)
		require.Equal(t, 1, row.Attempts)
	}
}
