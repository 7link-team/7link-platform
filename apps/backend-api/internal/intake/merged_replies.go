package intake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// AgentMerge is a separate entry point: ordinary disposition and every decline
// retain their Human-only authority. The project owner binds current governance.
type AgentMerge struct {
	AuthorizeOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
	MergeOn     func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, SourceDisposition) (IntakeLedgerEntry, error)
}
type AgentMergeConnector struct {
	Bind func(DelegatedNewAuthority) error
}
type MergedReplyRoute struct {
	MergeRoute
	SourceHash, TargetHash string
	TargetRecipientID      guid.ID
}

// MergedReplyRoutes is a trusted projector capability. The transport owner
// validates the actual client message and current thread memberships.
type MergedReplyRoutes func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (MergedReplyRoute, error)
type dispositionGrantKey struct{}
type dispositionGrant struct {
	tx                             *gorm.DB
	tenant, project, source, actor guid.ID
	authority                      string
}

func (m *Module) provideMergedReplies(s *module.Services) error {
	if err := module.Provide[MergedReplyRoutes](s, m.mergedReplyRouteOn); err != nil {
		return err
	}
	if err := module.Provide[AgentMergeConnector](s, AgentMergeConnector{Bind: func(check DelegatedNewAuthority) error {
		if check == nil || m.mergeAuthority != nil {
			return ErrInvalidArgument
		}
		m.mergeAuthority = check
		return nil
	}}); err != nil {
		return err
	}
	return module.Provide[AgentMerge](s, AgentMerge{AuthorizeOn: func(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID) error {
		_, err := m.agentMergeGrantOn(ctx, tx, tenant, project, source)
		return err
	}, MergeOn: m.agentMergeOn})
}

func (m *Module) mergedReplyRouteOn(ctx context.Context, tx *gorm.DB, tenant, project, source, recipient guid.ID) (MergedReplyRoute, error) {
	if recipient <= 0 {
		return MergedReplyRoute{}, ErrInvalidArgument
	}
	// A tenant-only system context provides no user authority. The source and
	// target are selected only from the same canonical project merge chain.
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: tenant})
	first, target, err := m.mergeRecordsOn(ctx, tx, tenant, project, source)
	if err != nil {
		return MergedReplyRoute{}, err
	}
	if first.ID == target.ID {
		return MergedReplyRoute{}, gorm.ErrRecordNotFound
	}
	c := query.BindContacts(tx, m.names)
	if _, err := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(first.ContactID), c.PrincipalID.Eq(recipient), c.State.Eq("ACTIVE"), c.ArchivedAt.IsNull()).First(); err != nil {
		return MergedReplyRoute{}, err
	}
	contact, err := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(target.ContactID), c.State.Eq("ACTIVE"), c.ArchivedAt.IsNull()).First()
	if err != nil {
		return MergedReplyRoute{}, err
	}
	if contact.PrincipalID == nil || *contact.PrincipalID <= 0 {
		return MergedReplyRoute{}, ErrConflict
	}
	return MergedReplyRoute{MergeRoute: MergeRoute{SourceID: first.ID, TargetID: target.ID, TargetContactID: target.ContactID, SourceRef: first.RefNo, TargetRequirementID: target.RequirementID}, SourceHash: first.RawContentHash, TargetHash: target.RawContentHash, TargetRecipientID: *contact.PrincipalID}, nil
}

func (m *Module) agentMergeGrantOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (dispositionGrant, error) {
	var out dispositionGrant
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || id <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.EntityIDFromContext(ctx) != project || authn.PrincipalIDFromContext(ctx) <= 0 {
		return out, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalidArgument
	}
	if m.mergeAuthority == nil {
		return out, ErrUnavailable
	}
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if err != nil {
		return out, err
	}
	if row.Kind == "REWORK" || row.LatestCardRevision < 1 {
		return out, ErrConflict
	}
	cards := query.BindTriageCards(tx, m.names)
	stored, err := cards.WithContext(ctx).Where(cards.TenantID.Eq(tenant), cards.IntakeID.Eq(id), cards.Revision.Eq(row.LatestCardRevision)).First()
	if err != nil {
		return out, err
	}
	digest := sha256.Sum256([]byte(stored.ProposalJSON))
	if stored.ProposalHash != hex.EncodeToString(digest[:]) {
		return out, ErrConflict
	}
	card := TriageSource{ID: stored.ID, IntakeID: id, ProjectID: project, Revision: stored.Revision, Version: row.Version, ActorID: stored.ActorID, Hash: stored.ProposalHash, CreatedAt: stored.CreatedAt}
	authority, err := m.mergeAuthority(ctx, tx, tenant, project, card)
	if err != nil {
		return out, err
	}
	if authority == "" || card.ActorID == authn.PrincipalIDFromContext(ctx) {
		return out, ErrForbidden
	}
	// Governance has acquired the current principal/project fences. Verify the
	// hint again; the canonical command then takes both source locks in ID order.
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.Version.Eq(row.Version), q.LatestCardRevision.Eq(row.LatestCardRevision)).First(); err != nil {
		return out, err
	}
	return dispositionGrant{tx: tx, tenant: tenant, project: project, source: id, actor: authn.PrincipalIDFromContext(ctx), authority: authority}, nil
}

func (m *Module) agentMergeOn(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID, expected int, in SourceDisposition) (IntakeLedgerEntry, error) {
	if in.Action != "MERGE" {
		return IntakeLedgerEntry{}, ErrForbidden
	}
	grant, err := m.agentMergeGrantOn(ctx, tx, tenant, project, source)
	if err != nil {
		return IntakeLedgerEntry{}, err
	}
	return m.disposeSourceOn(context.WithValue(ctx, dispositionGrantKey{}, grant), tx, tenant, project, source, expected, in)
}

func (m *Module) dispositionAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID, action string) (string, string, error) {
	if grant, ok := ctx.Value(dispositionGrantKey{}).(dispositionGrant); ok {
		if action != "MERGE" || grant.tx != tx || grant.tenant != tenant || grant.project != project || grant.source != source || grant.actor != authn.PrincipalIDFromContext(ctx) || grant.authority == "" {
			return "", "", ErrForbidden
		}
		return "AGENT", grant.authority, nil
	}
	permission := "intake_convert"
	if action == "DECLINE" {
		permission = "intake_decline"
	}
	return "HUMAN", "permission:" + permission, m.changeSourceAuthority(ctx, tx, tenant, project, permission)
}

// DispositionActor lets the round owner preserve the canonical Agent actor
// when closing a round as part of a delegated merge, never infer Human success.
func DispositionActor(ctx context.Context) (string, string) {
	if grant, ok := ctx.Value(dispositionGrantKey{}).(dispositionGrant); ok {
		return "AGENT", grant.authority
	}
	return "HUMAN", ""
}

func (m *Module) dispositionSourceReadAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID) error {
	if grant, ok := ctx.Value(dispositionGrantKey{}).(dispositionGrant); ok {
		if grant.tx == tx && grant.tenant == tenant && grant.project == project && grant.source == source && grant.actor == authn.PrincipalIDFromContext(ctx) {
			return nil
		}
		return ErrForbidden
	}
	return m.changeSourceAuthority(ctx, tx, tenant, project, "scope_change")
}

// ReworkAnchor is the source-owned immutable attachment frozen by the receipt
// owner. The Intake stores this value verbatim rather than rereading live files.
type ReworkAnchor struct {
	Anchor        json.RawMessage `json:"anchor,omitempty"`
	ThreadID      guid.ID         `json:"thread_id"`
	AnchorKind    string          `json:"anchor_kind"`
	AnchorExcerpt string          `json:"anchor_excerpt"`
	VersionRef    string          `json:"version_ref"`
	ArtifactRef   json.RawMessage `json:"artifact_ref"`
}
