package execution

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// relocatePinnedComment only maps unchanged, unambiguous source text. In the
// absence of a commit diff or live DOM, ambiguity stays visibly OUTDATED.
func relocatePinnedComment(original delivery.ArtifactAnchorSnapshot, pin files.PinnedResourceRead, fact ArtifactFact) (*delivery.ArtifactAnchorSnapshot, error) {
	if pin.Reader == nil {
		return nil, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(pin.Reader, 4*1024*1024+1))
	if err = errors.Join(err, pin.Reader.Close()); err != nil {
		return nil, err
	}
	if len(data) > 4*1024*1024 || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) || hash(data) != fact.SHA256 || pin.SHA256 != fact.SHA256 || pin.VersionID != fact.VersionID || pin.FileID != original.FileID || pin.BindingID != fact.BindingID {
		return nil, ErrInvalidArgument
	}
	in := ArtifactCommentCreate{AnchorKind: original.Kind}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	switch original.Kind {
	case "CODE_LINES":
		old := strings.Split(original.Excerpt, "\n")
		lines := strings.Split(text, "\n")
		matches := 0
		for i := 0; i+len(old) <= len(lines); i++ {
			if strings.Join(lines[i:i+len(old)], "\n") == original.Excerpt {
				matches++
				in.StartLine = i + 1
				in.EndLine = i + len(old)
			}
		}
		if matches != 1 {
			return nil, nil
		}
	case "TEXT_PARAGRAPH":
		var locator struct {
			SHA string `json:"paragraph_sha256"`
		}
		if json.Unmarshal(original.Locator, &locator) != nil || locator.SHA == "" {
			return nil, ErrUnavailable
		}
		matches := 0
		for i, p := range clientParagraphBreak.Split(strings.TrimSpace(text), -1) {
			if hash([]byte(p)) == locator.SHA {
				matches++
				idx := i
				in.ParagraphIndex = &idx
				in.ParagraphSHA256 = locator.SHA
				in.TextQuote = original.Excerpt
			}
		}
		if matches != 1 {
			return nil, nil
		}
	case "TABLE_ROW":
		var locator struct {
			SHA string `json:"row_sha256"`
		}
		if json.Unmarshal(original.Locator, &locator) != nil || locator.SHA == "" {
			return nil, ErrUnavailable
		}
		var rows []json.RawMessage
		ext := strings.ToLower(filepath.Ext(pin.Name))
		if ext == ".json" || pin.MIME == "application/json" {
			if json.Unmarshal(data, &rows) != nil {
				return nil, ErrInvalidArgument
			}
			for i, row := range rows {
				var value any
				decoder := json.NewDecoder(strings.NewReader(string(row)))
				decoder.UseNumber()
				if decoder.Decode(&value) != nil {
					return nil, ErrInvalidArgument
				}
				rows[i], err = json.Marshal(value)
				if err != nil {
					return nil, err
				}
			}
		} else {
			r := csv.NewReader(strings.NewReader(text))
			if ext == ".tsv" || pin.MIME == "text/tab-separated-values" {
				r.Comma = '\t'
			}
			table, e := r.ReadAll()
			if e != nil || len(table) < 2 {
				return nil, ErrInvalidArgument
			}
			for _, row := range table[1:] {
				raw, e := json.Marshal(row)
				if e != nil {
					return nil, e
				}
				rows = append(rows, raw)
			}
		}
		matches := 0
		for i, row := range rows {
			if hash(row) == locator.SHA {
				matches++
				idx := i
				in.RowIndex = &idx
				in.RowSHA256 = locator.SHA
			}
		}
		if matches != 1 {
			return nil, nil
		}
	case "HTML_REGION":
		return nil, nil // Stored rectangles have RECT_ONLY precision.
	default:
		return nil, ErrInvalidArgument
	}
	pin.Reader = io.NopCloser(strings.NewReader(string(data)))
	anchor, err := internalArtifactAnchor(pin, fact, in)
	if err != nil {
		return nil, err
	}
	return &anchor, nil
}

func (m *Module) ReanchorArtifactCommentOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, expected int64, in ArtifactCommentMutation) (ArtifactCommentMutationReceipt, error) {
	var empty ArtifactCommentMutationReceipt
	thread, row, kind, err := m.lockCommentMutationOn(ctx, tx, tenant, id, in.SeenSeq)
	if err != nil {
		return empty, err
	}
	if int64(thread.Version) != expected {
		return empty, ErrConflict
	}
	if in.EvidenceID <= 0 || m.commentFiles == nil {
		return empty, ErrInvalidArgument
	}
	q := query.BindEvidence(tx, m.names)
	evidence, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(thread.ProjectID), q.ExecutionID.Eq(thread.ExecutionID), q.ID.Eq(in.EvidenceID), q.Kind.Eq("FILE")).First()
	if err != nil {
		return empty, err
	}
	var original delivery.ArtifactAnchorSnapshot
	var fact ArtifactFact
	if json.Unmarshal([]byte(thread.AnchorSnapshot), &original) != nil || json.Unmarshal([]byte(evidence.Content), &fact) != nil {
		return empty, ErrUnavailable
	}
	if fact.FileID != original.FileID {
		return empty, ErrInvalidArgument
	}
	status := "CURRENT"
	var relocated *delivery.ArtifactAnchorSnapshot
	// Always open the target pin, even a current version: caller cannot assert
	// source availability, tenant scope or content identity through evidence JSON.
	readCtx := context.WithValue(ctx, artifactCommentReadKey{}, artifactCommentRead{tenant, thread.ProjectID, evidence.ID})
	pin, err := m.commentFiles.OpenOn(readCtx, tx, fact.BindingID)
	if err != nil {
		return empty, err
	}
	if pin.ResourceID != evidence.ID || pin.Purpose != "execution_evidence" {
		if pin.Reader != nil {
			return empty, errors.Join(ErrForbidden, pin.Reader.Close())
		}
		return empty, ErrForbidden
	}
	if fact.VersionID == original.VersionID && fact.SHA256 == original.SHA256 {
		if pin.Reader == nil {
			return empty, ErrUnavailable
		}
		data, e := io.ReadAll(io.LimitReader(pin.Reader, 4*1024*1024+1))
		if e = errors.Join(e, pin.Reader.Close()); e != nil {
			return empty, e
		}
		if len(data) > 4*1024*1024 || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) || pin.BindingID != fact.BindingID || hash(data) != original.SHA256 || pin.FileID != original.FileID || pin.VersionID != original.VersionID || pin.SHA256 != original.SHA256 {
			return empty, ErrConflict
		}
	} else {
		status = "OUTDATED"
		relocated, err = relocatePinnedComment(original, pin, fact)
		if err != nil {
			return empty, err
		}
		if relocated != nil {
			status = "RELOCATED"
		}
	}
	var encoded *string
	if relocated != nil {
		raw, e := json.Marshal(relocated)
		if e != nil {
			return empty, e
		}
		value := string(raw)
		encoded = &value
	}
	if err = m.appendCommentLifecycleOn(ctx, tx, thread, row, kind, "comment_anchor_checked", map[string]any{"anchor_checked_version": fact.VersionID, "relocated_anchor": encoded, "anchor_status": status}); err != nil {
		return empty, err
	}
	return ArtifactCommentMutationReceipt{ThreadID: id, LastSeq: expected + 1, AnchorStatus: status}, nil
}
