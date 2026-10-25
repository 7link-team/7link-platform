package clientwork

import (
	"context"
	"errors"
	"mime"
	"path"
	"slices"
	"strconv"
	"strings"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const answerAttachmentPurpose = "clientwork.clarification_answer"
const answerAttachmentLimit int64 = 25 * 1024 * 1024

// ID is an opaque attachment handle. Storage keys, object rows and credentials
// are never part of the persisted/public answer projection.
type AnswerAttachment struct {
	ID        guid.ID `json:"id"`
	FileID    guid.ID `json:"file_id"`
	VersionID guid.ID `json:"version_id"`
	Name      string  `json:"name"`
	MIME      string  `json:"mime"`
	Size      int64   `json:"size"`
	SHA256    string  `json:"sha256"`
}

func (m *Module) answerAttachmentResourceOn(ctx context.Context, tx *gorm.DB, id guid.ID, owner files.ResourceOwner) error {
	p := authn.EntityIDFromContext(ctx)
	if owner.Scope != "entity" || owner.ID != p {
		return ErrNotFound
	}
	row, err := m.loadClarificationOn(ctx, tx, p, id, false)
	if err != nil {
		return err
	}
	// The canonical reader and own-submitter are existing independent grants.
	// Only an explicit authorization denial may try the other grant; a storage
	// failure must never be converted into a successful alternate read.
	external, err := m.authorize(ctx, tx, p, "channel_read", true)
	if err == nil {
		if external && row.RecipientID != authn.PrincipalIDFromContext(ctx) {
			return ErrNotFound
		}
		return nil
	}
	if !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) && !errors.Is(err, delivery.ErrProjectForbidden) && !errors.Is(err, delivery.ErrWorkForbidden) {
		return err
	}
	source, _, err := m.ownedSubmissionOn(ctx, tx, row.IntakeID)
	if err != nil {
		return err
	}
	if source.ProjectID != p {
		return ErrNotFound
	}
	return nil
}

func permittedAnswerAttachment(name, contentType string) bool {
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(strings.TrimRight(strings.ReplaceAll(name, "\\", "/"), " ."))), ".")
	if slices.Contains([]string{"exe", "dll", "msi", "bat", "cmd", "com", "scr", "ps1", "vbs", "js", "jar", "apk", "app", "dmg", "sh"}, ext) {
		return false
	}
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return !slices.Contains([]string{"application/x-msdownload", "application/x-msdos-program", "application/x-dosexec", "application/vnd.microsoft.portable-executable", "application/x-msi", "application/x-ms-installer", "application/x-bat", "application/x-powershell", "text/x-powershell", "application/x-vbs", "application/x-vbscript", "text/vbscript", "application/javascript", "application/x-javascript", "text/javascript", "application/ecmascript", "text/ecmascript", "application/java-archive", "application/x-java-archive", "application/vnd.android.package-archive", "application/x-apple-diskimage", "application/x-mach-binary", "application/x-mach-o", "application/x-sh", "text/x-sh", "application/x-shellscript", "text/x-shellscript", "application/x-executable", "application/x-sharedlib"}, strings.ToLower(media))
}

func (m *Module) pinAnswerAttachmentsOn(ctx context.Context, tx *gorm.DB, round guid.ID, question int, ids []guid.ID) ([]AnswerAttachment, error) {
	if len(ids) > 5 {
		return nil, ErrInvalid
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if m.answerFiles == nil {
		return nil, ErrUnavailable
	}
	seen := map[guid.ID]bool{}
	candidates := make([]files.PinnedCandidate, 0, len(ids))
	var total int64
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, ErrInvalid
		}
		seen[id] = true
		file, err := m.answerFiles.InspectOn(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if file.FileID != id || file.VersionID <= 0 || file.NodeVersion < 1 {
			return nil, ErrConflict
		}
		if file.Size < 0 || file.Size > answerAttachmentLimit || total > answerAttachmentLimit-file.Size || !permittedAnswerAttachment(file.Name, file.MIME) {
			return nil, ErrInvalid
		}
		total += file.Size
		candidates = append(candidates, file)
	}
	out := make([]AnswerAttachment, 0, len(candidates))
	for _, file := range candidates {
		id, err := m.nextID()
		if err != nil {
			return nil, err
		}
		pinned, err := m.answerFiles.PinOn(ctx, tx, files.ResourceBindingInput{ID: id, SpaceID: file.SpaceID, FileID: file.FileID, ExpectedFileVersion: file.NodeVersion, VersionID: &file.VersionID, Purpose: answerAttachmentPurpose, ResourceKind: "clarification_round", ResourceID: round, Slot: "answer." + strconv.Itoa(question) + "." + file.FileID.String(), ActorID: authn.PrincipalIDFromContext(ctx)})
		if err != nil {
			return nil, err
		}
		if pinned.BindingID != id || pinned.FileID != file.FileID || pinned.VersionID != file.VersionID || pinned.Size != file.Size || pinned.SHA256 != file.SHA256 || pinned.MIME != file.MIME || pinned.Name != file.Name {
			return nil, ErrConflict
		}
		out = append(out, AnswerAttachment{ID: id, FileID: file.FileID, VersionID: file.VersionID, Name: file.Name, MIME: file.MIME, Size: file.Size, SHA256: file.SHA256})
	}
	return out, nil
}

type SubmissionAttachmentInput struct {
	ID           string `path:"id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	RoundID      string `path:"round_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	QuestionNo   int    `path:"question_no" minimum:"1" maximum:"10"`
	AttachmentID string `path:"attachment_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type ClarificationAttachmentInput struct {
	ObjectInput
	QuestionNo   int    `path:"question_no" minimum:"1" maximum:"10"`
	AttachmentID string `path:"attachment_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func (m *Module) submissionAttachmentHTTP(ctx context.Context, in *SubmissionAttachmentInput) (*huma.StreamResponse, error) {
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	round, e := guid.RequireID(in.RoundID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.answerAttachmentContent(ctx, in.QuestionNo, in.AttachmentID, func(tx *gorm.DB) (context.Context, *model.ClarificationRound, error) {
		source, projectCtx, e := m.ownedSubmissionOn(ctx, tx, id)
		if e != nil {
			return ctx, nil, e
		}
		row, e := m.loadClarificationOn(projectCtx, tx, source.ProjectID, round, false)
		if e == nil && row.IntakeID != id {
			e = ErrNotFound
		}
		return projectCtx, row, e
	})
}

func (m *Module) clarificationAttachmentHTTP(ctx context.Context, in *ClarificationAttachmentInput) (*huma.StreamResponse, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.answerAttachmentContent(ctx, in.QuestionNo, in.AttachmentID, func(tx *gorm.DB) (context.Context, *model.ClarificationRound, error) {
		external, e := m.authorize(ctx, tx, p, "channel_read", true)
		if e != nil {
			return ctx, nil, e
		}
		row, e := m.loadClarificationOn(ctx, tx, p, id, external)
		return ctx, row, e
	})
}

func (m *Module) answerAttachmentContent(ctx context.Context, question int, rawID string, find func(*gorm.DB) (context.Context, *model.ClarificationRound, error)) (*huma.StreamResponse, error) {
	id, e := guid.RequireID(rawID)
	if e != nil || question < 1 || question > 10 {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.answerFiles == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var download files.PinnedResourceRead
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		readCtx, row, e := find(tx)
		if e != nil {
			return e
		}
		view, e := clarificationView(row)
		if e != nil {
			return e
		}
		if question > len(view.Questions) {
			return ErrNotFound
		}
		for _, attachment := range view.Questions[question-1].Attachments {
			if attachment.ID != id {
				continue
			}
			download, e = m.answerFiles.OpenOn(readCtx, tx, id)
			if e != nil {
				return e
			}
			if download.Reader == nil || download.BindingID != id || download.ResourceID != row.ID || download.Purpose != answerAttachmentPurpose || download.ResourceKind != "clarification_round" || download.FileID != attachment.FileID || download.VersionID != attachment.VersionID || download.Size != attachment.Size || download.SHA256 != attachment.SHA256 || download.MIME != attachment.MIME || download.Name != attachment.Name {
				return ErrConflict
			}
			return nil
		}
		return ErrNotFound
	})
	if e != nil {
		if download.Reader != nil {
			_ = download.Reader.Close()
		}
		return nil, httpError(ctx, e)
	}
	return pinnedContentResponse(download, false), nil
}
