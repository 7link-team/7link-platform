package intake

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChangeSources stores only genuine source records and immutable server-assessed
// cards. The caller checks the canonical proposal/client decision and holds the
// project fence; no client-supplied approval JSON is accepted here.
type ChangeSources struct {
	OwnSubmissionsOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, int, SubmissionFilter) (SubmissionPage, error)
	OwnSubmissionOn       func(context.Context, *gorm.DB, guid.ID, guid.ID) (Submission, error)
	SuggestionProjectOn   func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error)
	MergeRouteOn          func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (MergeRoute, error)
	ClientMergeOn         func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (ClientMergeProjection, error)
	SuggestionSourcesOn   func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (SuggestionSourcePage, error)
	SuggestionSourceOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (SuggestionSource, error)
	RecipientOn           func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error
	DisposeOn             func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, SourceDisposition) (IntakeLedgerEntry, error)
	ConvertProjectOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, int, string, guid.ID, guid.ID) error
	TimingOn              func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (ClarificationTiming, bool, error)
	ExpireClarificationOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, time.Time) (int, error)
	ConvertNewOn          func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, int, string, guid.ID) error
	// ClarificationOn is called inside the authorized clientwork command transaction.
	ClarificationOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, guid.ID, ClarificationAction) (int, error)
	TriageOn        func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, TriageDraft) (TriageSource, error)
	ListOn          func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (IntakeLedgerPage, error)
	SourceOn        func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (IntakeLedgerSource, error)
	DraftOn         func(context.Context, *gorm.DB, guid.ID, guid.ID, ChangeSourceDraft) (ChangeSourceView, error)
	ReadOn          func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (ChangeSourceView, error)
	ConvertedOn     func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, int, guid.ID) error
}

type ChangeSourceDraft struct {
	OriginalIntakeID guid.ID
	RequirementID    guid.ID
	ChangeID         guid.ID
	ProposalHash     string
	ProposalJSON     string
	ImpactJSON       string
	Title            string
}

type ChangeSourceView struct {
	ID               guid.ID `json:"id"`
	ProjectID        guid.ID `json:"project_id"`
	RequirementID    guid.ID `json:"requirement_id"`
	OriginalIntakeID guid.ID `json:"original_intake_id"`
	ChangeID         guid.ID `json:"change_id"`
	State            string  `json:"state"`
	Version          int     `json:"version"`
	CardRevision     int     `json:"card_revision"`
	ProposalHash     string  `json:"proposal_hash"`
	ProposalJSON     string  `json:"proposal"`
	ImpactJSON       string  `json:"impact"`
	ResultVersionNo  *int    `json:"result_version_no,omitempty"`
}

func (m *Module) provideChangeSources(s *module.Services) error {
	if e := module.Provide[ReworkSources](s, ReworkSources{AdvanceOn: m.advanceReworkSourceOn, CreateOn: m.createReworkSourceOn, ReadOn: m.readReworkSourceOn}); e != nil {
		return e
	}
	return module.Provide[ChangeSources](s, ChangeSources{OwnSubmissionsOn: m.listOwnSubmissionsOn, OwnSubmissionOn: m.ownSubmissionOn, SuggestionProjectOn: m.suggestionProjectOn, MergeRouteOn: m.mergeRouteOn, ClientMergeOn: m.clientMergeOn, SuggestionSourcesOn: m.suggestionSourcesOn, SuggestionSourceOn: m.suggestionSourceOn, RecipientOn: m.sourceRecipientOn, DisposeOn: m.disposeSourceOn, ConvertProjectOn: m.convertProjectSourceOn, TimingOn: m.clarificationTimingOn, ExpireClarificationOn: m.expireClarificationOn, ConvertNewOn: m.convertNewSourceOn, ClarificationOn: m.clarificationStateOn, TriageOn: m.triageSourceOn, ListOn: m.listIntakeLedgerOn, SourceOn: m.readIntakeLedgerSourceOn, DraftOn: m.draftChangeSourceOn, ReadOn: m.readChangeSourceOn, ConvertedOn: m.convertChangeSourceOn})
}

// IntakeLedgerEntry contains internal processing facts, without source contents.
type IntakeLedgerEntry struct {
	ID           guid.ID   `json:"id"`
	ProjectID    guid.ID   `json:"project_id"`
	RefNo        int       `json:"ref_no"`
	Title        string    `json:"title"`
	Kind         string    `json:"kind"`
	State        string    `json:"state"`
	Version      int       `json:"version"`
	CardRevision int       `json:"card_revision"`
	ReceivedAt   time.Time `json:"received_at"`
}

type IntakeLedgerPage struct {
	Items   []IntakeLedgerEntry `json:"items"`
	HasMore bool                `json:"has_more"`
}

// IntakeLedgerSource returns the original immutable source and, when present,
// the real frozen CHANGE card. Other kinds never acquire a synthetic card.
type IntakeLedgerSource struct {
	LastHandlerID       *guid.ID            `json:"-"`
	DispositionDelivery DispositionDelivery `json:"disposition_delivery"`
	MergedIntoIntakeID  *guid.ID            `json:"merged_into_intake_id,omitempty"`
	DeclineReasonCode   *string             `json:"decline_reason_code,omitempty"`
	DeclineReason       *string             `json:"decline_reason,omitempty"`
	ClientMessage       *string             `json:"client_message,omitempty"`
	IntakeLedgerEntry
	RawContent       *string           `json:"raw_content,omitempty"`
	RawContentFormat string            `json:"raw_content_format"`
	RawContentHash   string            `json:"raw_content_hash"`
	RequirementID    *guid.ID          `json:"requirement_id,omitempty"`
	Card             *ChangeSourceView `json:"card,omitempty"`
	Triage           *TriageSource     `json:"triage,omitempty"`
	Rework           *ReworkSourceView `json:"rework,omitempty"`
}

func intakeLedgerEntry(row *model.Record, project guid.ID) IntakeLedgerEntry {
	return IntakeLedgerEntry{ID: row.ID, ProjectID: project, RefNo: row.RefNo, Title: row.Title, Kind: row.Kind, State: row.Status, Version: row.Version, CardRevision: row.LatestCardRevision, ReceivedAt: row.ReceivedAt}
}

func (m *Module) listIntakeLedgerOn(ctx context.Context, tx *gorm.DB, tenant, project, before guid.ID, limit int) (IntakeLedgerPage, error) {
	out := IntakeLedgerPage{Items: []IntakeLedgerEntry{}}
	if before < 0 || limit < 1 || limit > 100 {
		return out, ErrInvalidArgument
	}
	if e := m.changeSourceAuthority(ctx, tx, tenant, project, "scope_change"); e != nil {
		return out, e
	}
	q := query.BindRecords(tx, m.names)
	r := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project))
	if before > 0 {
		r = r.Where(q.ID.Lt(before))
	}
	rows, e := r.Order(q.ID.Desc()).Limit(limit + 1).Find()
	if e != nil {
		return out, e
	}
	if len(rows) > limit {
		out.HasMore = true
		rows = rows[:limit]
	}
	for _, row := range rows {
		out.Items = append(out.Items, intakeLedgerEntry(row, project))
	}
	return out, nil
}

func (m *Module) readIntakeLedgerSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (IntakeLedgerSource, error) {
	var out IntakeLedgerSource
	if id <= 0 {
		return out, ErrInvalidArgument
	}
	if e := m.dispositionSourceReadAuthorityOn(ctx, tx, tenant, project, id); e != nil {
		return out, e
	}
	q := query.BindRecords(tx, m.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
	if e != nil {
		return out, e
	}
	var card *ChangeSourceView
	if row.Kind == "CHANGE" && row.LatestCardRevision > 0 {
		value, e := m.readChangeSourceOn(ctx, tx, tenant, project, id)
		if e != nil {
			return out, e
		}
		card = &value
	}
	var handler *guid.ID
	if row.LatestCardRevision > 0 {
		cards := query.BindTriageCards(tx, m.names)
		latest, e := cards.WithContext(ctx).Where(cards.TenantID.Eq(tenant), cards.IntakeID.Eq(id), cards.Revision.Eq(row.LatestCardRevision)).Take()
		if e != nil {
			return out, e
		}
		if latest.ActorID > 0 {
			handler = &latest.ActorID
		}
	}
	out = IntakeLedgerSource{LastHandlerID: handler, DispositionDelivery: DispositionDelivery{State: row.ClientNotificationState, MessageID: row.ClientNotificationMessageID}, MergedIntoIntakeID: row.MergedIntoIntakeID, DeclineReasonCode: row.DeclineReasonCode, DeclineReason: row.DeclineReason, ClientMessage: row.ClientMessage, IntakeLedgerEntry: intakeLedgerEntry(row, project), RawContent: row.RawContent, RawContentFormat: row.RawContentFormat, RawContentHash: row.RawContentHash, RequirementID: row.RequirementID, Card: card}
	if row.Kind == "REWORK" && row.OriginAcceptanceID != nil {
		value, found, err := m.readReworkSourceOn(ctx, tx, tenant, project, *row.OriginAcceptanceID)
		if err != nil {
			return IntakeLedgerSource{}, err
		}
		if !found || value.ID != row.ID {
			return IntakeLedgerSource{}, ErrConflict
		}
		out.Rework = &value
	}
	if row.Kind != "CHANGE" && row.LatestCardRevision > 0 {
		value, err := m.readTriageSourceOn(ctx, tx, tenant, row)
		if err != nil {
			return IntakeLedgerSource{}, err
		}
		out.Triage = &value
	}
	return out, nil
}

// ReworkSources accepts only receipt facts frozen by the authorized package
// owner. It never reopens work or supplies substitute contacts/channels.
type ReworkSources struct {
	AdvanceOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, bool) (int, error)
	CreateOn  func(context.Context, *gorm.DB, guid.ID, guid.ID, ReworkDraft) (ReworkSourceView, bool, error)
	ReadOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (ReworkSourceView, bool, error)
}
type ReworkItem struct {
	Anchors          []ReworkAnchor `json:"anchors,omitempty"`
	ItemID           string         `json:"item_id"`
	WorkItemID       guid.ID        `json:"work_item_id"`
	ResultID         guid.ID        `json:"result_id"`
	EvidenceID       guid.ID        `json:"evidence_id"`
	RequirementID    guid.ID        `json:"requirement_id"`
	SourceIntakeID   guid.ID        `json:"source_intake_id"`
	FeedbackCategory string         `json:"feedback_category,omitempty"`
	Comment          string         `json:"comment"`
}
type ReworkDraft struct {
	PackageID   guid.ID      `json:"package_id"`
	ReceiptID   guid.ID      `json:"receipt_id"`
	RecipientID guid.ID      `json:"recipient_id"`
	ContentHash string       `json:"content_hash"`
	Items       []ReworkItem `json:"items"`
}
type ReworkSourceView struct {
	ID          guid.ID     `json:"id"`
	ProjectID   guid.ID     `json:"project_id"`
	PackageID   guid.ID     `json:"package_id"`
	ReceiptID   guid.ID     `json:"receipt_id"`
	State       string      `json:"state"`
	Version     int         `json:"version"`
	ContentHash string      `json:"content_hash"`
	Rejection   ReworkDraft `json:"rejection"`
}

func (m *Module) readReworkSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, receipt guid.ID) (ReworkSourceView, bool, error) {
	var out ReworkSourceView
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || receipt <= 0 {
		return out, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalidArgument
	}
	q := query.BindRecords(tx, m.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.OriginAcceptanceID.Eq(receipt), q.Kind.Eq("REWORK")).Take()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if e != nil {
		return out, false, e
	}
	if row.RawContent == nil || row.OriginDeliveryID == nil {
		return out, false, ErrConflict
	}
	if e = json.Unmarshal([]byte(*row.RawContent), &out.Rejection); e != nil {
		return out, false, e
	}
	out.ID, out.ProjectID, out.PackageID, out.ReceiptID = row.ID, project, *row.OriginDeliveryID, receipt
	out.State, out.Version, out.ContentHash = row.Status, row.Version, row.RawContentHash
	return out, true, nil
}

func (m *Module) createReworkSourceOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, in ReworkDraft) (ReworkSourceView, bool, error) {
	var out ReworkSourceView
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || in.PackageID <= 0 || in.ReceiptID <= 0 || in.RecipientID <= 0 || len(in.Items) == 0 || len(in.Items) > 100 || len(in.ContentHash) != 64 {
		return out, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalidArgument
	}
	if m.brief.ids == nil || m.brief.audit == nil || m.brief.events == nil {
		return out, false, ErrUnavailable
	}
	raw, e := json.Marshal(in)
	if e != nil {
		return out, false, e
	}
	// Intake's immutable original-content limit is preserved without truncating
	// rejected feedback. A later explicit retry reports capability unavailable.
	if len(raw) > 65536 {
		return out, false, nil
	}
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	q := query.BindRecords(tx, m.names)
	if _, e = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant)).Order(q.ID.Asc()).Take(); errors.Is(e, gorm.ErrRecordNotFound) {
		return out, false, nil
	} else if e != nil {
		return out, false, e
	}
	prior, found, e := m.readReworkSourceOn(ctx, tx, tenant, project, in.ReceiptID)
	if e != nil {
		return out, false, e
	}
	if found {
		if prior.PackageID != in.PackageID || prior.ContentHash != hash {
			return out, false, ErrConflict
		}
		return prior, true, nil
	}
	var channel *model.Record
	seen := map[string]bool{}
	for _, item := range in.Items {
		if item.WorkItemID <= 0 || item.ResultID <= 0 || item.EvidenceID <= 0 || len(item.ItemID) != 64 || seen[item.ItemID] {
			return out, false, ErrInvalidArgument
		}
		seen[item.ItemID] = true
		if item.RequirementID <= 0 || item.SourceIntakeID <= 0 {
			return out, false, nil
		}
		source, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.RequirementID.Eq(item.RequirementID), q.ID.Eq(item.SourceIntakeID), q.Status.Eq("CONVERTED")).Take()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return out, false, nil
		}
		if e != nil {
			return out, false, e
		}
		c := query.BindContacts(tx, m.names)
		contact, e := c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.TenantID.Eq(tenant), c.ID.Eq(source.ContactID), c.PrincipalID.Eq(in.RecipientID), c.State.Eq("ACTIVE"), c.ArchivedAt.IsNull()).Take()
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return out, false, e
		}
		if e == nil && contact.ID == source.ContactID && channel == nil {
			channel = source
		}
	}
	if channel == nil {
		return out, false, nil
	}
	maxRow, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant)).Order(q.RefNo.Desc()).Take()
	if e != nil {
		return out, false, e
	}
	id, e := m.brief.ids.Next()
	if e != nil {
		return out, false, e
	}
	if id <= 0 {
		return out, false, ErrUnavailable
	}
	var token [8]byte
	if _, e = rand.Read(token[:]); e != nil {
		return out, false, e
	}
	body := string(raw)
	at := time.Now().UTC().Truncate(time.Millisecond)
	actor := authn.PrincipalIDFromContext(ctx)
	row := &model.Record{ID: guid.ID(id), TenantID: tenant, RefNo: maxRow.RefNo + 1, ChannelID: channel.ChannelID, ChannelKind: channel.ChannelKind, ContactID: channel.ContactID, Kind: "REWORK", Status: "RECEIVED", Title: "Rework: " + in.PackageID.String(), RawContent: &body, RawContentFormat: "PLAIN", RawContentHash: hash, ReplyToken: hex.EncodeToString(token[:]), ReceivedAt: at, SubmittedByPrincipalID: &in.RecipientID, ProjectID: &project, OriginDeliveryID: &in.PackageID, OriginAcceptanceID: &in.ReceiptID, Version: 1, CreatedAt: at, UpdatedAt: at, CreatedBy: &actor, UpdatedBy: &actor}
	if e = q.WithContext(ctx).Create(row); e != nil {
		return out, false, e
	}
	data := map[string]any{"origin_delivery_id": in.PackageID.String(), "origin_acceptance_id": in.ReceiptID.String(), "source": "CLIENT_PACKAGE_REJECTION", "content_hash": hash}
	event := audit.NewDomainEvent(ctx, tenant, "intake.received", "intake", row.ID)
	event.Detail = data
	if e = m.brief.audit(ctx, tx, event); e != nil {
		return out, false, e
	}
	if _, e = m.brief.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "intake.received", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "intake:client_package_rejection"}, Subject: events.Subject{Kind: "intake", ID: row.ID}, SubjectVersion: 1, Data: data}); e != nil {
		return out, false, e
	}
	return m.readReworkSourceOn(ctx, tx, tenant, project, in.ReceiptID)
}

func (m *Module) changeSourceAuthority(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if tenant <= 0 || project <= 0 || actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	if tx == nil || tx.Statement == nil {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if m == nil || m.brief.principals.Lock == nil || m.brief.principals.ActiveHumanByID == nil || m.brief.explain == nil {
		return ErrUnavailable
	}
	if e := m.brief.principals.Lock(ctx, tx, actor); e != nil {
		return e
	}
	p, active, e := m.brief.principals.ActiveHumanByID(ctx, tx, actor)
	if e != nil {
		return e
	}
	if !active || p.ID != actor {
		return ErrForbidden
	}
	a, e := m.brief.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: actor, Permission: permission})
	if e != nil {
		return e
	}
	if !a.Allowed || a.Reason == "explicit_deny" || a.Reason == "administrator_scope_denied" {
		return ErrForbidden
	}
	return nil
}

func (m *Module) readChangeSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (ChangeSourceView, error) {
	var out ChangeSourceView
	if id <= 0 {
		return out, ErrInvalidArgument
	}
	if e := m.dispositionSourceReadAuthorityOn(ctx, tx, tenant, project, id); e != nil {
		return out, e
	}
	q := query.BindRecords(tx, m.names)
	r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.Kind.Eq("CHANGE")).Take()
	if e != nil {
		return out, e
	}
	cq := query.BindTriageCards(tx, m.names)
	c, e := cq.WithContext(ctx).Where(cq.TenantID.Eq(tenant), cq.IntakeID.Eq(id), cq.Revision.Eq(r.LatestCardRevision)).Take()
	if e != nil {
		return out, e
	}
	if r.TargetRequirementID == nil || r.ChangeOfIntakeID == nil || c.ChangeID == nil {
		return out, ErrConflict
	}
	return ChangeSourceView{ID: r.ID, ProjectID: project, RequirementID: *r.TargetRequirementID, OriginalIntakeID: *r.ChangeOfIntakeID, ChangeID: *c.ChangeID, State: r.Status, Version: r.Version, CardRevision: c.Revision, ProposalHash: c.ProposalHash, ProposalJSON: c.ProposalJSON, ImpactJSON: c.ImpactJSON, ResultVersionNo: r.ResultVersionNo}, nil
}

func (m *Module) draftChangeSourceOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, in ChangeSourceDraft) (ChangeSourceView, error) {
	var out ChangeSourceView
	if in.OriginalIntakeID <= 0 || in.RequirementID <= 0 || in.ChangeID <= 0 || len(in.ProposalHash) != 64 || !json.Valid([]byte(in.ProposalJSON)) || !json.Valid([]byte(in.ImpactJSON)) || len(in.ProposalJSON) > 65536 || len(in.ImpactJSON) > 1048576 || len(in.Title) == 0 || len([]rune(in.Title)) > 200 {
		return out, ErrInvalidArgument
	}
	if decoded, e := hex.DecodeString(in.ProposalHash); e != nil || len(decoded) != 32 {
		return out, ErrInvalidArgument
	}
	if e := m.changeSourceAuthority(ctx, tx, tenant, project, "scope_change"); e != nil {
		return out, e
	}
	if m.brief.ids == nil || m.brief.audit == nil || m.brief.events == nil {
		return out, ErrUnavailable
	}
	q := query.BindRecords(tx, m.names)
	// A genuine converted original guarantees a tenant row exists. Lock the
	// stable oldest row before any source row so independent projects serialize
	// tenant-wide reference allocation without a process-local mutex.
	if _, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant)).Order(q.ID.Asc()).Take(); e != nil {
		return out, e
	}
	original, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(in.OriginalIntakeID), q.RequirementID.Eq(in.RequirementID), q.Status.Eq("CONVERTED")).Take()
	if e != nil {
		return out, e
	}
	// One canonical source per approved change, even with a different request key.
	cq := query.BindTriageCards(tx, m.names)
	prior, e := cq.WithContext(ctx).Where(cq.TenantID.Eq(tenant), cq.ChangeID.Eq(in.ChangeID)).Take()
	if e == nil {
		out, e = m.readChangeSourceOn(ctx, tx, tenant, project, prior.IntakeID)
		if e == nil && (out.OriginalIntakeID != in.OriginalIntakeID || out.ProposalHash != in.ProposalHash) {
			e = ErrConflict
		}
		return out, e
	}
	if !errors.Is(e, gorm.ErrRecordNotFound) {
		return out, e
	}
	id, e := m.brief.ids.Next()
	if e != nil {
		return out, e
	}
	cardID, e := m.brief.ids.Next()
	if e != nil {
		return out, e
	}
	if id <= 0 || cardID <= 0 {
		return out, ErrUnavailable
	}
	var random [8]byte
	if _, e = rand.Read(random[:]); e != nil {
		return out, e
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	actor := authn.PrincipalIDFromContext(ctx)
	// A locking read observes the latest committed allocation even under
	// repeatable-read isolation after waiting for the tenant fence above.
	maxRow, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant)).Order(q.RefNo.Desc()).Take()
	if e != nil {
		return out, e
	}
	r := &model.Record{ID: guid.ID(id), TenantID: tenant, RefNo: maxRow.RefNo + 1, ChannelID: original.ChannelID, ChannelKind: original.ChannelKind, ContactID: original.ContactID, Kind: "CHANGE", Status: "TRIAGED", Title: in.Title, RawContent: &in.ProposalJSON, RawContentFormat: "PLAIN", RawContentHash: in.ProposalHash, ReplyToken: hex.EncodeToString(random[:]), ReceivedAt: at, ProjectID: &project, TargetRequirementID: &in.RequirementID, ChangeOfIntakeID: &in.OriginalIntakeID, LatestCardRevision: 1, Version: 1, CreatedAt: at, UpdatedAt: at, CreatedBy: &actor, UpdatedBy: &actor}
	if e = q.WithContext(ctx).Create(r); e != nil {
		return out, e
	}
	c := &model.TriageCard{ID: guid.ID(cardID), TenantID: tenant, IntakeID: r.ID, Revision: 1, ChangeID: &in.ChangeID, ProposalHash: in.ProposalHash, ProposalJSON: in.ProposalJSON, ImpactJSON: in.ImpactJSON, ActorID: actor, CreatedAt: at}
	if e = cq.WithContext(ctx).Create(c); e != nil {
		return out, e
	}
	if e = m.sourceEvent(ctx, tx, tenant, project, r.ID, "intake.triaged", 1, map[string]any{"change_id": in.ChangeID.String(), "card_revision": 1, "proposal_hash": in.ProposalHash}); e != nil {
		return out, e
	}
	return m.readChangeSourceOn(ctx, tx, tenant, project, r.ID)
}

func (m *Module) convertChangeSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, version, resultVersion int, change guid.ID) error {
	if resultVersion < 2 || change <= 0 {
		return ErrInvalidArgument
	}
	v, e := m.readChangeSourceOn(ctx, tx, tenant, project, id)
	if e != nil {
		return e
	}
	if v.State != "TRIAGED" || v.Version != version {
		return ErrConflict
	}
	q := query.BindRecords(tx, m.names)
	at := time.Now().UTC().Truncate(time.Millisecond)
	actor := authn.PrincipalIDFromContext(ctx)
	mode, kind, authority := "APPEND_TO_REQUIREMENT", "HUMAN", "IAM:scope_change"
	result, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version), q.Status.Eq("TRIAGED")).Updates(map[string]any{"status": "CONVERTED", "requirement_id": v.RequirementID, "conversion_mode": mode, "result_version_no": resultVersion, "version": version + 1, "resolved_at": at, "resolved_actor_kind": kind, "resolved_actor_id": actor, "resolved_authority_source": authority, "updated_at": at, "updated_by": actor})
	if e != nil {
		return e
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	return m.sourceEvent(ctx, tx, tenant, project, id, "intake.converted", version+1, map[string]any{"mode": mode, "source_intake_id": id.String(), "change_record_id": change.String(), "result_version_no": resultVersion, "card_revision": v.CardRevision, "proposal_hash": v.ProposalHash})
}

func (m *Module) sourceEvent(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, kind string, version int, data map[string]any, authority ...string) error {
	source := "IAM:scope_change"
	if len(authority) > 0 {
		source = authority[0]
	}
	actorKind := "HUMAN"
	if data["actor_kind"] == "AGENT" {
		actorKind = "AGENT"
	}
	event := audit.NewDomainEvent(ctx, tenant, kind, "intake", id)
	event.Detail = data
	event.Detail["authority_source"] = source
	event.Detail["actor_kind"] = actorKind
	if e := m.brief.audit(ctx, tx, event); e != nil {
		return e
	}
	_, e := m.brief.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: kind, Actor: events.Actor{Kind: actorKind, ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: source}, Subject: events.Subject{Kind: "intake", ID: id}, SubjectVersion: int64(version), Data: data})
	return e
}
