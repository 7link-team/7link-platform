package clientwork

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

func (m *Module) packageDraftContentOn(ctx context.Context, tx *gorm.DB, project guid.ID, in PackageBody) (PackageContent, error) {
	if !validText(in.Title, 200) || !validText(in.ReleaseNotes, 8000) || in.RecipientID <= 0 {
		return PackageContent{}, ErrInvalid
	}
	if _, e := m.authorize(ctx, tx, project, "delivery_compose", false); e != nil {
		return PackageContent{}, e
	}
	tenant := authn.TenantIDFromContext(ctx)
	active, e := m.clients.IsClientOn(ctx, tx, tenant, project, in.RecipientID)
	if e != nil {
		return PackageContent{}, e
	}
	if !active {
		return PackageContent{}, ErrNotFound
	}
	artifacts, e := m.projects.AcceptedArtifactsOn(ctx, tx, tenant, project, in.Artifacts)
	if e != nil {
		return PackageContent{}, e
	}
	return PackageContent{Title: in.Title, ReleaseNotes: in.ReleaseNotes, Artifacts: artifacts}, nil
}

// Multiple criteria may reference the same immutable file. Pin it once and
// retain its binding across draft edits; removed files lose their binding.
func (m *Module) packagePinsOn(ctx context.Context, tx *gorm.DB, project, id guid.ID, refs []delivery.ClientArtifactRef, artifacts []delivery.ClientArtifact, previous []packageFile) ([]packageFile, error) {
	if len(refs) != len(artifacts) || len(refs) == 0 {
		return nil, ErrInvalid
	}
	old := map[guid.ID]packageFile{}
	for _, pin := range previous {
		old[pin.VersionID] = pin
	}
	seen := map[guid.ID]bool{}
	pins := make([]packageFile, 0, len(artifacts))
	for i, artifact := range artifacts {
		if seen[artifact.VersionID] {
			continue
		}
		seen[artifact.VersionID] = true
		if pin, ok := old[artifact.VersionID]; ok {
			pins = append(pins, pin)
			continue
		}
		pin, e := m.pinArtifactOn(ctx, tx, project, id, packagePurpose, "client_package", refs[i])
		if e != nil {
			return nil, e
		}
		if pin.VersionID != artifact.VersionID || pin.SHA256 != artifact.SHA256 {
			return nil, ErrConflict
		}
		pins = append(pins, packageFile{VersionID: pin.VersionID, BindingID: pin.BindingID})
	}
	for _, pin := range previous {
		if seen[pin.VersionID] {
			continue
		}
		if m.files.UnpinOn == nil {
			return nil, ErrUnavailable
		}
		if e := m.files.UnpinOn(ctx, tx, pin.BindingID, id); e != nil {
			return nil, e
		}
	}
	return pins, nil
}

func (m *Module) editPackageOn(ctx context.Context, tx *gorm.DB, project, id guid.ID, expected int, in PackageBody) (Receipt, error) {
	content, e := m.packageDraftContentOn(ctx, tx, project, in)
	if e != nil {
		return Receipt{}, e
	}
	row, e := m.packageOn(ctx, tx, project, id, false)
	if e != nil {
		return Receipt{}, e
	}
	if row.State != "DRAFT" || row.Version != expected || row.ArchivedAt != nil {
		return Receipt{}, ErrConflict
	}
	var old packageManifest
	if e = json.Unmarshal([]byte(row.Manifest), &old); e != nil {
		return Receipt{}, e
	}
	pins, e := m.packagePinsOn(ctx, tx, project, id, in.Artifacts, content.Artifacts, old.Bindings)
	if e != nil {
		return Receipt{}, e
	}
	if old.Bundle != nil {
		old.Bundle.State = "STALE"
	}
	manifest, e := json.Marshal(packageManifest{Content: content, Refs: in.Artifacts, Bindings: pins, Bundle: old.Bundle})
	if e != nil {
		return Receipt{}, e
	}
	public, e := json.Marshal(content)
	if e != nil {
		return Receipt{}, e
	}
	digest := sha256.Sum256(public)
	row.Title, row.ReleaseNotes, row.RecipientID = in.Title, in.ReleaseNotes, in.RecipientID
	row.Manifest, row.ContentHash = string(manifest), hex.EncodeToString(digest[:])
	row.Version++
	row.UpdatedAt = now()
	q := query.BindDeliveryPackage(tx, m.names)
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id)).Select(q.Title, q.ReleaseNotes, q.RecipientID, q.Manifest, q.ContentHash, q.Version, q.UpdatedAt).Updates(row); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, project, id, "package_drafted", "delivery_compose", row.Version); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: row.Version, State: row.State}, nil
}
