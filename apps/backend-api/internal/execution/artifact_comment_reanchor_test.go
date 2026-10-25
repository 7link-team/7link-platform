package execution

import (
	"7link.com/platform/internal/execution/model"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
)

func TestArtifactCommentReanchorVerifiedPinnedUnchangedUniqueContent(t *testing.T) {
	for _, tc := range []struct {
		kind, old, new string
		start, end     int
		index          int
		want           bool
	}{
		{"CODE_LINES", "one\ntwo", "inserted\none\ntwo", 2, 2, 0, true},
		{"CODE_LINES", "one\ntwo", "two\none\ntwo", 2, 2, 0, false},
		{"CODE_LINES", "one\ntwo", "one\nchanged", 2, 2, 0, false},
		{"TEXT_PARAGRAPH", "first\n\nsecond", "inserted\n\nfirst\n\nsecond", 0, 0, 1, true},
		{"TABLE_ROW", "[{\"id\":1},{\"id\":2}]", "[{\"id\":0},{\"id\":1},{\"id\":2}]", 0, 0, 1, true},
	} {
		t.Run(tc.kind+tc.new, func(t *testing.T) {
			pin := func(data string, version int64) files.PinnedResourceRead {
				return files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 1, FileID: 2, VersionID: guid.ID(version), SHA256: hash([]byte(data)), Name: "source.json"}, Reader: io.NopCloser(strings.NewReader(data))}
			}
			index := tc.index
			in := ArtifactCommentCreate{AnchorKind: tc.kind, StartLine: tc.start, EndLine: tc.end, RowIndex: &index, ParagraphIndex: &index}
			old, err := internalArtifactAnchor(pin(tc.old, 3), ArtifactFact{BindingID: 1, FileID: 2, VersionID: 3, SHA256: hash([]byte(tc.old))}, in)
			require.NoError(t, err)
			original, _ := json.Marshal(old)
			mapped, err := relocatePinnedComment(old, pin(tc.new, 4), ArtifactFact{BindingID: 1, FileID: 2, VersionID: 4, SHA256: hash([]byte(tc.new))})
			require.NoError(t, err)
			if tc.want {
				require.NotNil(t, mapped)
				require.Equal(t, old.Excerpt, mapped.Excerpt)
				require.NotEqual(t, old.VersionRef, mapped.VersionRef)
			} else {
				require.Nil(t, mapped)
			}
			after, _ := json.Marshal(old)
			require.Equal(t, string(original), string(after))
		})
	}
}

func TestArtifactCommentReanchorHTMLRectStaysOutdatedAndForgedPinDenied(t *testing.T) {
	data := "<p>unchanged</p>"
	digest := hash([]byte(data))
	original := delivery.ArtifactAnchorSnapshot{Kind: "HTML_REGION", FileID: 2}
	pin := files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 1, FileID: 2, VersionID: 4, SHA256: digest}, Reader: io.NopCloser(strings.NewReader(data))}
	out, err := relocatePinnedComment(original, pin, ArtifactFact{BindingID: 1, FileID: 2, VersionID: 4, SHA256: digest})
	require.NoError(t, err)
	require.Nil(t, out)
	pin.Reader = io.NopCloser(strings.NewReader("forged"))
	_, err = relocatePinnedComment(original, pin, ArtifactFact{BindingID: 1, FileID: 2, VersionID: 4, SHA256: digest})
	require.Error(t, err)
}

func TestArtifactCommentReanchorEveryStorageFailurePreservesAnchorAndActivity(t *testing.T) {
	for _, command := range []string{"apply"} {
		for _, kind := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				f, execution, evidence := pinnedCommentFixture(t)
				in := ArtifactCommentCreate{EvidenceID: evidence, StartLine: 2, EndLine: 2, Body: "Review observed source"}
				var thread ArtifactCommentView
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
					var e error
					thread, e = f.m.CreateArtifactCommentOn(f.human, tx, 7, execution, in)
					return e
				}))
				snapshot := func() map[string][]map[string]any {
					out := map[string][]map[string]any{}
					for _, table := range []string{f.m.names.Executions.String(), f.m.names.Receipts.String(), f.m.names.Logs.String(), f.m.names.Evidence.String(), f.m.names.Approvals.String(), f.m.names.CommentThreads.String(), f.m.names.CommentMessages.String(), f.m.names.CommentEvents.String(), "slots", "effects"} {
						var rows []map[string]any
						require.NoError(t, f.db.Table(table).Order("rowid").Find(&rows).Error)
						out[table] = rows
					}
					return out
				}
				before := snapshot()
				count := 0
				injected := errors.New("execution transactional storage unavailable")
				hook := func(tx *gorm.DB) {
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch kind {
				case "query":
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("execution_transaction_failure", hook))
				case "create":
					require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("execution_transaction_failure", hook))
				case "update":
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("execution_transaction_failure", hook))
				}
				err := f.db.Transaction(func(tx *gorm.DB) error {
					_, e := f.m.ReanchorArtifactCommentOn(f.human, tx, 7, thread.ID, 1, ArtifactCommentMutation{EvidenceID: evidence})
					return e
				})
				require.NoError(t, f.db.Callback().Query().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Create().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Update().Remove("execution_transaction_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				require.Equal(t, before, snapshot(), "failed command must roll back execution, journals, slots and effects")
				return count
			}
			total := 0
			t.Run(command+"/"+kind+"/success", func(t *testing.T) { total = exercise(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(fmt.Sprintf("%s/%s/failure_%d", command, kind, at), func(t *testing.T) { require.GreaterOrEqual(t, exercise(t, at), at) })
			}
		}
	}
}

func TestRelocatePinnedTableFormatsAndInvalidLocators(t *testing.T) {
	for _, ext := range []string{"csv", "tsv"} {
		for _, change := range []string{"unique", "duplicate", "missing", "malformed", "header_only"} {
			t.Run(ext+"/"+change, func(t *testing.T) {
				sep := ","
				if ext == "tsv" {
					sep = "\t"
				}
				oldData := "name" + sep + "value\na" + sep + "1\nb" + sep + "2"
				pin := func(data string, version guid.ID) files.PinnedResourceRead {
					return files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 1, FileID: 2, VersionID: version, SHA256: hash([]byte(data)), Name: "table." + ext}, Reader: io.NopCloser(strings.NewReader(data))}
				}
				idx := 1
				old, e := internalArtifactAnchor(pin(oldData, 3), ArtifactFact{BindingID: 1, FileID: 2, VersionID: 3, SHA256: hash([]byte(oldData))}, ArtifactCommentCreate{AnchorKind: "TABLE_ROW", RowIndex: &idx})
				require.NoError(t, e)
				data := "name" + sep + "value\nx" + sep + "0\na" + sep + "1\nb" + sep + "2"
				switch change {
				case "duplicate":
					data += "\nb" + sep + "2"
				case "missing":
					data = oldData[:len(oldData)-3]
				case "malformed":
					data = "name,value\n\"broken"
				case "header_only":
					data = "name" + sep + "value"
				}
				mapped, e := relocatePinnedComment(old, pin(data, 4), ArtifactFact{BindingID: 1, FileID: 2, VersionID: 4, SHA256: hash([]byte(data))})
				if change == "malformed" || change == "header_only" {
					require.Error(t, e)
					require.Nil(t, mapped)
				} else {
					require.NoError(t, e)
					if change == "unique" {
						require.NotNil(t, mapped)
						require.Equal(t, old.Excerpt, mapped.Excerpt)
					} else {
						require.Nil(t, mapped)
					}
				}
			})
		}
	}
	for _, kind := range []string{"TEXT_PARAGRAPH", "TABLE_ROW", "UNKNOWN"} {
		for _, locator := range []string{"{", "{}"} {
			data := "unchanged"
			pin := files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 1, FileID: 2, VersionID: 4, SHA256: hash([]byte(data)), Name: "source.json"}, Reader: io.NopCloser(strings.NewReader(data))}
			out, e := relocatePinnedComment(delivery.ArtifactAnchorSnapshot{Kind: kind, FileID: 2, Locator: json.RawMessage(locator)}, pin, ArtifactFact{BindingID: 1, FileID: 2, VersionID: 4, SHA256: hash([]byte(data))})
			require.Error(t, e)
			require.Nil(t, out)
		}
	}
}

func TestReanchorCommandVerifiesTargetPinAndPreservesOriginalSnapshot(t *testing.T) {
	for _, fault := range []string{"relocated", "outdated", "invalid", "open", "reader", "purpose", "resource", "read", "close", "hash", "file", "snapshot", "fact"} {
		t.Run(fault, func(t *testing.T) {
			f, execution, evidence := pinnedCommentFixture(t)
			var thread ArtifactCommentView
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				v, e := f.m.CreateArtifactCommentOn(f.human, tx, 7, execution, ArtifactCommentCreate{EvidenceID: evidence, StartLine: 2, EndLine: 2, Body: "Pinned line"})
				thread = v
				return e
			}))
			var source model.Evidence
			require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Where("id=?", evidence).Take(&source).Error)
			var fact ArtifactFact
			require.NoError(t, json.Unmarshal([]byte(source.Content), &fact))
			data := "first\nsecond\n"
			if fault == "relocated" {
				data = "inserted\nfirst\nsecond\n"
				fact.VersionID++
			}
			if fault == "outdated" {
				data = "first\nchanged\n"
				fact.VersionID++
			}
			fact.SHA256 = hash([]byte(data))
			if fault == "file" {
				fact.FileID++
			}
			raw, e := json.Marshal(fact)
			require.NoError(t, e)
			if fault == "fact" {
				raw = []byte("{")
			}
			require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Where("id=?", evidence).Update("content", string(raw)).Error)
			if fault == "snapshot" {
				require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Where("id=?", thread.ID).Update("anchor_snapshot", "{").Error)
			}
			reader := &artifactAnchorReader{Reader: strings.NewReader(data)}
			failure := errors.New("target pin unavailable")
			pin := files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: fact.BindingID, FileID: fact.FileID, VersionID: fact.VersionID, SHA256: fact.SHA256, Purpose: "execution_evidence", ResourceID: evidence}, Reader: reader}
			pins := artifactSourcePins{pin: pin}
			switch fault {
			case "open":
				pins.err = failure
			case "reader":
				pins.pin.Reader = nil
			case "purpose":
				pins.pin.Purpose = "other"
			case "resource":
				pins.pin.ResourceID++
			case "read":
				reader.Reader = iotest.ErrReader(failure)
			case "close":
				reader.closeErr = failure
			case "hash":
				reader.Reader = strings.NewReader("corrupted")
			}
			f.m.commentFiles = pins
			target := evidence
			if fault == "invalid" {
				target = 0
			}
			outErr := f.db.Transaction(func(tx *gorm.DB) error {
				out, e := f.m.ReanchorArtifactCommentOn(f.human, tx, 7, thread.ID, 1, ArtifactCommentMutation{EvidenceID: target})
				if e == nil {
					expected := "RELOCATED"
					if fault == "outdated" {
						expected = "OUTDATED"
					}
					require.Equal(t, expected, out.AnchorStatus)
				}
				return e
			})
			if fault == "relocated" || fault == "outdated" {
				require.NoError(t, outErr)
			} else {
				require.Error(t, outErr)
			}
			var stored model.ArtifactCommentThread
			require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Where("id=?", thread.ID).Take(&stored).Error)
			if fault != "snapshot" {
				var original delivery.ArtifactAnchorSnapshot
				require.NoError(t, json.Unmarshal([]byte(stored.AnchorSnapshot), &original))
				require.Equal(t, thread.Anchor, original)
			}
			if outErr != nil {
				require.Equal(t, 1, stored.Version)
			}
		})
	}
}
