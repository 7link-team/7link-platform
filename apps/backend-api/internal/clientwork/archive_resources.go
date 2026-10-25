package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
)

func (m *Module) injectArchiveResources(s *module.Services) error {
	if err := m.injectRubric(s); err != nil {
		return err
	}
	c, err := module.Resolve[delivery.ProjectArchiveConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.Bind("clientwork", m.projectArchiveResourcesOn)
}
func (m *Module) projectArchiveResourcesOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]delivery.ProjectArchiveResource, error) {
	if m == nil || tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return nil, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalid
	}
	out := []delivery.ProjectArchiveResource{}
	// Publication without an exact-content receipt is still a pending handoff.
	p := query.BindDeliveryPackage(tx, m.names)
	packages, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(p.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range packages {
		ready := row.ArchivedAt != nil && row.State == "DRAFT"
		if slices.Contains([]string{"ACCEPTED", "REJECTED", "PARTIALLY_ACCEPTED"}, row.State) {
			r := query.BindPackageReceipt(tx, m.names)
			receipts, e := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.PackageID.Eq(row.ID), r.ContentHash.Eq(row.ContentHash)).Clauses(clause.Locking{Strength: "UPDATE"}).Find()
			if e != nil {
				return nil, e
			}
			expected := "ACCEPT"
			if row.State == "REJECTED" {
				expected = "REJECT"
			}
			if row.State == "PARTIALLY_ACCEPTED" {
				expected = "PARTIALLY_ACCEPTED"
			}
			ready = len(receipts) == 1 && receipts[0].Decision == expected
		}
		out = append(out, delivery.ProjectArchiveResource{Kind: "CLIENT_PACKAGE", ID: row.ID, State: row.State, Version: row.Version, Ready: ready})
	}
	r := query.BindAcceptanceReminder(tx, m.names)
	reminders, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(r.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range reminders {
		state := "SCHEDULED"
		if row.EmittedAt != nil {
			state = "EMITTED"
		}
		if row.CancelledAt != nil {
			state = "CANCELLED" //nolint:misspell // Preserve the published domain or serialized contract spelling.
		}
		out = append(out, delivery.ProjectArchiveResource{Kind: "ACCEPTANCE_REMINDER", ID: row.ID, State: state, Version: 1, Ready: row.EmittedAt != nil || row.CancelledAt != nil})
	}
	c := query.BindClarificationRound(tx, m.names)
	rounds, err := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(c.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range rounds {
		out = append(out, delivery.ProjectArchiveResource{Kind: "CLIENT_CLARIFICATION", ID: row.ID, State: row.State, Version: row.Version, Ready: row.ClosedAt != nil && slices.Contains([]string{"ANSWERED", "CANCELLED", "EXPIRED"}, row.State)}) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	}
	s := query.BindPreviewShare(tx, m.names)
	shares, err := s.WithContext(ctx).Where(s.TenantID.Eq(tenant), s.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(s.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range shares {
		out = append(out, delivery.ProjectArchiveResource{Kind: "PREVIEW_SHARE", ID: row.ID, State: row.State, Version: row.Version, Ready: row.ActiveSlot == nil && slices.Contains([]string{"REJECTED", "REVOKED", "EXPIRED"}, row.State)})
	}
	d := query.BindDispatchProposal(tx, m.names)
	proposals, err := d.WithContext(ctx).Where(d.TenantID.Eq(tenant), d.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(d.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range proposals {
		out = append(out, delivery.ProjectArchiveResource{Kind: "DISPATCH_PROPOSAL", ID: row.ID, State: row.State, Version: row.Version, Ready: row.ActiveSlot == nil && (row.State == "EXPIRED" || (row.DecidedAt != nil && slices.Contains([]string{"APPLIED", "OVERRIDDEN", "DISMISSED"}, row.State)))})
	}
	sh := query.BindSheets(tx, m.names)
	sheets, err := sh.WithContext(ctx).Where(sh.TenantID.Eq(tenant), sh.ProjectID.Eq(project), sh.State.Eq("AWAITING_SIGNOFF")).Clauses(clause.Locking{Strength: "UPDATE"}).Order(sh.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range sheets {
		out = append(out, delivery.ProjectArchiveResource{Kind: "CLIENT_SIGNOFF", ID: row.ID, State: row.State, Version: row.Version})
	}
	ch := query.BindChangeRequest(tx, m.names)
	changes, err := ch.WithContext(ctx).Where(ch.TenantID.Eq(tenant), ch.ProjectID.Eq(project), ch.State.In("REQUESTED", "AWAITING_CONFIRMATION", "CONFIRMED")).Clauses(clause.Locking{Strength: "UPDATE"}).Order(ch.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range changes {
		out = append(out, delivery.ProjectArchiveResource{Kind: "CLIENT_CHANGE", ID: row.ID, State: row.State, Version: row.Version})
	}
	ex := query.BindToleranceException(tx, m.names)
	exceptions, err := ex.WithContext(ctx).Where(ex.TenantID.Eq(tenant), ex.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(ex.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range exceptions {
		out = append(out, delivery.ProjectArchiveResource{Kind: "TOLERANCE_EXCEPTION", ID: row.ID, State: row.State, Version: row.Version, Ready: row.State == "DECIDED" && row.DecidedAt != nil && row.Decision != nil && *row.Decision != ""})
	}
	return out, nil
}

func (m *Module) injectRubric(s *module.Services) error {
	c, e := module.Resolve[delivery.RubricClientConnector](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	return c.Bind(func(ctx context.Context, tx *gorm.DB, in delivery.RubricClientRequest) (delivery.RubricClientTarget, error) {
		v, e := m.rubricPackageOn(ctx, tx, in)
		switch {
		case errors.Is(e, ErrForbidden):
			e = delivery.ErrWorkForbidden
		case errors.Is(e, ErrNotFound):
			e = delivery.ErrProjectNotFound
		case errors.Is(e, ErrConflict):
			e = delivery.ErrResultState
		case errors.Is(e, ErrInvalid):
			e = delivery.ErrInvalidArgument
		case errors.Is(e, ErrUnavailable):
			e = delivery.ErrUnavailable
		}
		return v, e
	})
}
func rubricRoute(method string, p []string) bool {
	return len(p) == 5 && p[0] == "entities" && p[2] == "delivery-packages" && p[4] == "rubric-scores" && (method == "GET" || method == "POST")
}
func (m *Module) rubricPackageOn(ctx context.Context, tx *gorm.DB, in delivery.RubricClientRequest) (delivery.RubricClientTarget, error) {
	var out delivery.RubricClientTarget
	if !validPackageItemID(in.ItemID) || in.PackageID <= 0 {
		return out, ErrInvalid
	}
	permission := "channel_read"
	if in.Write {
		permission = "delivery_compose"
	}
	ext, e := m.authorize(ctx, tx, in.ProjectID, permission, true)
	if e != nil {
		return out, e
	}
	if in.Write {
		if ext {
			if in.Stage != "CLIENT_ACCEPTANCE" {
				return out, ErrForbidden
			}
			if _, e = m.clientSignerOn(ctx, tx, in.ProjectID); e != nil {
				return out, e
			}
		} else if in.Stage != "HUMAN_REVIEW" {
			return out, ErrForbidden
		}
	}
	row, e := m.packageOn(ctx, tx, in.ProjectID, in.PackageID, ext)
	if e != nil {
		return out, e
	}
	if in.Write && in.Version != row.Version {
		return out, ErrConflict
	}
	if row.State == "DRAFT" || row.ArchivedAt != nil {
		return out, ErrConflict
	}
	var manifest packageManifest
	if json.Unmarshal([]byte(row.Manifest), &manifest) != nil || len(manifest.Refs) != len(manifest.Content.Artifacts) {
		return out, ErrUnavailable
	}
	public, e := json.Marshal(manifest.Content)
	if e != nil {
		return out, e
	}
	digest := sha256.Sum256(public)
	if hex.EncodeToString(digest[:]) != row.ContentHash {
		return out, ErrConflict
	}
	items := packageItems(row, manifest.Content)
	for i, item := range items {
		if item.ItemID != in.ItemID {
			continue
		}
		ref := manifest.Refs[i]
		if ref.WorkItemID <= 0 || ref.ResultID <= 0 || ref.EvidenceID <= 0 {
			return out, ErrUnavailable
		}
		kind, authority := "HUMAN", "permission:delivery_compose"
		if ext {
			kind = "CLIENT"
			authority = "client-package:" + row.ID.String() + ":" + authn.PrincipalIDFromContext(ctx).String()
		}
		return delivery.RubricClientTarget{Version: row.Version, WorkItemID: ref.WorkItemID, ResultID: ref.ResultID, Hash: row.ContentHash, Authority: authority, ActorKind: kind, EvidenceIDs: []guid.ID{ref.EvidenceID}}, nil
	}
	return out, ErrNotFound
}
