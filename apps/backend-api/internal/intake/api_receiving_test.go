package intake

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

type receiptIDs struct {
	id  int64
	err error
}

func (s receiptIDs) Next() (int64, error) { return s.id, s.err }

func TestAPIReceiptDependencyFailuresDoNotCreateSourcesOrEffects(t *testing.T) {
	for _, fault := range []string{"ids_missing", "ids_error", "ids_zero", "audit_missing", "audit_error", "contact_storage", "record_storage", "reference_exhausted", "insert"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, _ := changeSourceHarness(t)
			m.brief.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				return authz.Explanation{Allowed: true}, nil
			}
			failure := errors.New("receipt dependency failed")
			want := failure
			switch fault {
			case "ids_missing":
				m.brief.ids = nil
				want = ErrUnavailable
			case "ids_error":
				m.brief.ids = receiptIDs{err: failure}
			case "ids_zero":
				m.brief.ids = receiptIDs{}
				want = ErrUnavailable
			case "audit_missing":
				m.brief.audit = nil
				want = ErrUnavailable
			case "audit_error":
				m.brief.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "contact_storage", "record_storage":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("receipt_failure", func(tx *gorm.DB) {
					if (fault == "contact_storage" && tx.Statement.Table == m.names.Contacts.String()) || (fault == "record_storage" && tx.Statement.Table == m.names.Records.String()) {
						tx.AddError(failure)
					}
				}))
			case "reference_exhausted":
				require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Update("ref_no", 2147483647).Error)
				want = ErrConflict
			case "insert":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("receipt_failure", func(tx *gorm.DB) { tx.AddError(failure) }))
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				out, err := m.receiveAPIOn(ctx, tx, 7, 20, 99, APIReceiptInput{ContactID: 1, Title: "Source", Content: "immutable", Format: "PLAIN"})
				require.Zero(t, out.ID)
				return err
			}), want)
			require.NoError(t, db.Callback().Query().Remove("receipt_failure"))
			require.NoError(t, db.Callback().Create().Remove("receipt_failure"))
			var count int64
			require.NoError(t, db.Table(m.names.Records.String()).Count(&count).Error)
			require.EqualValues(t, 1, count)
			require.NoError(t, db.Table("brief_probe").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestAPIReceivingCreatesImmutableRealSourceAndRollsBackOutbox(t *testing.T) {
	m, db, ctx, allowed, pub := changeSourceHarness(t)
	m.brief.explain = func(_ context.Context, _ *gorm.DB, in core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.Equal(t, "intake_submit", in.Permission)
		return authz.Explanation{Allowed: *allowed}, nil
	}
	in := APIReceiptInput{ContactID: 1, Title: "API source", Content: "Original <script>untrusted</script>", Format: "PLAIN"}
	var received IntakeLedgerEntry
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		received, err = m.receiveAPIOn(ctx, tx, 7, 20, 99, in)
		return err
	}))
	require.Equal(t, "RECEIVED", received.State)
	require.Equal(t, "NEW", received.Kind)
	require.Equal(t, 2, received.RefNo)
	require.Zero(t, received.CardRevision)
	q := query.BindRecords(db, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(received.ID)).Take()
	require.NoError(t, err)
	digest := sha256.Sum256([]byte(in.Content))
	require.Equal(t, hex.EncodeToString(digest[:]), row.RawContentHash)
	require.Equal(t, in.Content, *row.RawContent)
	require.EqualValues(t, 20, *row.ProjectID)
	require.EqualValues(t, 99, row.ChannelID)
	pub.fail = true
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.receiveAPIOn(ctx, tx, 7, 20, 99, in); return err }))
	n, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	pub.fail = false
	*allowed = false
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.receiveAPIOn(ctx, tx, 7, 20, 99, in); return err }), ErrForbidden)
	*allowed = true
	for _, bad := range []APIReceiptInput{{ContactID: 3, Title: "Foreign contact", Content: "content", Format: "PLAIN"}, {ContactID: 1, Title: " ", Content: "content", Format: "PLAIN"}, {ContactID: 1, Title: "valid", Content: strings.Repeat("x", 65537), Format: "PLAIN"}, {ContactID: 1, Title: "valid", Content: "content", Format: "HTML_TEXT"}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.receiveAPIOn(ctx, tx, 7, 20, 99, bad); return err }), ErrInvalidArgument)
	}
}

func TestAPIReceiptCreatesAndReusesCurrentCallerSystemContact(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	m.brief.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true}, nil
	}
	in := APIReceiptInput{Title: "First real submission", Content: "immutable", Format: "PLAIN"}
	var firstContact int64
	for i := 0; i < 2; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			received, err := m.receiveAPIOn(ctx, tx, 7, 20, 99, in)
			if err != nil {
				return err
			}
			var row model.Record
			require.NoError(t, tx.Table(m.names.Records.String()).Where("id=?", received.ID).Take(&row).Error)
			if i == 0 {
				firstContact = int64(row.ContactID)
			} else {
				require.EqualValues(t, firstContact, row.ContactID)
			}
			return nil
		}))
	}
	var contacts []model.Contact
	require.NoError(t, db.Table(m.names.Contacts.String()).Where("tenant_id=? AND principal_id=?", 7, 2).Find(&contacts).Error)
	require.Len(t, contacts, 1)
	require.EqualValues(t, firstContact, contacts[0].ID)
	require.Equal(t, "SYSTEM", contacts[0].Kind)
	require.Equal(t, "ACTIVE", contacts[0].State)
	require.EqualValues(t, 2, *contacts[0].CreatedBy)
	for _, update := range []map[string]any{{"state": "BLOCKED"}, {"state": "ACTIVE", "archived_at": time.Now()}, {"archived_at": nil, "kind": "PERSON"}} {
		require.NoError(t, db.Table(m.names.Contacts.String()).Where("id=?", firstContact).Updates(update).Error)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.receiveAPIOn(ctx, tx, 7, 20, 99, in); return err }), ErrInvalidArgument)
	}
}

func TestAPIReceiptFirstContactRollsBackWithSourceAndEffects(t *testing.T) {
	for _, fault := range []string{"ids_error", "ids_zero", "contact_read", "contact_create", "audit", "outbox", "forbidden", "negative_contact"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, pub := changeSourceHarness(t)
			m.brief.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				return authz.Explanation{Allowed: fault != "forbidden"}, nil
			}
			failure := errors.New("first receipt failed")
			in := APIReceiptInput{Title: "First", Content: "real", Format: "PLAIN"}
			switch fault {
			case "ids_error":
				m.brief.ids = receiptIDs{err: failure}
			case "ids_zero":
				m.brief.ids = receiptIDs{}
			case "contact_read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("receipt_read", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Contacts.String() {
						tx.AddError(failure)
					}
				}))
			case "contact_create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("receipt_create", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Contacts.String() {
						tx.AddError(failure)
					}
				}))
			case "audit":
				m.brief.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "outbox":
				pub.fail = true
			case "negative_contact":
				in.ContactID = -1
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.receiveAPIOn(ctx, tx, 7, 20, 99, in); return err }))
			require.NoError(t, db.Callback().Query().Remove("receipt_read"))
			require.NoError(t, db.Callback().Create().Remove("receipt_create"))
			var count int64
			require.NoError(t, db.Table(m.names.Contacts.String()).Where("principal_id=?", 2).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, db.Table(m.names.Records.String()).Count(&count).Error)
			require.EqualValues(t, 1, count)
			require.NoError(t, db.Table("brief_probe").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
