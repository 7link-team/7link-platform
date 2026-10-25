package execution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type artifactFiles struct {
	stage      func(context.Context, files.ExecutionArtifactAuthority, string, string, string, io.Reader, int64) (*files.PreparedExecutionArtifact, error)
	attach     func(context.Context, *gorm.DB, *files.PreparedExecutionArtifact, guid.ID) (files.PinnedResource, error)
	references func(context.Context, *gorm.DB, files.ExecutionArtifactAuthority, func(context.Context) error) error
}

func (m *Module) injectArtifactFiles(s *module.Services) error {
	p, err := module.Resolve[*files.ExecutionArtifactFiles](s)
	if err != nil {
		return err
	}
	if err = p.BindAuthorization(m.authorizeArtifactOn); err != nil {
		return err
	}
	m.artifacts = artifactFiles{stage: p.Stage, attach: p.AttachOn, references: p.WithReferencesOn}
	return nil
}
func artifactAuthority(tenant, project, id guid.ID, f RunnerFence, in ReceiptInput) files.ExecutionArtifactAuthority {
	return files.ExecutionArtifactAuthority{TenantID: tenant, EntityID: project, ExecutionID: id, MachineID: f.MachineID, SessionID: f.SessionID, ConnEpoch: f.ConnEpoch, DispatchEpoch: in.DispatchEpoch, Seq: in.Seq}
}
func (m *Module) authorizeArtifactOn(ctx context.Context, tx *gorm.DB, a files.ExecutionArtifactAuthority, purpose string, resourceID guid.ID, owner files.ResourceOwner) error {
	if owner.Scope != "entity" || owner.ID <= 0 {
		return ErrForbidden
	}
	if a.VerificationTaskID > 0 {
		return m.authorizeVisualArtifactOn(ctx, tx, a, purpose, resourceID, owner)
	}
	// Ordinary Human reads of execution-managed content retain execution authority.
	if a.ExecutionID == 0 {
		var tenant, project, id guid.ID
		if purpose == "execution_log" {
			q := query.BindLogs(tx, m.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(resourceID)).First()
			if err != nil {
				return err
			}
			tenant, id = row.TenantID, row.ExecutionID
		} else {
			q := query.BindEvidence(tx, m.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(resourceID)).First()
			if err != nil {
				return err
			}
			tenant, id = row.TenantID, row.ExecutionID
		}
		row, err := m.rowOn(ctx, tx, tenant, id, false)
		if err != nil {
			return err
		}
		project = row.ProjectID
		if project != owner.ID {
			return ErrForbidden
		}
		if manual, _ := ctx.Value(manualEvidenceReadKey{}).(bool); manual && purpose == "execution_evidence" {
			_, err := m.commentAuthority(ctx, tx, tenant, project, "evidence_submit")
			return err
		}
		if read, ok := ctx.Value(artifactCommentReadKey{}).(artifactCommentRead); ok && purpose == "execution_evidence" && read.Tenant == tenant && read.Project == project && read.Evidence == resourceID {
			_, err := m.commentAuthority(ctx, tx, tenant, project, "work_item_write")
			return err
		}
		readErr := m.humanAuthority(ctx, tx, tenant, project, "execution_read")
		if !errors.Is(readErr, ErrForbidden) || purpose != "execution_evidence" {
			return readErr
		}
		return m.authorizeVisualReviewerReadOn(ctx, tx, row, resourceID)
	}
	row, err := m.rowOn(ctx, tx, a.TenantID, a.ExecutionID, false)
	if err != nil {
		return err
	}
	if err = m.workPrincipalLocks(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)), authn.PrincipalIDFromContext(ctx), row.InitiatorID); err != nil {
		return err
	}
	if m.work.AuthorizeOn == nil {
		return ErrCapabilityUnavailable
	}
	if err = m.work.AuthorizeOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)), "RECEIPT"); err != nil {
		return err
	}
	if err = m.validateRunner(ctx, tx, a.TenantID, RunnerFence{MachineID: a.MachineID, SessionID: a.SessionID, ConnEpoch: a.ConnEpoch}); err != nil {
		return err
	}
	row, err = m.rowOn(ctx, tx, a.TenantID, a.ExecutionID, true)
	if err != nil {
		return err
	}
	if row.ProjectID != a.EntityID || owner.ID != row.ProjectID || row.MachineID != a.MachineID || row.DispatchEpoch != a.DispatchEpoch || row.State != "RUNNING" || row.CancelRequestedAt != nil || row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(time.Now()) || a.Seq != row.LastSeq+1 {
		return ErrFenced
	}
	if resourceID > 0 {
		if purpose == "execution_log" {
			q := query.BindLogs(tx, m.names)
			_, err = q.WithContext(ctx).Where(q.ID.Eq(resourceID), q.ExecutionID.Eq(a.ExecutionID), q.TenantID.Eq(a.TenantID)).First()
		} else {
			q := query.BindEvidence(tx, m.names)
			_, err = q.WithContext(ctx).Where(q.ID.Eq(resourceID), q.ExecutionID.Eq(a.ExecutionID), q.ProjectID.Eq(a.EntityID), q.TenantID.Eq(a.TenantID)).First()
		}
	}
	return err
}

type ArtifactUpload struct {
	Name        string   `json:"name" minLength:"1" maxLength:"200"`
	Data        []byte   `json:"data" doc:"Base64-encoded immutable artifact bytes, at most 64 KiB."`
	CriterionID *guid.ID `json:"criterion_id,omitempty"`
	Coverage    string   `json:"coverage" enum:"COVERED,PARTIAL,NOT_APPLICABLE"`
	Note        string   `json:"note" maxLength:"2000"`
}

// PrepareReceipt stages storage before opening the state-changing receipt
// transaction. Replayed receipts skip staging; ReceiptOn still checks the full
// original request digest under its execution lock.
func (m *Module) PrepareReceipt(ctx context.Context, tenant, id guid.ID, f RunnerFence, in ReceiptInput) (ReceiptInput, error) {
	if in.Upload != nil && (in.Kind != "EVIDENCE" || in.EvidenceKind != "FILE") {
		return in, ErrInvalidArgument
	}
	if in.Kind == "EVIDENCE" && in.EvidenceKind == "FILE" {
		if in.Upload == nil || !slices.Contains([]string{"COVERED", "PARTIAL", "NOT_APPLICABLE"}, in.Upload.Coverage) || !utf8.ValidString(in.Upload.Note) || utf8.RuneCountInString(in.Upload.Note) > 2000 || (in.Upload.Coverage == "NOT_APPLICABLE" && strings.TrimSpace(in.Upload.Note) == "") || (in.Upload.CriterionID != nil && *in.Upload.CriterionID <= 0) {
			return in, ErrInvalidArgument
		}
		if _, _, err := files.PortableName(in.Upload.Name); err != nil || len(in.Upload.Data) == 0 || len(in.Upload.Data) > 65536 {
			return in, ErrInvalidArgument
		}
	}
	if in.Kind != "LOG" && in.Kind != "EVIDENCE" {
		return in, nil
	}
	db, err := m.writer()
	if err != nil {
		return in, err
	}
	if tenant <= 0 || id <= 0 {
		return in, ErrInvalidArgument
	}
	q := query.BindExecutions(db, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return in, err
	}
	if row.BranchReason == "HANDOVER" {
		if in.Kind != "LOG" || in.Upload != nil {
			return in, ErrInvalidArgument
		}
		return in, nil
	}
	if m.artifacts.stage == nil {
		return in, ErrCapabilityUnavailable
	}
	r := query.BindReceipts(db, m.names)
	prior, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ExecutionID.Eq(id), r.DispatchEpoch.Eq(in.DispatchEpoch), r.Seq.Eq(in.Seq)).Count()
	if err != nil {
		return in, err
	}
	if prior > 0 {
		return in, nil
	}
	content := []byte(in.Content)
	name, purpose := "evidence.txt", "execution_evidence"
	if in.Kind == "LOG" {
		name, purpose = "execution.log", "execution_log"
	}
	if in.EvidenceKind == "FILE" {
		if in.Upload == nil || len(in.Upload.Data) == 0 {
			return in, ErrInvalidArgument
		}
		content, name = in.Upload.Data, in.Upload.Name
	}
	if len(content) == 0 || len(content) > 65536 {
		return in, ErrInvalidArgument
	}
	in.prepared, err = m.artifacts.stage(ctx, artifactAuthority(tenant, row.ProjectID, id, f, in), purpose, name, fmt.Sprintf("receipt.%d.%d", in.DispatchEpoch, in.Seq), bytes.NewReader(content), int64(len(content)))
	return in, err
}
