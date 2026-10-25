package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
	"strings"
	"time"
)

type ChangeBody struct {
	SheetID      guid.ID `json:"sheet_id"`
	BaseRevision int     `json:"base_revision" minimum:"1"`
	Description  string  `json:"description" minLength:"1" maxLength:"2000"`
}
type ChangeAction struct {
	Proposal     *ClientChangeProposal `json:"proposal,omitempty"`
	Action       string                `json:"action" enum:"ASSESS,CONFIRM,REJECT"`
	Impact       string                `json:"impact,omitempty" maxLength:"8000"`
	ProposalHash string                `json:"proposal_hash,omitempty" pattern:"^([a-f0-9]{64})?$"`
}
type ChangeView struct {
	Proposal     *ClientChangePublic `json:"proposal,omitempty"`
	Applied      bool                `json:"applied"`
	ID           guid.ID             `json:"id"`
	SheetID      guid.ID             `json:"sheet_id"`
	BaseRevision int                 `json:"base_revision"`
	Description  string              `json:"description"`
	State        string              `json:"state"`
	Impact       string              `json:"impact"`
	ProposalHash string              `json:"proposal_hash"`
	Version      int                 `json:"version"`
}

func changeView(r *model.ChangeRequest) ChangeView {
	out := ChangeView{ID: r.ID, SheetID: r.SheetID, BaseRevision: r.BaseRevision, Description: r.Description, State: r.State, Impact: r.Impact, ProposalHash: r.ProposalHash, Version: r.Version, Applied: r.AppliedChangeID != nil}
	if r.Proposal != nil {
		var p ClientChangeProposal
		if json.Unmarshal([]byte(*r.Proposal), &p) == nil {
			out.Proposal = &ClientChangePublic{ToleranceDelta: p.ToleranceDelta, RequirementID: p.RequirementID, BaseVersion: p.Change.BaseVersionNo, Title: p.Change.Title, Description: p.Change.Description, Priority: p.Change.Priority, Criteria: p.Change.Criteria}
		}
	}
	return out
}
func (m *Module) createChangeOn(ctx context.Context, tx *gorm.DB, p guid.ID, in ChangeBody) (Receipt, error) {
	if !validText(in.Description, 2000) || in.SheetID <= 0 || in.BaseRevision < 1 {
		return Receipt{}, ErrInvalid
	}
	if ext, e := m.authorize(ctx, tx, p, "requirement_read", true); e != nil {
		return Receipt{}, e
	} else if !ext {
		return Receipt{}, ErrForbidden
	}
	sheet, e := m.loadSheet(ctx, tx, p, in.SheetID)
	if e != nil {
		return Receipt{}, e
	}
	v, e := m.sheetView(ctx, tx, sheet, true, in.BaseRevision)
	if e != nil {
		return Receipt{}, e
	}
	if v.State != "SIGNED" || sheet.PublishedRevision != in.BaseRevision {
		return Receipt{}, ErrConflict
	}
	id, e := m.nextID()
	if e != nil {
		return Receipt{}, e
	}
	row := &model.ChangeRequest{TenantID: authn.TenantIDFromContext(ctx), ID: id, ProjectID: p, SheetID: in.SheetID, BaseRevision: in.BaseRevision, Description: in.Description, State: "REQUESTED", ActorID: authn.PrincipalIDFromContext(ctx), Version: 1, CreatedAt: now(), UpdatedAt: now()}
	if e = query.BindChangeRequest(tx, m.names).WithContext(ctx).Create(row); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "change_requested", "channel_read", 1); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: 1, State: row.State}, nil
}
func (m *Module) changeOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, external bool) (*model.ChangeRequest, error) {
	q := query.BindChangeRequest(tx, m.names)
	r := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id))
	if external {
		r = r.Where(q.ActorID.Eq(authn.PrincipalIDFromContext(ctx)))
	}
	row, e := r.First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return row, e
}
func (m *Module) actChangeOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, in ChangeAction) (Receipt, error) {
	ext, e := m.authorize(ctx, tx, p, "requirement_write", true)
	if e != nil {
		return Receipt{}, e
	}
	row, e := m.changeOn(ctx, tx, p, id, ext)
	if e != nil {
		return Receipt{}, e
	}
	if row.Version != v {
		return Receipt{}, ErrConflict
	}
	if in.Action == "ASSESS" {
		if ext {
			return Receipt{}, ErrForbidden
		}
		if row.State != "REQUESTED" || !validText(in.Impact, 8000) {
			return Receipt{}, ErrConflict
		}
		row.Impact = in.Impact
		if in.Proposal != nil {
			if e = m.validateChangeProposalOn(ctx, tx, p, row, in.Proposal); e != nil {
				return Receipt{}, e
			}
			data, e := json.Marshal(in.Proposal)
			if e != nil {
				return Receipt{}, e
			}
			text := string(data)
			row.Proposal = &text
		}
		data, _ := json.Marshal(struct {
			Impact   string
			Proposal *string
		}{in.Impact, row.Proposal})
		digest := sha256.Sum256(data)
		row.ProposalHash = hex.EncodeToString(digest[:])
		row.State = "AWAITING_CONFIRMATION"
	} else if in.Action == "CONFIRM" || in.Action == "REJECT" {
		if !ext || row.ActorID != authn.PrincipalIDFromContext(ctx) {
			return Receipt{}, ErrForbidden
		}
		if row.State != "AWAITING_CONFIRMATION" || in.ProposalHash != row.ProposalHash {
			return Receipt{}, ErrConflict
		}
		if in.Action == "CONFIRM" {
			row.State = "CONFIRMED"
		} else {
			row.State = "REJECTED"
		}
	} else {
		return Receipt{}, ErrInvalid
	}
	row.Version++
	row.UpdatedAt = now()
	q := query.BindChangeRequest(tx, m.names)
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id)).Select(q.Impact, q.ProposalHash, q.Proposal, q.State, q.Version, q.UpdatedAt).Updates(row); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "change_decided", "channel_read", row.Version); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: row.Version, State: row.State}, nil
}

type ArtifactAnchor struct {
	Kind  string  `json:"kind" enum:"TEXT,IMAGE,PAGE"`
	Start int     `json:"start" minimum:"0"`
	End   int     `json:"end" minimum:"0"`
	Page  int     `json:"page" minimum:"0"`
	X     float64 `json:"x" minimum:"0" maximum:"1"`
	Y     float64 `json:"y" minimum:"0" maximum:"1"`
}

func (a ArtifactAnchor) valid() bool {
	switch a.Kind {
	case "TEXT":
		return a.Start >= 0 && a.End > a.Start && a.End <= 10000000
	case "IMAGE":
		return a.X >= 0 && a.X <= 1 && a.Y >= 0 && a.Y <= 1
	case "PAGE":
		return a.Page >= 1 && a.Page <= 100000
	}
	return false
}

type IssueBody struct {
	Title       string          `json:"title" minLength:"1" maxLength:"200"`
	Description string          `json:"description" minLength:"1" maxLength:"8000"`
	PreviewID   *guid.ID        `json:"preview_id,omitempty"`
	Anchor      *ArtifactAnchor `json:"anchor,omitempty"`
}
type IssueAction struct {
	State            string   `json:"state" enum:"TRIAGED,RESPONDED,CLOSED"`
	Reply            string   `json:"reply,omitempty" maxLength:"8000"`
	InternalDefectID *guid.ID `json:"internal_defect_id,omitempty"`
}
type ReplyView struct {
	ID   guid.ID `json:"id"`
	Body string  `json:"body"`
}
type IssueView struct {
	RepliesHasMore    bool            `json:"replies_has_more"`
	RepliesNextCursor string          `json:"replies_next_cursor,omitempty"`
	ID                guid.ID         `json:"id"`
	Title             string          `json:"title"`
	Description       string          `json:"description"`
	State             string          `json:"state"`
	PreviewID         *guid.ID        `json:"preview_id,omitempty"`
	Anchor            *ArtifactAnchor `json:"anchor,omitempty"`
	Version           int             `json:"version"`
	Replies           []ReplyView     `json:"replies"`
}

func (m *Module) issueOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, external bool) (*model.ClientIssue, error) {
	q := query.BindClientIssue(tx, m.names)
	r := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id))
	if external {
		r = r.Where(q.ActorID.Eq(authn.PrincipalIDFromContext(ctx)))
	}
	row, e := r.First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return row, e
}
func (m *Module) issueView(ctx context.Context, tx *gorm.DB, row *model.ClientIssue, replyCursor string) (IssueView, error) {
	out := IssueView{ID: row.ID, Title: row.Title, Description: row.Description, State: row.State, PreviewID: row.ArtifactID, Version: row.Version, Replies: []ReplyView{}}
	if row.Anchor != "" {
		if e := json.Unmarshal([]byte(row.Anchor), &out.Anchor); e != nil {
			return out, e
		}
	}
	q := query.BindIssueReply(tx, m.names)
	scope := cursorScope(ctx, row.ProjectID, "issue_replies:"+row.ID.String())
	after, e := position(replyCursor, scope)
	if e != nil {
		return out, e
	}
	rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(row.ProjectID), q.IssueID.Eq(row.ID), q.ID.Gt(after)).Order(q.ID).Limit(101).Find()
	if e != nil {
		return out, e
	}
	if len(rows) > 100 {
		rows = rows[:100]
		out.RepliesHasMore = true
		out.RepliesNextCursor = cursor(scope, rows[99].ID)
	}
	for _, r := range rows {
		out.Replies = append(out.Replies, ReplyView{r.ID, r.Body})
	}
	return out, nil
}
func (m *Module) createIssueOn(ctx context.Context, tx *gorm.DB, p guid.ID, in IssueBody) (Receipt, error) {
	if !validText(in.Title, 200) || !validText(in.Description, 8000) || (in.Anchor != nil && (!in.Anchor.valid() || in.PreviewID == nil)) {
		return Receipt{}, ErrInvalid
	}
	ext, e := m.authorize(ctx, tx, p, "project_read", true)
	if e != nil {
		return Receipt{}, e
	}
	if in.PreviewID != nil {
		if _, e = m.previewOn(ctx, tx, p, *in.PreviewID, ext); e != nil {
			return Receipt{}, e
		}
	}
	id, e := m.nextID()
	if e != nil {
		return Receipt{}, e
	}
	anchor := ""
	if in.Anchor != nil {
		b, _ := json.Marshal(in.Anchor)
		anchor = string(b)
	}
	row := &model.ClientIssue{TenantID: authn.TenantIDFromContext(ctx), ID: id, ProjectID: p, Title: in.Title, Description: in.Description, State: "OPEN", ArtifactID: in.PreviewID, Anchor: anchor, ActorID: authn.PrincipalIDFromContext(ctx), Version: 1, CreatedAt: now(), UpdatedAt: now()}
	if e = query.BindClientIssue(tx, m.names).WithContext(ctx).Create(row); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "issue_created", "channel_read", 1); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: 1, State: row.State}, nil
}
func (m *Module) actIssueOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, in IssueAction) (Receipt, error) {
	if _, e := m.authorize(ctx, tx, p, "project_manage", false); e != nil {
		return Receipt{}, e
	}
	row, e := m.issueOn(ctx, tx, p, id, false)
	if e != nil {
		return Receipt{}, e
	}
	if row.Version != v || row.State == "CLOSED" {
		return Receipt{}, ErrConflict
	}
	switch in.State {
	case "TRIAGED":
		if row.State != "OPEN" {
			return Receipt{}, ErrConflict
		}
	case "RESPONDED":
		if !validText(in.Reply, 8000) {
			return Receipt{}, ErrInvalid
		}
	case "CLOSED":
		if row.State != "RESPONDED" {
			return Receipt{}, ErrConflict
		}
	default:
		return Receipt{}, ErrInvalid
	}
	if in.InternalDefectID != nil {
		if e = m.projects.ValidateWorkOn(ctx, tx, row.TenantID, p, *in.InternalDefectID, true); e != nil {
			return Receipt{}, e
		}
		row.InternalDefectID = in.InternalDefectID
	}
	if in.Reply != "" {
		if !validText(in.Reply, 8000) {
			return Receipt{}, ErrInvalid
		}
		rid, e := m.nextID()
		if e != nil {
			return Receipt{}, e
		}
		if e = query.BindIssueReply(tx, m.names).WithContext(ctx).Create(&model.IssueReply{TenantID: row.TenantID, ID: rid, ProjectID: p, IssueID: id, Body: in.Reply, ActorID: authn.PrincipalIDFromContext(ctx), CreatedAt: now()}); e != nil {
			return Receipt{}, e
		}
	}
	row.State = in.State
	row.Version++
	row.UpdatedAt = now()
	q := query.BindClientIssue(tx, m.names)
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id)).Select(q.State, q.InternalDefectID, q.Version, q.UpdatedAt).Updates(row); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "issue_updated", "project_manage", row.Version); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: row.Version, State: row.State}, nil
}

type PackageContent struct {
	Title        string                    `json:"title"`
	ReleaseNotes string                    `json:"release_notes"`
	Artifacts    []delivery.ClientArtifact `json:"artifacts"`
}
type packageManifest struct {
	Bundle   *packageBundle               `json:"bundle,omitempty"`
	Bindings []packageFile                `json:"bindings"`
	Content  PackageContent               `json:"content"`
	Refs     []delivery.ClientArtifactRef `json:"refs"`
}
type PackageBody struct {
	Title        string                       `json:"title" minLength:"1" maxLength:"200"`
	ReleaseNotes string                       `json:"release_notes" minLength:"1" maxLength:"8000"`
	RecipientID  guid.ID                      `json:"recipient_id"`
	Artifacts    []delivery.ClientArtifactRef `json:"artifacts" minItems:"1" maxItems:"100"`
}
type PackageDecision struct {
	SignerName  string                `json:"signer_name" minLength:"1" maxLength:"200"`
	Confirmed   bool                  `json:"confirmed"`
	ContentHash string                `json:"content_hash" pattern:"^[a-f0-9]{64}$"`
	Decision    string                `json:"decision,omitempty" enum:"ACCEPT,REJECT,PARTIALLY_ACCEPTED"`
	Reason      string                `json:"reason" minLength:"1" maxLength:"2000"`
	Items       []PackageItemDecision `json:"items,omitempty" minItems:"1" maxItems:"100"`
}
type PackageItemDecision struct {
	CommentThreadIDs []guid.ID                      `json:"comment_thread_ids,omitempty" maxItems:"20"`
	AcceptanceItemID guid.ID                        `json:"acceptance_item_id,omitempty" readOnly:"true"`
	Anchors          []execution.ClientThreadAnchor `json:"anchors,omitempty" readOnly:"true"`
	ItemID           string                         `json:"item_id" pattern:"^[a-f0-9]{64}$"`
	Result           string                         `json:"result" enum:"ACCEPTED,REJECTED"`
	FeedbackCategory string                         `json:"feedback_category,omitempty" enum:"FUNCTIONAL_DEFECT,REQUIREMENT_MISMATCH,STYLE,PERFORMANCE,SECURITY,OTHER"`
	Comment          string                         `json:"comment,omitempty" maxLength:"2000"`
}
type PackageItem struct {
	ItemID   string                  `json:"item_id"`
	Artifact delivery.ClientArtifact `json:"artifact"`
}
type PackageDisposition struct {
	ItemID        string `json:"item_id"`
	Mode          string `json:"mode"`
	ClientMessage string `json:"client_message"`
}
type PackageView struct {
	Bundle                  *PackageBundleView   `json:"bundle,omitempty"`
	Dispositions            []PackageDisposition `json:"dispositions,omitempty"`
	Signature               *PackageSignature    `json:"signature,omitempty"`
	ArchivedAt              *time.Time           `json:"archived_at"`
	Draft                   *PackageBody         `json:"draft,omitempty"`
	ID                      guid.ID              `json:"id"`
	State                   string               `json:"state"`
	Version                 int                  `json:"version"`
	ContentHash             string               `json:"content_hash"`
	Content                 PackageContent       `json:"content"`
	Decision                *PackageDecision     `json:"decision,omitempty"`
	Items                   []PackageItem        `json:"items"`
	ReworkIntakeID          *guid.ID             `json:"rework_intake_id,omitempty"`
	ReworkUnavailable       bool                 `json:"rework_unavailable,omitempty"`
	ReworkUnavailableReason string               `json:"rework_unavailable_reason,omitempty"`
}

// Public item identities bind the exact frozen manifest and its artifact order.
// They reveal none of the private work/result/evidence references.
func packageItems(row *model.DeliveryPackage, content PackageContent) []PackageItem {
	items := make([]PackageItem, 0, len(content.Artifacts))
	for i, artifact := range content.Artifacts {
		hash := sha256.Sum256([]byte(row.ID.String() + ":" + row.ContentHash + ":" + strconv.Itoa(i)))
		items = append(items, PackageItem{ItemID: hex.EncodeToString(hash[:]), Artifact: artifact})
	}
	return items
}

func packageItemOutcomes(row *model.DeliveryPackage, content PackageContent, in PackageDecision) ([]PackageItemDecision, string, error) {
	items := packageItems(row, content)
	if len(items) == 0 || len(items) > 100 {
		return nil, "", ErrInvalid
	}
	// Whole-package commands remain compatible, but now freeze every outcome.
	if in.Items == nil {
		if in.Decision != "ACCEPT" && in.Decision != "REJECT" {
			return nil, "", ErrInvalid
		}
		result := "ACCEPTED"
		if in.Decision == "REJECT" {
			// Existing whole-package rejection has no category or per-item
			// comment contract. Preserve that receipt without inventing invalid
			// structured feedback; new item decisions enforce those fields.
			return nil, in.Decision, nil
		}
		out := make([]PackageItemDecision, 0, len(items))
		for _, item := range items {
			out = append(out, PackageItemDecision{ItemID: item.ItemID, Result: result, Comment: in.Reason})
		}
		return out, in.Decision, nil
	}
	if len(in.Items) != len(items) {
		return nil, "", ErrInvalid
	}
	byID := make(map[string]PackageItemDecision, len(in.Items))
	for _, decision := range in.Items {
		if _, duplicate := byID[decision.ItemID]; duplicate || (decision.Result != "ACCEPTED" && decision.Result != "REJECTED") || len([]rune(decision.Comment)) > 2000 {
			return nil, "", ErrInvalid
		}
		if decision.Result == "REJECTED" {
			switch decision.FeedbackCategory {
			case "FUNCTIONAL_DEFECT", "REQUIREMENT_MISMATCH", "STYLE", "PERFORMANCE", "SECURITY", "OTHER":
			default:
				return nil, "", ErrInvalid
			}
			if len([]rune(strings.TrimSpace(decision.Comment))) < 10 {
				return nil, "", ErrInvalid
			}
		} else if decision.FeedbackCategory != "" {
			return nil, "", ErrInvalid
		}
		byID[decision.ItemID] = decision
	}
	accepted := 0
	out := make([]PackageItemDecision, 0, len(items))
	for _, item := range items {
		decision, found := byID[item.ItemID]
		if !found {
			return nil, "", ErrInvalid
		}
		out = append(out, decision)
		if decision.Result == "ACCEPTED" {
			accepted++
		}
	}
	aggregate := "PARTIALLY_ACCEPTED"
	if accepted == len(items) {
		aggregate = "ACCEPT"
	} else if accepted == 0 {
		aggregate = "REJECT"
	}
	if in.Decision != "" && in.Decision != aggregate {
		return nil, "", ErrInvalid
	}
	return out, aggregate, nil
}

func (m *Module) packageOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, ext bool) (*model.DeliveryPackage, error) {
	q := query.BindDeliveryPackage(tx, m.names)
	r := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id))
	if ext {
		r = r.Where(q.RecipientID.Eq(authn.PrincipalIDFromContext(ctx)), q.State.Neq("DRAFT"))
	}
	row, e := r.First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return row, e
}
func (m *Module) packageView(ctx context.Context, tx *gorm.DB, row *model.DeliveryPackage) (PackageView, error) {
	out := PackageView{ArchivedAt: row.ArchivedAt, ID: row.ID, State: row.State, Version: row.Version, ContentHash: row.ContentHash}
	var manifest packageManifest
	if e := json.Unmarshal([]byte(row.Manifest), &manifest); e != nil {
		return out, e
	}
	bundle, bundleErr := m.packageBundleViewOn(ctx, tx, row, manifest)
	if bundleErr != nil {
		return out, bundleErr
	}
	out.Bundle = &bundle
	out.Content = manifest.Content
	out.Items = packageItems(row, manifest.Content)
	q := query.BindPackageReceipt(tx, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(row.ProjectID), q.PackageID.Eq(row.ID)).First()
	if e == nil {
		out.Decision = &PackageDecision{ContentHash: r.ContentHash, Decision: r.Decision, Reason: r.Reason}
		if r.SignerName != nil && r.ReceiptSHA256 != nil && r.ReceiptJSON != nil {
			out.Decision.SignerName, out.Decision.Confirmed = *r.SignerName, true
			out.Signature = &PackageSignature{Source: r.Source, RecordedBy: r.RecordedBy, SignerName: *r.SignerName, ReceiptSHA256: *r.ReceiptSHA256, ReceiptJSON: *r.ReceiptJSON}
		}
		if r.EvidenceJSON != nil && out.Signature != nil {
			if e = json.Unmarshal([]byte(*r.EvidenceJSON), &out.Signature.Evidence); e != nil {
				return out, e
			}
		}
		if r.ItemOutcomes != nil {
			if e = json.Unmarshal([]byte(*r.ItemOutcomes), &out.Decision.Items); e != nil {
				return out, e
			}
		}
		if r.Decision != "ACCEPT" {
			out.ReworkUnavailable, out.ReworkUnavailableReason = true, "REWORK_SOURCE_UNAVAILABLE"
			if m.reworkSources.ReadOn != nil {
				source, found, e := m.reworkSources.ReadOn(ctx, tx, row.TenantID, row.ProjectID, r.ID)
				if e != nil {
					return out, e
				}
				if found {
					out.ReworkIntakeID = &source.ID
					ledger := query.BindReworkDisposition(tx, m.names)
					decisions, err := ledger.WithContext(ctx).Where(ledger.TenantID.Eq(row.TenantID), ledger.ProjectID.Eq(row.ProjectID), ledger.IntakeID.Eq(source.ID), ledger.ReceiptID.Eq(r.ID)).Order(ledger.ID).Find()
					if err != nil {
						return out, err
					}
					for _, decision := range decisions {
						out.Dispositions = append(out.Dispositions, PackageDisposition{ItemID: decision.ItemID, Mode: decision.Mode, ClientMessage: decision.ClientMessage})
					}

					out.ReworkUnavailable, out.ReworkUnavailableReason = false, ""
				}
			}
		}
	} else if !errors.Is(e, gorm.ErrRecordNotFound) {
		return out, e
	}
	if row.State == "DRAFT" {
		out.Draft = &PackageBody{Title: row.Title, ReleaseNotes: row.ReleaseNotes, RecipientID: row.RecipientID, Artifacts: manifest.Refs}
	}
	return out, nil
}
func (m *Module) createPackageOn(ctx context.Context, tx *gorm.DB, p guid.ID, in PackageBody) (Receipt, error) {
	content, e := m.packageDraftContentOn(ctx, tx, p, in)
	if e != nil {
		return Receipt{}, e
	}
	tenant := authn.TenantIDFromContext(ctx)
	public, _ := json.Marshal(content)
	hash := sha256.Sum256(public)
	manifest, _ := json.Marshal(packageManifest{Content: content, Refs: in.Artifacts})
	id, e := m.nextID()
	if e != nil {
		return Receipt{}, e
	}
	row := &model.DeliveryPackage{TenantID: tenant, ID: id, ProjectID: p, Title: in.Title, ReleaseNotes: in.ReleaseNotes, Manifest: string(manifest), ContentHash: hex.EncodeToString(hash[:]), State: "DRAFT", RecipientID: in.RecipientID, ActorID: authn.PrincipalIDFromContext(ctx), Version: 1, CreatedAt: now(), UpdatedAt: now()}
	if e = query.BindDeliveryPackage(tx, m.names).WithContext(ctx).Create(row); e != nil {
		return Receipt{}, e
	}
	pins, e := m.packagePinsOn(ctx, tx, p, id, in.Artifacts, content.Artifacts, nil)
	if e != nil {
		return Receipt{}, e
	}
	private := packageManifest{Content: content, Refs: in.Artifacts, Bindings: pins}
	raw, e := json.Marshal(private)
	if e != nil {
		return Receipt{}, e
	}
	q := query.BindDeliveryPackage(tx, m.names)
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).UpdateSimple(q.Manifest.Value(string(raw))); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "package_drafted", "delivery_compose", 1); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: 1, State: "DRAFT"}, nil
}
func (m *Module) publishPackageOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int) (Receipt, error) {
	if _, e := m.authorize(ctx, tx, p, "delivery_send", false); e != nil {
		return Receipt{}, e
	}
	row, e := m.packageOn(ctx, tx, p, id, false)
	if e != nil {
		return Receipt{}, e
	}
	if row.Version != v || row.State != "DRAFT" || row.ArchivedAt != nil {
		return Receipt{}, ErrConflict
	}
	active, e := m.clients.IsClientOn(ctx, tx, row.TenantID, p, row.RecipientID)
	if e != nil {
		return Receipt{}, e
	}
	if !active {
		return Receipt{}, ErrNotFound
	}
	var manifest packageManifest
	if e = json.Unmarshal([]byte(row.Manifest), &manifest); e != nil {
		return Receipt{}, e
	}
	if e = m.requireCurrentPackageBundleOn(ctx, tx, row, manifest); e != nil {
		return Receipt{}, e
	}
	current, e := m.projects.AcceptedArtifactsOn(ctx, tx, row.TenantID, p, manifest.Refs)
	if e != nil {
		return Receipt{}, e
	}
	a, _ := json.Marshal(current)
	b, _ := json.Marshal(manifest.Content.Artifacts)
	if string(a) != string(b) {
		return Receipt{}, ErrConflict
	}
	q := query.BindDeliveryPackage(tx, m.names)
	row.Version++
	row.State = "PUBLISHED"
	row.UpdatedAt = now()
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id)).Select(q.State, q.Version, q.UpdatedAt).Updates(row); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "package_published", "delivery_send", row.Version); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: row.Version, State: row.State}, nil
}
func (m *Module) decidePackageOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, in PackageDecision) (Receipt, error) {
	if !validText(in.Reason, 2000) || !validText(in.SignerName, 200) || !in.Confirmed {
		return Receipt{}, ErrInvalid
	}
	ext, e := m.authorize(ctx, tx, p, "delivery_read", true)
	if e != nil {
		return Receipt{}, e
	}
	if !ext {
		return Receipt{}, ErrForbidden
	}
	contact, e := m.clientSignerOn(ctx, tx, p)
	if e != nil {
		return Receipt{}, e
	}
	return m.decidePackageForOn(ctx, tx, p, id, v, in, contact, authn.PrincipalIDFromContext(ctx), nil)
}

func (m *Module) decidePackageForOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, in PackageDecision, contact, actor guid.ID, record *recordedAcceptance) (Receipt, error) {
	row, e := m.packageOn(ctx, tx, p, id, record == nil)
	if e != nil {
		return Receipt{}, e
	}
	if row.Version != v || row.State != "PUBLISHED" || row.ContentHash != in.ContentHash {
		return Receipt{}, ErrConflict
	}
	var manifest packageManifest
	if e = json.Unmarshal([]byte(row.Manifest), &manifest); e != nil {
		return Receipt{}, e
	}
	outcomes, aggregate, e := packageItemOutcomes(row, manifest.Content, in)
	if e != nil {
		return Receipt{}, e
	}
	if e = m.freezeRejectionAnchorsOn(ctx, tx, row, contact, outcomes); e != nil {
		return Receipt{}, e
	}
	raw, e := json.Marshal(outcomes)
	if e != nil {
		return Receipt{}, e
	}
	var frozen *string
	if outcomes != nil {
		value := string(raw)
		frozen = &value
	}
	rid, e := m.nextID()
	if e != nil {
		return Receipt{}, e
	}
	at := now()
	signedOutcomes := outcomes
	if signedOutcomes == nil {
		for _, item := range packageItems(row, manifest.Content) {
			signedOutcomes = append(signedOutcomes, PackageItemDecision{ItemID: item.ItemID, Result: "REJECTED"})
		}
	}
	signature, e := packageReceiptSignature(id, contact, in.ContentHash, in.SignerName, in.Reason, signedOutcomes, at)
	if e != nil {
		return Receipt{}, e
	}
	if record != nil {
		signature, e = recordedReceiptSignature(signature, *record)
		if e != nil {
			return Receipt{}, e
		}
	}
	receiptRow := &model.PackageReceipt{SignerName: &signature.SignerName, SignerContactID: &contact, ReceiptSHA256: &signature.ReceiptSHA256, ReceiptJSON: &signature.ReceiptJSON, TenantID: row.TenantID, ID: rid, ProjectID: p, PackageID: id, ContentHash: in.ContentHash, Decision: aggregate, Reason: in.Reason, ItemOutcomes: frozen, ActorID: actor, CreatedAt: at}
	receiptRow.Source = "PORTAL"
	if record != nil {
		receiptRow.Source = "RECORDED"
		receiptRow.RecordedBy = &record.RecordedBy
		receiptRow.EvidenceBindingID = &record.Evidence.ID
		raw, _ := json.Marshal(record.Evidence)
		v := string(raw)
		receiptRow.EvidenceJSON = &v
	}
	if e = query.BindPackageReceipt(tx, m.names).WithContext(ctx).Create(receiptRow); e != nil {
		return Receipt{}, e
	}
	if aggregate != "ACCEPT" && in.Items != nil {
		if _, _, e = m.packageReworkOn(ctx, tx, row, rid, actor, outcomes); e != nil {
			return Receipt{}, e
		}
	}
	row.State = "ACCEPTED"
	if aggregate == "REJECT" {
		row.State = "REJECTED"
	} else if aggregate == "PARTIALLY_ACCEPTED" {
		row.State = aggregate
	}
	row.Version++
	row.UpdatedAt = now()
	q := query.BindDeliveryPackage(tx, m.names)
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id)).Select(q.State, q.Version, q.UpdatedAt).Updates(row); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "package_decided", "channel_read", row.Version); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: row.Version, State: row.State}, nil
}

type PreviewBody struct {
	ProgressSource *delivery.ClientSourceRef  `json:"progress_source,omitempty"`
	SourceStage    string                     `json:"source_stage,omitempty" enum:"ACCEPTED,PROGRESS"`
	Title          string                     `json:"title" minLength:"1" maxLength:"200"`
	Artifact       delivery.ClientArtifactRef `json:"artifact,omitempty"`
	ClientVisible  bool                       `json:"client_visible"`
}
type PreviewView struct {
	SourceStage   string  `json:"source_stage" enum:"ACCEPTED,PROGRESS"`
	ID            guid.ID `json:"id"`
	Title         string  `json:"title"`
	FileVersionID guid.ID `json:"file_version_id"`
	ContentHash   string  `json:"content_hash"`
	ClientVisible bool    `json:"client_visible"`
}

func previewView(r *model.Preview) PreviewView {
	return PreviewView{r.SourceStage, r.ID, r.Title, r.FileVersionID, r.ContentHash, r.ClientVisible}
}
func (m *Module) previewOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, ext bool) (*model.Preview, error) {
	q := query.BindPreview(tx, m.names)
	r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id))
	if ext {
		r = r.Where(q.ClientVisible.Is(true))
	}
	row, e := r.First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return row, e
}
func (m *Module) createPreviewOn(ctx context.Context, tx *gorm.DB, p guid.ID, in PreviewBody) (Receipt, error) {
	if !validText(in.Title, 200) {
		return Receipt{}, ErrInvalid
	}
	if in.SourceStage == "" {
		in.SourceStage = "ACCEPTED"
	}
	if in.SourceStage != "ACCEPTED" && in.SourceStage != "PROGRESS" {
		return Receipt{}, ErrInvalid
	}
	if in.ProgressSource != nil && (in.SourceStage != "PROGRESS" || in.Artifact.WorkItemID != 0 || in.Artifact.ResultID != 0 || in.Artifact.EvidenceID != 0) {
		return Receipt{}, ErrInvalid
	}
	permission := "project_manage"
	if in.ClientVisible {
		permission = "preview_share_approve"
	}
	if _, e := m.authorize(ctx, tx, p, permission, false); e != nil {
		return Receipt{}, e
	}
	var items []delivery.ClientArtifact
	var progressFile files.PinnedCandidate
	var e error
	if in.SourceStage == "PROGRESS" {
		var item delivery.ClientArtifact
		if in.ProgressSource != nil {
			item, progressFile, e = m.projects.SourceArtifactOn(ctx, tx, authn.TenantIDFromContext(ctx), p, *in.ProgressSource)
		} else {
			item, progressFile, e = m.projects.ProgressArtifactOn(ctx, tx, authn.TenantIDFromContext(ctx), p, in.Artifact)
		}
		items = []delivery.ClientArtifact{item}
	} else {
		items, e = m.projects.AcceptedArtifactsOn(ctx, tx, authn.TenantIDFromContext(ctx), p, []delivery.ClientArtifactRef{in.Artifact})
	}
	if e != nil {
		return Receipt{}, e
	}
	id, e := m.nextID()
	if e != nil {
		return Receipt{}, e
	}
	r := &model.Preview{SourceStage: in.SourceStage, TenantID: authn.TenantIDFromContext(ctx), ID: id, ProjectID: p, Title: in.Title, WorkItemID: in.Artifact.WorkItemID, ResultID: in.Artifact.ResultID, EvidenceID: in.Artifact.EvidenceID, FileVersionID: items[0].VersionID, ContentHash: items[0].SHA256, ClientVisible: in.ClientVisible, ActorID: authn.PrincipalIDFromContext(ctx), CreatedAt: now()}
	if in.ProgressSource != nil {
		r.SourceFileID = &in.ProgressSource.FileID
		r.WorkItemID = in.ProgressSource.WorkItemID
	}
	if e = query.BindPreview(tx, m.names).WithContext(ctx).Create(r); e != nil {
		return Receipt{}, e
	}
	var pin files.PinnedResource
	if in.SourceStage == "PROGRESS" {
		bid, err := m.nextID()
		if err != nil {
			return Receipt{}, err
		}
		pin, e = m.files.PinOn(ctx, tx, files.ResourceBindingInput{ID: bid, SpaceID: progressFile.SpaceID, FileID: progressFile.FileID, ExpectedFileVersion: progressFile.NodeVersion, VersionID: &progressFile.VersionID, Purpose: previewPurpose, ResourceKind: "client_preview", ResourceID: id, Slot: "artifact." + progressFile.VersionID.String(), ActorID: authn.PrincipalIDFromContext(ctx)})
	} else {
		pin, e = m.pinArtifactOn(ctx, tx, p, id, previewPurpose, "client_preview", in.Artifact)
	}
	if e != nil {
		return Receipt{}, e
	}
	binding := pin.BindingID
	q := query.BindPreview(tx, m.names)
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(id)).UpdateSimple(q.BindingID.Value(binding)); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "preview_created", permission, 1); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: 1, State: "PUBLISHED"}, nil
}

type AnnotationBody struct {
	PreviewID     guid.ID        `json:"preview_id"`
	FileVersionID guid.ID        `json:"file_version_id"`
	ContentHash   string         `json:"content_hash" pattern:"^[a-f0-9]{64}$"`
	Anchor        ArtifactAnchor `json:"anchor"`
	Body          string         `json:"body" minLength:"1" maxLength:"2000"`
	ClientVisible bool           `json:"client_visible"`
}
type AnnotationView struct {
	ID            guid.ID        `json:"id"`
	PreviewID     guid.ID        `json:"preview_id"`
	FileVersionID guid.ID        `json:"file_version_id"`
	ContentHash   string         `json:"content_hash"`
	Anchor        ArtifactAnchor `json:"anchor"`
	Body          string         `json:"body"`
	ClientVisible bool           `json:"client_visible"`
}

func (m *Module) createAnnotationOn(ctx context.Context, tx *gorm.DB, p guid.ID, in AnnotationBody) (Receipt, error) {
	if !validText(in.Body, 2000) || !in.Anchor.valid() {
		return Receipt{}, ErrInvalid
	}
	ext, e := m.authorize(ctx, tx, p, "artifact_comment_write", true)
	if e != nil {
		return Receipt{}, e
	}
	preview, e := m.previewOn(ctx, tx, p, in.PreviewID, ext)
	if e != nil {
		return Receipt{}, e
	}
	if preview.FileVersionID != in.FileVersionID || preview.ContentHash != in.ContentHash {
		return Receipt{}, ErrConflict
	}
	if ext {
		in.ClientVisible = true
	} else if in.ClientVisible {
		if _, e = m.authorize(ctx, tx, p, "preview_share_approve", false); e != nil {
			return Receipt{}, e
		}
	}
	if in.ClientVisible && !preview.ClientVisible {
		return Receipt{}, ErrNotFound
	}
	id, e := m.nextID()
	if e != nil {
		return Receipt{}, e
	}
	anchor, _ := json.Marshal(in.Anchor)
	r := &model.Annotation{TenantID: authn.TenantIDFromContext(ctx), ID: id, ProjectID: p, PreviewID: preview.ID, FileVersionID: preview.FileVersionID, ContentHash: preview.ContentHash, Anchor: string(anchor), Body: in.Body, ClientVisible: in.ClientVisible, ActorID: authn.PrincipalIDFromContext(ctx), CreatedAt: now()}
	if e = query.BindAnnotation(tx, m.names).WithContext(ctx).Create(r); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "annotation_created", "artifact_comment_write", 1); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: 1, State: "OPEN"}, nil
}
