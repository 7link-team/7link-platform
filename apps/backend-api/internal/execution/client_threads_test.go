package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestClientThreadsFreezePinnedPackageBytesAndConversionAtomically(t *testing.T) {
	f := executionHarness(t)
	item := strings.Repeat("a", 64)
	data := "first line\nprecise rejected source\n"
	denied := false
	services := module.NewServices()
	require.NoError(t, f.m.provideClientThreads(services))
	require.NoError(t, services.Seal())
	connector, err := module.Resolve[ClientCommentAuthorityConnector](services)
	require.NoError(t, err)
	provider, err := module.Resolve[ClientArtifactComments](services)
	require.NoError(t, err)
	authority := ClientCommentAuthority{ItemOn: func(_ context.Context, _ *gorm.DB, tenant, project, delivery guid.ID, id string, _ bool) (ClientDeliveryItem, error) {
		if denied || tenant != 7 || project != 9 || delivery != 80 || id != item {
			return ClientDeliveryItem{}, ErrForbidden
		}
		return ClientDeliveryItem{WorkItemID: 10, EvidenceID: 20, VersionID: 30, ContactID: 40, SHA256: hash([]byte(data))}, nil
	}, OpenOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (files.PinnedResourceRead, error) {
		return files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 50, FileID: 60, VersionID: 30, SHA256: hash([]byte(data)), Name: "source.txt"}, Reader: io.NopCloser(strings.NewReader(data))}, nil
	}}
	require.NoError(t, connector.Bind(authority))
	require.ErrorIs(t, connector.Bind(authority), ErrUnavailable)
	var thread ClientThreadView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		thread, err = provider.CreateOn(f.human, tx, 7, 9, 80, item, ClientThreadCreate{StartLine: 2, EndLine: 2, Body: "This is the rejected output"})
		return err
	}))
	require.Equal(t, "precise rejected source", thread.AnchorExcerpt)
	require.Equal(t, "CODE_LINES", thread.AnchorKind)
	rollback := errors.New("receipt outbox failed")
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := provider.ConvertOn(f.human, tx, 7, 9, 80, item, thread.ThreadID, 100, 40)
		require.NoError(t, err)
		return rollback
	}), rollback)
	var link model.ClientCommentLink
	require.NoError(t, f.db.Table(f.m.names.ClientCommentLinks.String()).Where("thread_id = ?", thread.ThreadID).First(&link).Error)
	require.Nil(t, link.TargetID)
	frozen := link.AnchorSnapshot
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		anchor, err := provider.ConvertOn(f.human, tx, 7, 9, 80, item, thread.ThreadID, 101, 40)
		require.Equal(t, thread.ClientThreadAnchor, anchor)
		return err
	}))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := provider.ConvertOn(f.human, tx, 7, 9, 80, item, thread.ThreadID, 102, 40)
		var refusal *ClientThreadError
		require.ErrorAs(t, err, &refusal)
		require.Equal(t, "ALREADY_CONVERTED", refusal.Reason)
		require.EqualValues(t, 101, *refusal.TargetID)
		return nil
	}))
	require.NoError(t, f.db.Table(f.m.names.ClientCommentLinks.String()).Where("thread_id = ?", thread.ThreadID).First(&link).Error)
	require.Equal(t, frozen, link.AnchorSnapshot)
	var snapshot map[string]any
	require.NoError(t, json.Unmarshal([]byte(link.AnchorSnapshot), &snapshot))
	require.Equal(t, "precise rejected source", snapshot["excerpt"])
	denied = true
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := provider.GetOn(f.human, tx, 7, 9, 80, item, thread.ThreadID)
		return err
	}), ErrForbidden)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := provider.ListOn(f.human, tx, 7, 9, 80, item, 0, 21); return err }), ErrForbidden)
}

func TestClientThreadsRejectInternalForeignItemAndInvalidContent(t *testing.T) {
	f := executionHarness(t)
	item := strings.Repeat("b", 64)
	f.m.clientThreads.authority = ClientCommentAuthority{ItemOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, bool) (ClientDeliveryItem, error) {
		return ClientDeliveryItem{VersionID: 30, ContactID: 40, SHA256: hash([]byte("one\ntwo"))}, nil
	}, OpenOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (files.PinnedResourceRead, error) {
		return files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 1, FileID: 2, VersionID: 30, SHA256: hash([]byte("one\ntwo"))}, Reader: io.NopCloser(strings.NewReader("one\ntwo"))}, nil
	}}
	link := model.ClientCommentLink{ThreadID: 900, TenantID: 7, ProjectID: 9, DeliveryID: 80, ItemID: item, ContactID: 40, AnchorSnapshot: `{"kind":"CODE_LINES","excerpt":"one"}`, FirstBody: "Comment"}
	require.NoError(t, f.db.Table(f.m.names.ClientCommentLinks.String()).Create(&link).Error)
	parent := f.create(t)
	internal := model.ArtifactCommentThread{ID: 901, TenantID: 7, ProjectID: 9, ExecutionID: parent.ID, WorkItemID: 10, EvidenceID: 20, Visibility: "INTERNAL", State: "OPEN", AnchorSnapshot: `{"kind":"CODE_LINES"}`, FirstBody: "Internal team note", CreatedBy: 11, CreatedAt: time.Now(), Version: 1}
	require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Create(&internal).Error)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.getClientThreadOn(f.human, tx, 7, 9, 80, item, 901)
		var refusal *ClientThreadError
		require.ErrorAs(t, err, &refusal)
		require.Equal(t, "NOT_CLIENT_THREAD", refusal.Reason)
		return nil
	}))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.getClientThreadOn(f.human, tx, 7, 9, 81, item, 900)
		var refusal *ClientThreadError
		require.ErrorAs(t, err, &refusal)
		require.Equal(t, "NOT_THIS_ITEM", refusal.Reason)
		return nil
	}))
	for _, in := range []ClientThreadCreate{{StartLine: 1, EndLine: 1, Body: " "}, {StartLine: 0, EndLine: 1, Body: "Comment"}, {StartLine: 1, EndLine: 9, Body: "Comment"}} {
		require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
			_, err := f.m.createClientThreadOn(f.human, tx, 7, 9, 80, item, in)
			return err
		}))
	}
	require.Error(t, func() error { _, err := f.m.getClientThreadOn(f.human, f.db, 7, 9, 80, item, 900); return err }(), "source operations require the caller receipt transaction")
}

func TestClientThreadConversionDatabaseRejectsNullAndPartialTargets(t *testing.T) {
	f := executionHarness(t)
	for _, broken := range []string{
		"'REJECTION_ITEM',NULL,CURRENT_TIMESTAMP,40",
		"'REJECTION_ITEM',100,CURRENT_TIMESTAMP,NULL",
		"'REJECTION_ITEM',100,NULL,40",
		"'REJECTION_ITEM',0,CURRENT_TIMESTAMP,40",
		"'REJECTION_ITEM',100,CURRENT_TIMESTAMP,0",
		"'',100,NULL,NULL",
		"'OTHER',100,CURRENT_TIMESTAMP,40",
	} {
		t.Run(broken, func(t *testing.T) {
			err := f.db.Exec("INSERT INTO "+f.m.names.ClientCommentLinks.String()+" (thread_id,tenant_id,project_id,delivery_id,item_id,contact_id,first_body,created_by,created_at,anchor_snapshot,target_kind,target_id,converted_at,converted_by) VALUES (990,7,9,80,?,40,'comment',11,CURRENT_TIMESTAMP,'{}',"+broken+")", strings.Repeat("a", 64)).Error
			require.Error(t, err, "the actual migrated database must reject every partial conversion")
		})
	}
	for _, valid := range []string{"'',NULL,NULL,NULL", "'REJECTION_ITEM',100,CURRENT_TIMESTAMP,40"} {
		require.NoError(t, f.db.Exec("INSERT INTO "+f.m.names.ClientCommentLinks.String()+" (thread_id,tenant_id,project_id,delivery_id,item_id,contact_id,first_body,created_by,created_at,anchor_snapshot,target_kind,target_id,converted_at,converted_by) VALUES (990,7,9,80,?,40,'comment',11,CURRENT_TIMESTAMP,'{}',"+valid+")", strings.Repeat("a", 64)).Error)
		require.NoError(t, f.db.Exec("DELETE FROM "+f.m.names.ClientCommentLinks.String()+" WHERE thread_id=990").Error)
	}
}

func TestClientThreadCommandsRollbackEveryStorageFailureAndListFrozenAnchors(t *testing.T) {
	for _, command := range []string{"create", "get", "list", "convert"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f := executionHarness(t)
				item := strings.Repeat("a", 64)
				data := "first\nsecond\n"
				f.m.clientThreads.authority = ClientCommentAuthority{ItemOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, bool) (ClientDeliveryItem, error) {
					return ClientDeliveryItem{WorkItemID: 10, EvidenceID: 20, VersionID: 30, ContactID: 40, SHA256: hash([]byte(data))}, nil
				}, OpenOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (files.PinnedResourceRead, error) {
					return files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 50, FileID: 60, VersionID: 30, SHA256: hash([]byte(data))}, Reader: io.NopCloser(strings.NewReader(data))}, nil
				}}
				in := ClientThreadCreate{StartLine: 2, EndLine: 2, Body: "Review exact source"}
				var thread ClientThreadView
				if command != "create" {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
						var e error
						thread, e = f.m.createClientThreadOn(f.human, tx, 7, 9, 80, item, in)
						return e
					}))
				}
				return f, func(tx *gorm.DB) error {
					switch command {
					case "create":
						out, e := f.m.createClientThreadOn(f.human, tx, 7, 9, 80, item, in)
						if e == nil {
							require.Equal(t, "second", out.AnchorExcerpt)
						}
						return e
					case "get":
						out, e := f.m.getClientThreadOn(f.human, tx, 7, 9, 80, item, thread.ThreadID)
						if e == nil {
							require.Equal(t, thread, out)
						}
						return e
					case "list":
						out, e := f.m.listClientThreadsOn(f.human, tx, 7, 9, 80, item, 0, 1)
						if e == nil {
							require.Equal(t, []ClientThreadView{thread}, out)
						}
						return e
					default:
						out, e := f.m.convertClientThreadOn(f.human, tx, 7, 9, 80, item, thread.ThreadID, 100, 40)
						if e == nil {
							require.Equal(t, thread.ClientThreadAnchor, out)
						}
						return e
					}
				}
			})
		})
	}
}
func TestClientThreadAuthorityAndPinnedSourceFailures(t *testing.T) {
	for _, fault := range []string{"missing_authority", "item_error", "missing_open", "open_error", "id_error", "bad_anchor", "invalid_item", "wrong_contact", "invalid_limit"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			item := strings.Repeat("a", 64)
			failure := errors.New("client source unavailable")
			f.m.clientThreads.authority = ClientCommentAuthority{ItemOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, bool) (ClientDeliveryItem, error) {
				return ClientDeliveryItem{VersionID: 30, ContactID: 40, SHA256: hash([]byte("source"))}, nil
			}, OpenOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (files.PinnedResourceRead, error) {
				return files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 50, FileID: 60, VersionID: 30, SHA256: hash([]byte("source"))}, Reader: io.NopCloser(strings.NewReader("source"))}, nil
			}}
			switch fault {
			case "missing_authority":
				f.m.clientThreads.authority.ItemOn = nil
			case "item_error":
				f.m.clientThreads.authority.ItemOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, bool) (ClientDeliveryItem, error) {
					return ClientDeliveryItem{}, failure
				}
			case "missing_open":
				f.m.clientThreads.authority.OpenOn = nil
			case "open_error":
				f.m.clientThreads.authority.OpenOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (files.PinnedResourceRead, error) {
					return files.PinnedResourceRead{Reader: io.NopCloser(strings.NewReader("source"))}, failure
				}
			case "id_error":
				f.m.ids = failedExecutionIDs{err: failure}
			case "invalid_item":
				item = "not-a-pin"
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
				if fault == "wrong_contact" || fault == "bad_anchor" {
					link := model.ClientCommentLink{ThreadID: 900, TenantID: 7, ProjectID: 9, DeliveryID: 80, ItemID: item, ContactID: 40, AnchorSnapshot: `{"kind":"CODE_LINES"}`, FirstBody: "Frozen"}
					if fault == "bad_anchor" {
						link.AnchorSnapshot = "{"
					}
					require.NoError(t, tx.Table(f.m.names.ClientCommentLinks.String()).Create(&link).Error)
					if fault == "bad_anchor" {
						_, e := f.m.getClientThreadOn(f.human, tx, 7, 9, 80, item, 900)
						return e
					}
					_, e := f.m.convertClientThreadOn(f.human, tx, 7, 9, 80, item, 900, 100, 41)
					return e
				}
				if fault == "invalid_limit" {
					_, e := f.m.listClientThreadsOn(f.human, tx, 7, 9, 80, item, 0, 0)
					return e
				}
				_, e := f.m.createClientThreadOn(f.human, tx, 7, 9, 80, item, ClientThreadCreate{StartLine: 1, EndLine: 1, Body: "Exact source"})
				return e
			}))
			var n int64
			require.NoError(t, f.db.Table(f.m.names.ClientCommentLinks.String()).Count(&n).Error)
			require.Zero(t, n)
		})
	}
}

func TestProvideClientThreadsRejectsDuplicateCapabilityOwnership(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, f.m.provideClientThreads(s))
	require.ErrorIs(t, f.m.provideClientThreads(s), module.ErrServiceDuplicate)
}
