package delivery

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/internal/performance"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrArchivePreflight = errors.New("delivery: archive resources require disposition")

// registerReviewHTTP adds an exact read without changing the collection or
// command contracts owned by the review implementation.
func (m *Module) registerReviewHTTP(api huma.API) {
	m.archiveReviewsRegisterDefectHTTP(api)
	authz.RegisterOwned(m.registrar, api, huma.Operation{
		OperationID: "review_sheet.get", Method: "GET",
		Path:    "/entities/{entity_id}/review-sheets/{sheet_id}",
		Summary: "Read a review sheet and its frozen target", Tags: []string{"entities"},
		Errors: []int{400, 401, 403, 404, 422, 500, 503},
	}, authz.EntityPath, authz.ResourceOwnerResolver(m.entityOwner), authz.Guard{Resource: "requirement", Verb: "read"}, m.getReviewHTTP)
}

func (m *Module) getReviewHTTP(ctx context.Context, in *RequirementReviewSheetPath) (*respx.Body[RequirementReviewSheetView], error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	id, err := guid.RequireID(in.SheetID)
	if err != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.service == nil {
		return nil, requirementHTTPError(ctx, ErrUnavailable)
	}
	var out RequirementReviewSheetView
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindReviewSheets(tx, m.service.names)
		row, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
		if e != nil {
			return e
		}
		// Recheck the actual current target, including work-item visibility and
		// archived targets. Possession of a sheet ID never grants target access.
		if _, _, e = m.reviewTargetOn(ctx, tx, project, row.RequirementID, row.WorkItemID); e != nil {
			return e
		}
		out, e = sheetView(row)
		return e
	})
	if err != nil {
		return nil, requirementHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

// ProjectArchiveResource is an owner-observed resource state. Ready means the
// owner has persisted the required disposition, never that a request was sent.
// Owners must serialize creation and disposition on the same project lock.
type ProjectArchiveResource struct {
	Kind    string  `json:"kind"`
	ID      guid.ID `json:"id"`
	State   string  `json:"state"`
	Version int     `json:"version"`
	Ready   bool    `json:"ready"`
}
type ProjectArchiveReader func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error)

// ProjectArchiveConnector permits later resource owners to contribute receipts
// without reversing module dependencies. Binding is initialization-only.
type ProjectArchiveConnector struct {
	Bind func(string, ProjectArchiveReader) error
}
type projectArchiveSources map[string]ProjectArchiveReader

func (m *Module) injectArchiveReviews(s *module.Services) error {
	evidence, evidenceErr := module.Resolve[performance.EvidenceReaderConnector](s)
	if evidenceErr != nil {
		return evidenceErr
	}
	if evidenceErr = evidence.Bind(performance.EvidenceReader{SubjectOn: m.performanceSubjectOn, ReadOn: m.performanceEvidenceOn}); evidenceErr != nil {
		return evidenceErr
	}
	state, stateErr := module.Resolve[knowledge.ProjectArchiveStateConnector](s)
	if stateErr != nil {
		return stateErr
	}
	if stateErr = state.Bind(m.archiveProjectStateOn); stateErr != nil {
		return stateErr
	}
	fence, fenceErr := module.Resolve[core.EntityStateFenceConnector](s)
	if fenceErr != nil {
		return fenceErr
	}
	if fenceErr = fence.Bind(m.archiveEntityFenceOn); fenceErr != nil {
		return fenceErr
	}
	if m.archiveSources == nil {
		m.archiveSources = projectArchiveSources{}
	}
	memories, memoryErr := module.Resolve[knowledge.ProjectArchiveFacts](s)
	if memoryErr != nil && !errors.Is(memoryErr, module.ErrServiceMissing) {
		return memoryErr
	}
	if memories != nil {
		m.archiveSources["knowledge"] = func(ctx context.Context, tx *gorm.DB, t, p guid.ID) ([]ProjectArchiveResource, error) {
			facts, err := memories(ctx, tx, t, p)
			out := []ProjectArchiveResource{}
			for _, r := range facts {
				out = append(out, ProjectArchiveResource{Kind: "MEMORY_APPROVAL", ID: r.ID, State: r.State, Version: r.Version})
			}
			return out, err
		}
	}
	terminals, err := module.Resolve[fleet.ProjectArchiveFacts](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if terminals != nil {
		m.archiveSources["fleet"] = func(ctx context.Context, tx *gorm.DB, t, p guid.ID) ([]ProjectArchiveResource, error) {
			facts, e := terminals(ctx, tx, t, p)
			out := []ProjectArchiveResource{}
			for _, r := range facts {
				out = append(out, ProjectArchiveResource{Kind: r.Kind, ID: r.ID, State: r.State, Version: r.Version, Ready: r.Ready})
			}
			return out, e
		}
	}
	delegated, err := module.Resolve[governance.ProjectArchiveFacts](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if delegated != nil {
		m.archiveSources["governance"] = func(ctx context.Context, tx *gorm.DB, t, p guid.ID) ([]ProjectArchiveResource, error) {
			facts, e := delegated(ctx, tx, t, p)
			out := []ProjectArchiveResource{}
			for _, r := range facts {
				out = append(out, ProjectArchiveResource{Kind: r.Kind, ID: r.ID, State: r.State, Version: r.Version, Ready: r.Ready})
			}
			return out, e
		}
	}
	queued, err := module.Resolve[jobs.ProjectArchiveJobs](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if queued != nil {
		m.archiveSources["jobs"] = func(ctx context.Context, tx *gorm.DB, t, p guid.ID) ([]ProjectArchiveResource, error) {
			facts, e := queued(ctx, tx, t, p)
			out := []ProjectArchiveResource{}
			for _, r := range facts {
				out = append(out, ProjectArchiveResource{Kind: "BACKGROUND_JOB", ID: r.ID, State: r.State, Version: r.Version, Ready: false})
			}
			return out, e
		}
	}
	catalog, e := module.Resolve[events.Catalog](s)
	if e != nil {
		return e
	}
	return catalog.AddTypes(Name, events.TypeSpec{Name: "project.archived", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "project.restored", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal})
}

// performanceSubjectOn validates the report subject, independently of the
// performance module's administrator authorization. Archived projects remain valid.
func (m *Module) performanceSubjectOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind string, subject guid.ID) error {
	if m == nil || m.service == nil || tx == nil || tx.Statement == nil || tenant <= 0 || subject <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	switch kind {
	case "TEAM":
		if subject == tenant {
			return nil
		}
	case "PROJECT":
		q := query.BindProjects(tx, m.service.names)
		_, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(subject)).First()
		return err
	case "HUMAN", "AGENT":
		if m.members == nil || m.principals.Lock == nil {
			return ErrUnavailable
		}
		if err := m.principals.Lock(ctx, tx, subject); err != nil {
			return err
		}
		active, err := m.members.IsMemberActiveOn(ctx, tx, tenant, subject)
		if err != nil {
			return err
		}
		if !active {
			return ErrWorkForbidden
		}
		if kind == "HUMAN" {
			if m.principals.ActiveHumanByID == nil {
				return ErrUnavailable
			}
			human, found, err := m.principals.ActiveHumanByID(ctx, tx, subject)
			if err != nil {
				return err
			}
			if found && human.ID == subject {
				return nil
			}
		} else {
			if m.agents.ByPrincipalOn == nil {
				return ErrUnavailable
			}
			agent, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, subject)
			if err != nil {
				return err
			}
			if found && agent.State == "ACTIVE" && agent.TenantID == tenant && agent.PrincipalID == subject {
				return nil
			}
		}
	}
	return ErrWorkForbidden
}

// performanceEvidenceOn reads only immutable owner receipts. No archived-state
// filter or current assignee/state projection can rewrite historical attribution.
func (m *Module) performanceEvidenceOn(ctx context.Context, tx *gorm.DB, request performance.EvidenceRequest) (performance.EvidenceSet, error) {
	out := performance.EvidenceSet{Gaps: []string{
		"QUALITY_SCORE: no agreed score mapping for immutable quality receipts",
		"ON_TIME_RATIO: immutable deadline coverage unavailable",
		"PROGRESS_RATIO: immutable baseline denominator unavailable",
		"ADOPTION_SCORE: downstream adoption receipts unavailable",
		"DOWNSTREAM_VALUE_SCORE: downstream value receipts unavailable",
		"RISK_RESPONSE_SCORE: risk response receipts unavailable",
		"WORK_SUMMARY: immutable work titles and complete period state unavailable",
	}}
	if !request.Period.StartAt.Before(request.Period.CutoffAt) {
		return out, ErrInvalidArgument
	}
	if err := m.performanceSubjectOn(ctx, tx, request.TenantID, request.SubjectKind, request.SubjectID); err != nil {
		return out, err
	}
	individual := request.SubjectKind == "HUMAN" || request.SubjectKind == "AGENT"
	selected := func(project, actor guid.ID) bool {
		if individual {
			return actor == request.SubjectID
		}
		return request.SubjectKind == "TEAM" || project == request.SubjectID
	}
	// A fixed source receipt establishes participation in a work item, but does
	// not establish its historical title, deadline or complete period-end state.
	// Retain the actual work links and disclose those unknowns instead of either
	// dropping the work summary or reading today's mutable work row.
	workIndex := map[guid.ID]int{}
	addWork := func(id, project guid.ID, ref string) {
		index, exists := workIndex[id]
		if !exists {
			index = len(out.Work)
			workIndex[id] = index
			out.Work = append(out.Work, performance.WorkSummaryItem{WorkID: id, ProjectID: project, Title: id.String(), EvidenceRefs: []string{}, Gaps: []string{"Historical title unavailable", "Complete period-end state unavailable", "Immutable deadline and change baseline unavailable"}})
		}
		out.Work[index].EvidenceRefs = append(out.Work[index].EvidenceRefs, ref)
	}
	add := func(source, kind, role string, id, project, work guid.ID, version int, at time.Time, row any) (string, error) {
		if len(out.Facts) >= 10000 {
			return "", ErrUnavailable
		}
		data, err := json.Marshal(row)
		if err != nil {
			return "", err
		}
		digest := sha256.Sum256(data)
		ref := "delivery:" + source + ":" + id.String()
		out.Facts = append(out.Facts, performance.PerformanceEvidenceFact{WorkID: work, ID: ref, Kind: kind, Version: version, SHA256: hex.EncodeToString(digest[:]), ProjectID: project, OccurredAt: at.UTC(), SubjectID: request.SubjectID, SubjectKind: request.SubjectKind, Role: role, Reference: ref})
		return ref, nil
	}
	start, cutoff := request.Period.StartAt.UTC(), request.Period.CutoffAt.UTC()
	r := query.BindResultVersions(tx, m.service.names)
	results, err := r.WithContext(ctx).Where(r.TenantID.Eq(request.TenantID), r.CreatedAt.Gte(start), r.CreatedAt.Lt(cutoff)).Order(r.ID).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(results) > 10000 {
		return out, ErrUnavailable
	}
	for _, row := range results {
		if !selected(row.ProjectID, row.ActorID) {
			continue
		}
		snapshot := *row
		snapshot.CreatedAt = snapshot.CreatedAt.UTC()
		ref, e := add("result", "RESULT", "EXECUTOR", row.ID, row.ProjectID, row.WorkItemID, row.Sequence, row.CreatedAt, snapshot)
		if e != nil {
			return out, e
		}
		addWork(row.WorkItemID, row.ProjectID, ref)
	}
	a := query.BindResultActions(tx, m.service.names)
	actions, err := a.WithContext(ctx).Where(a.TenantID.Eq(request.TenantID), a.CreatedAt.Gte(start), a.CreatedAt.Lt(cutoff)).Order(a.ID).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(actions) > 10000 {
		return out, ErrUnavailable
	}
	for _, row := range actions {
		if !selected(row.ProjectID, row.ActorID) {
			continue
		}
		snapshot := *row
		snapshot.CreatedAt = snapshot.CreatedAt.UTC()
		ref, e := add("action", "DECISION", "DECISION_ACTOR", row.ID, row.ProjectID, row.WorkItemID, 1, row.CreatedAt, snapshot)
		if e != nil {
			return out, e
		}
		addWork(row.WorkItemID, row.ProjectID, ref)
	}
	w := query.BindWorkTransitions(tx, m.service.names)
	transitions, err := w.WithContext(ctx).Where(w.TenantID.Eq(request.TenantID), w.CreatedAt.Gte(start), w.CreatedAt.Lt(cutoff)).Order(w.ID).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(transitions) > 10000 {
		return out, ErrUnavailable
	}
	for _, row := range transitions {
		actor := guid.ID(0)
		if row.ActorID != nil {
			actor = *row.ActorID
		}
		if !selected(row.ProjectID, actor) {
			continue
		}
		snapshot := *row
		snapshot.CreatedAt = snapshot.CreatedAt.UTC()
		role := "DECISION_ACTOR"
		if actor == 0 {
			role = "SUBJECT"
		}
		ref, e := add("transition", "WORK_ITEM", role, row.ID, row.ProjectID, row.WorkItemID, row.VersionAfter, row.CreatedAt, snapshot)
		if e != nil {
			return out, e
		}
		addWork(row.WorkItemID, row.ProjectID, ref)
	}
	q := query.BindReportQualityReceipts(tx, m.service.names)
	receipts, err := q.WithContext(ctx).Where(q.TenantID.Eq(request.TenantID), q.OccurredAt.Gte(start), q.OccurredAt.Lt(cutoff)).Order(q.ID).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(receipts) > 10000 {
		return out, ErrUnavailable
	}
	marker := query.BindReportQualityObservations(tx, m.service.names)
	observed, err := marker.WithContext(ctx).Where(marker.Singleton.Eq(1)).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	covered := err == nil && !start.Before(observed.ObservedSince)
	refs := []string{}
	var rework int64
	workIDs := []guid.ID{}
	unknownExecutor := false
	for _, row := range receipts {
		actor := guid.ID(0)
		if row.ExecutorPrincipalID != nil {
			actor = *row.ExecutorPrincipalID
		}
		if individual && actor == 0 {
			covered = false
		}
		if actor == 0 && (individual || selected(row.ProjectID, actor)) {
			unknownExecutor = true
		}
		if !selected(row.ProjectID, actor) {
			continue
		}
		snapshot := *row
		snapshot.OccurredAt = snapshot.OccurredAt.UTC()
		role := "EXECUTOR"
		if actor == 0 {
			role = "SUBJECT"
		}
		ref, e := add("quality", "RESULT", role, row.ID, row.ProjectID, row.WorkItemID, 1, row.OccurredAt, snapshot)
		if e != nil {
			return out, e
		}
		addWork(row.WorkItemID, row.ProjectID, ref)
		if row.Kind == "INTERNAL_QA_PASSED" {
			refs = append(refs, ref)
			workIDs = append(workIDs, row.WorkItemID)
			if row.Rounds < 1 {
				covered = false
			} else {
				rework += int64(row.Rounds - 1)
			}
		}
	}
	if unknownExecutor {
		out.Gaps = append(out.Gaps, "ATTRIBUTION: historical quality receipt executor unavailable")
	}
	// The observation boundary alone cannot prove complete rounds for legacy
	// work. Only an immutable creation transition can establish post-marker birth.
	if covered && len(workIDs) > 0 {
		values := make([]driver.Valuer, len(workIDs))
		for i, id := range workIDs {
			values[i] = id
		}
		births, e := w.WithContext(ctx).Where(w.TenantID.Eq(request.TenantID), w.WorkItemID.In(values...), w.FromState.IsNull()).Limit(10001).Find()
		if e != nil {
			return out, e
		}
		if len(births) > 10000 {
			return out, ErrUnavailable
		}
		born := map[guid.ID]bool{}
		for _, birth := range births {
			if !birth.CreatedAt.Before(observed.ObservedSince) && birth.CreatedAt.Before(cutoff) {
				born[birth.WorkItemID] = true
			}
		}
		for _, id := range workIDs {
			if !born[id] {
				covered = false
			}
		}
	}
	if covered && len(refs) > 0 {
		if rework > 1000000000 {
			return out, ErrUnavailable
		}
		out.Metrics = append(out.Metrics, performance.MetricValue{Metric: "REWORK_COUNT", Value: rework, EvidenceRefs: refs})
	} else {
		out.Gaps = append(out.Gaps, "REWORK_COUNT: complete observed quality rounds unavailable")
	}
	return out, nil
}

func (m *Module) provideArchiveReviews(s *module.Services) error {
	if err := m.provideRubric(s); err != nil {
		return err
	}
	if err := module.Provide[TeamWorkOn](s, m.teamWorkOn); err != nil {
		return err
	}
	if err := module.Provide[TeamHumanOn](s, m.teamHumanOn); err != nil {
		return err
	}
	m.archiveSources = projectArchiveSources{}
	if err := module.Provide[WorkItemVisibilityOn](s, m.workVisibilityOn); err != nil {
		return err
	}
	return module.Provide[ProjectArchiveConnector](s, ProjectArchiveConnector{Bind: func(owner string, reader ProjectArchiveReader) error {
		if m.initialized || reader == nil || !slices.Contains([]string{"execution", "fleet", "planning", "governance", "jobs", "agents", "clientwork"}, owner) {
			return ErrInvalidArgument
		}
		if _, exists := m.archiveSources[owner]; exists {
			return ErrInvalidArgument
		}
		m.archiveSources[owner] = reader
		return nil
	}})
}

// TeamWorkFact is an authorized current snapshot, never a studio-supplied status.
type TeamWorkFact struct {
	ID        guid.ID   `json:"id"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	Risk      string    `json:"risk"`
	OwnerID   guid.ID   `json:"owner_id"`
	AuthorID  *guid.ID  `json:"author_id,omitempty"`
	Version   int       `json:"version"`
	StartedAt time.Time `json:"started_at"`
}
type TeamWorkOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (TeamWorkFact, error)

// TeamHumanOn checks current group membership without impersonating a signer.
type TeamHumanOn func(context.Context, *gorm.DB, guid.ID, guid.ID) error

func (m *Module) teamHumanOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) error {
	if tenant != authn.TenantIDFromContext(ctx) || id <= 0 {
		return ErrWorkForbidden
	}
	// Keep a reusable session: identity builds a service-account subquery from
	// this handle. A terminal statement handle would leak that predicate into
	// the principal/membership query.
	current := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Session(&gorm.Session{})
	human, active, err := m.principals.ActiveHumanByID(ctx, current, id)
	if err != nil {
		return err
	}
	member, err := m.members.IsMemberActiveOn(ctx, current, tenant, id)
	if err != nil {
		return err
	}
	if !active || !member || human.ID != id {
		return ErrWorkForbidden
	}
	return nil
}

func (m *Module) teamWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (TeamWorkFact, error) {
	var out TeamWorkFact
	if tx == nil || tenant != authn.TenantIDFromContext(ctx) || id < 0 {
		return out, ErrInvalidArgument
	}
	if err := m.authorizeOn(ctx, tx, tenant, project, "work_item_read"); err != nil {
		return out, err
	}
	p := query.BindProjects(tx, m.service.names)
	pr, err := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.ArchivedAt.IsNull()).Take()
	if err != nil {
		return out, err
	}
	if pr.TeamMode != "HUMAN_ONLY" {
		return out, ErrWorkForbidden
	}
	if id == 0 {
		return out, nil
	}
	c, err := m.workPolicy(ctx, tx, tenant, project, "work_item_read", authn.PrincipalIDFromContext(ctx))
	if err != nil {
		return out, err
	}
	q := query.BindWorkItems(tx, m.service.names)
	scope, err := workScope(c, project, q.CreatedBy)
	if err != nil {
		return out, err
	}
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.ArchivedAt.IsNull())
	if scope != nil {
		base = base.Where(scope)
	}
	w, err := base.Clauses(clause.Locking{Strength: "UPDATE"}).Take()
	if err != nil {
		return out, err
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, w.AccountablePrincipalID)
	if err != nil {
		return out, err
	}
	member, err := m.members.IsMemberActiveOn(ctx, tx, tenant, w.AccountablePrincipalID)
	if err != nil {
		return out, err
	}
	if !active || !member || human.ID != w.AccountablePrincipalID {
		return out, ErrWorkForbidden
	}
	started := w.CreatedAt
	if w.FirstClaimedAt != nil {
		started = *w.FirstClaimedAt
	}
	return TeamWorkFact{ID: w.ID, Title: w.Title, State: w.State, Risk: w.Risk, OwnerID: w.AccountablePrincipalID, AuthorID: w.CreatedBy, Version: w.Version, StartedAt: started}, nil
}

type projectArchivePreflight struct {
	ProjectID         guid.ID                  `json:"project_id"`
	Version           int                      `json:"version"`
	ArchivedAt        *time.Time               `json:"archived_at"`
	Resources         []ProjectArchiveResource `json:"resources"`
	UnavailableOwners []string                 `json:"unavailable_owners"`
	Ready             bool                     `json:"ready"`
	Hash              string                   `json:"hash"`
}
type projectArchiveBody struct {
	Reason        string `json:"reason" minLength:"10" maxLength:"2000"`
	PreflightHash string `json:"preflight_hash" pattern:"^[a-f0-9]{64}$"`
}

// archiveProjectOn admits terminal business stages too. Archival is independent
// of completion/cancellation; neither stage nor child identities are changed.
func (m *Module) archiveProjectOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID) (*model.Project, error) {
	if m == nil || m.service == nil || tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || actor <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) != actor {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	if m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.workPolicy == nil || m.members == nil {
		return nil, ErrUnavailable
	}
	if err := m.principals.Lock(ctx, tx, actor); err != nil {
		return nil, err
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	if !active || human.ID != actor {
		return nil, ErrConstitutionHumanOnly
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, ErrInvalidMember
	}
	c, err := m.workPolicy(context.WithValue(ctx, archiveCommandAuthority{}, true), tx, tenant, project, "project_manage", actor)
	if err != nil {
		return nil, err
	}
	if !c.AllowsEntityRows(project) {
		return nil, ErrWorkForbidden
	}
	p := query.BindProjects(tx, m.service.names)
	scope, err := workScope(c, project, p.CreatedBy)
	if err != nil {
		return nil, err
	}
	base := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"})
	if scope != nil {
		base = base.Where(scope)
	}
	row, err := base.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectNotFound
	}
	return row, err
}

type archiveCommandAuthority struct{}

func (m *Module) projectHistoryAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, permission string) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || actor <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) != actor || !strings.HasSuffix(permission, "_read") {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if m == nil || m.service == nil || m.workPolicy == nil {
		return ErrUnavailable
	}
	c, err := m.workPolicy(ctx, tx, tenant, project, permission, actor)
	if err != nil {
		return err
	}
	if !c.AllowsEntityRows(project) {
		return ErrWorkForbidden
	}
	p := query.BindProjects(tx, m.service.names)
	scope, err := workScope(c, project, p.CreatedBy)
	if err != nil {
		return err
	}
	base := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"})
	if scope != nil {
		base = base.Where(scope)
	}
	_, err = base.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrProjectNotFound
	}
	return err
}

// All entity-scoped business authorization serializes with archival on the
// owner's project row. Non-project entities retain their existing IAM behavior.
func (m *Module) archiveEntityFenceOn(ctx context.Context, tx *gorm.DB, tenant, entity guid.ID, permission string) error {
	if m == nil || m.service == nil || tx == nil || tx.Statement == nil {
		return ErrUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	p := query.BindProjects(tx, m.service.names)
	row, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(entity)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.ArchivedAt != nil && (core.EntityMutationFromContext(ctx) || !strings.HasSuffix(permission, "_read")) && ctx.Value(archiveCommandAuthority{}) != true {
		return ErrWorkProjectReadOnly
	}
	return nil
}

// archiveProjectStateOn is an owner-only state read; callers still apply their
// own current authorization. The project lock serializes automatic writes.
func (m *Module) archiveProjectStateOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (bool, error) {
	if m == nil || m.service == nil || tx == nil || tx.Statement == nil {
		return false, ErrUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return false, ErrInvalidArgument
	}
	p := query.BindProjects(tx, m.service.names)
	row, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, ErrProjectNotFound
	}
	if err != nil {
		return false, err
	}
	return row.ArchivedAt != nil, nil
}

func (m *Module) archivePreflightOn(ctx context.Context, tx *gorm.DB, p *model.Project) (projectArchivePreflight, error) {
	out := projectArchivePreflight{ProjectID: p.ProjectID, Version: p.Version, ArchivedAt: p.ArchivedAt, Resources: []ProjectArchiveResource{}, UnavailableOwners: []string{}, Ready: true}
	// Keep every unfinished obligation visible. Pause is accepted only where the
	// work owner recorded an explicit pause; no approval/handoff is auto-canceled.
	w := query.BindWorkItems(tx, m.service.names)
	rows, err := w.WithContext(ctx).Where(w.TenantID.Eq(p.TenantID), w.ProjectID.Eq(p.ProjectID), w.ArchivedAt.IsNull(), w.IsContainer.Is(false)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(w.ID).Find()
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		settled := slices.Contains([]string{"ACCEPTED", "CANCELLED", "SKIPPED"}, r.State) //nolint:misspell // Preserve the published domain or serialized contract spelling.
		if !settled {
			out.Resources = append(out.Resources, ProjectArchiveResource{Kind: "WORK_ITEM", ID: r.ID, State: r.State, Version: r.Version, Ready: r.PausedAt != nil && r.CurrentExecutionID == nil && slices.Contains([]string{"DRAFT", "READY", "BLOCKED"}, r.State)})
		}
	}
	d := query.BindDeliveries(tx, m.service.names)
	deliveries, err := d.WithContext(ctx).Where(d.TenantID.Eq(p.TenantID), d.ProjectID.Eq(p.ProjectID), d.ArchivedAt.IsNull()).Clauses(clause.Locking{Strength: "UPDATE"}).Order(d.ID).Find()
	if err != nil {
		return out, err
	}
	for _, r := range deliveries {
		out.Resources = append(out.Resources, ProjectArchiveResource{Kind: "DELIVERY", ID: r.ID, State: r.State, Version: r.Version, Ready: slices.Contains([]string{"ACCEPTED", "REJECTED", "PARTIALLY_ACCEPTED"}, r.State) && r.ResultAt != nil})
	}
	h := query.BindResultHandoffs(tx, m.service.names)
	handoffs, err := h.WithContext(ctx).Where(h.TenantID.Eq(p.TenantID), h.ProjectID.Eq(p.ProjectID)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(h.ID).Find()
	if err != nil {
		return out, err
	}
	for _, r := range handoffs {
		a := query.BindResultActions(tx, m.service.names)
		actions, e := a.WithContext(ctx).Where(a.TenantID.Eq(p.TenantID), a.ProjectID.Eq(p.ProjectID), a.HandoffID.Eq(r.ID), a.Kind.In("RECEIPT", "RETURN")).Clauses(clause.Locking{Strength: "UPDATE"}).Order(a.ID).Find()
		if e != nil {
			return out, e
		}
		ready := false
		if len(actions) == 1 {
			for _, delivered := range deliveries {
				if delivered.ID == r.DeliveryID && delivered.AcceptanceID != nil && *delivered.AcceptanceID == actions[0].ID && delivered.ResultAt != nil && ((actions[0].Kind == "RECEIPT" && delivered.State == "ACCEPTED") || (actions[0].Kind == "RETURN" && delivered.State == "REJECTED")) {
					ready = true
					break
				}
			}
		}
		state := "AWAITING_RECEIPT"
		if ready {
			state = actions[0].Kind
		}
		out.Resources = append(out.Resources, ProjectArchiveResource{Kind: "RESULT_HANDOFF", ID: r.ID, State: state, Version: 1, Ready: ready})
	}
	c := query.BindConstitutions(tx, m.service.names)
	constitutions, err := c.WithContext(ctx).Where(c.TenantID.Eq(p.TenantID), c.ProjectID.Eq(p.ProjectID), c.State.Eq("IN_REVIEW")).Clauses(clause.Locking{Strength: "UPDATE"}).Order(c.ID).Find()
	if err != nil {
		return out, err
	}
	for _, row := range constitutions {
		out.Resources = append(out.Resources, ProjectArchiveResource{Kind: "CONSTITUTION_REVIEW", ID: row.ID, State: row.State, Version: row.Version})
	}
	rp := query.BindReviewPlans(tx, m.service.names)
	plans, err := rp.WithContext(ctx).Where(rp.TenantID.Eq(p.TenantID), rp.ProjectID.Eq(p.ProjectID), rp.State.Eq("PROPOSED")).Clauses(clause.Locking{Strength: "UPDATE"}).Order(rp.ID).Find()
	if err != nil {
		return out, err
	}
	for _, row := range plans {
		out.Resources = append(out.Resources, ProjectArchiveResource{Kind: "WORK_PLAN_REVIEW", ID: row.ID, State: row.State, Version: row.Version})
	}
	for _, owner := range []string{"execution", "fleet", "governance", "jobs", "planning", "agents", "clientwork", "knowledge"} {
		read := m.archiveSources[owner]
		if read == nil {
			out.UnavailableOwners = append(out.UnavailableOwners, owner)
			out.Ready = false
			continue
		}
		resources, e := read(ctx, tx, p.TenantID, p.ProjectID)
		if e != nil {
			return out, e
		}
		for _, r := range resources {
			if r.ID <= 0 || r.Kind == "" || r.State == "" || r.Version < 1 {
				return out, ErrUnavailable
			}
			out.Resources = append(out.Resources, r)
		}
	}
	slices.SortFunc(out.Resources, func(a, b ProjectArchiveResource) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	for i, r := range out.Resources {
		if !r.Ready {
			out.Ready = false
		}
		if i > 0 && out.Resources[i-1].Kind == r.Kind && out.Resources[i-1].ID == r.ID {
			return out, ErrUnavailable
		}
	}
	raw, e := json.Marshal(out)
	if e != nil {
		return out, e
	}
	sum := sha256.Sum256(raw)
	out.Hash = hex.EncodeToString(sum[:])
	return out, nil
}

func (m *Module) changeProjectArchiveOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, version int, restore bool, in projectArchiveBody) (ProjectView, error) {
	var empty ProjectView
	reason := strings.TrimSpace(in.Reason)
	if !utf8.ValidString(reason) || utf8.RuneCountInString(reason) < 10 || utf8.RuneCountInString(reason) > 2000 || version < 1 {
		return empty, ErrInvalidArgument
	}
	if m == nil || m.service == nil || m.service.audit == nil || m.service.events == nil {
		return empty, ErrUnavailable
	}
	row, err := m.archiveProjectOn(ctx, tx, tenant, project, actor)
	if err != nil {
		return empty, err
	}
	if row.Version != version {
		return empty, ErrWorkVersion
	}
	if restore != (row.ArchivedAt != nil) {
		return empty, ErrWorkVersion
	}
	var preflight *projectArchivePreflight
	if !restore {
		checked, e := m.archivePreflightOn(ctx, tx, row)
		if e != nil {
			return empty, e
		}
		if !checked.Ready || in.PreflightHash != checked.Hash {
			return empty, ErrArchivePreflight
		}
		preflight = &checked
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	row.ArchivedAt = &now
	if restore {
		row.ArchivedAt = nil
	}
	row.Version++
	row.UpdatedAt = now
	row.UpdatedBy = &actor
	p := query.BindProjects(tx, m.service.names)
	result, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.Version.Eq(version)).Select(p.ArchivedAt, p.Version, p.UpdatedAt, p.UpdatedBy).Updates(row)
	if err != nil {
		return empty, err
	}
	if result.RowsAffected != 1 {
		return empty, ErrWorkVersion
	}
	action := "archived"
	if restore {
		action = "restored"
	}
	ev := audit.NewDomainEvent(ctx, tenant, "delivery.project_"+action, "project", project)
	ev.Detail["reason"], ev.Detail["version"], ev.Detail["preflight"] = reason, row.Version, preflight
	if err = m.service.audit(ctx, tx, ev); err != nil {
		return empty, err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "project." + action, Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "PERMISSION:project_manage"}, Subject: events.Subject{Kind: "project", ID: project}, SubjectVersion: int64(row.Version), Data: map[string]any{"reason": reason, "preflight": preflight}})
	if err != nil {
		return empty, err
	}
	return projectRecordView(*row)
}

// ReviewCursorPage uses the product's bounded keyset collection protocol.
// Counts are intentionally absent: unreadable targets must never affect totals.
type ReviewCursorPage[T any] struct {
	Items      []T    `json:"items"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
}
type ReviewCursorInput struct {
	Cursor string `query:"cursor" maxLength:"200"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
}
type sheetListInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ReviewCursorInput
}
type opinionListInput struct {
	RequirementReviewSheetPath
	ReviewCursorInput
}
type archiveReviewsReviewTypeListInput struct {
	RequirementReviewTypeTenantPath
	ProjectID string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ReviewCursorInput
}

func reviewPosition(ctx context.Context, kind string, project guid.ID, in ReviewCursorInput) (guid.ID, error) {
	if in.Limit < 1 || in.Limit > 100 || len(in.Cursor) > 200 {
		return 0, ErrInvalidArgument
	}
	raw := in.Cursor
	if raw != "" {
		var ok bool
		raw, ok = strings.CutPrefix(raw, kind+".")
		if !ok {
			return 0, ErrInvalidArgument
		}
	}
	return personalWorkPosition(raw, authn.TenantIDFromContext(ctx), project, authn.PrincipalIDFromContext(ctx))
}
func reviewNext[T any](ctx context.Context, kind string, project guid.ID, limit int, items []T, id func(T) guid.ID) ReviewCursorPage[T] {
	page := ReviewCursorPage[T]{Items: items, HasMore: len(items) > limit}
	if page.HasMore {
		page.Items = items[:limit]
		page.NextCursor = kind + "." + personalWorkCursor(authn.TenantIDFromContext(ctx), project, authn.PrincipalIDFromContext(ctx), id(page.Items[limit-1]))
	}
	return page
}
func (m *Module) reviewDatabase() *gorm.DB {
	if m == nil || m.service == nil || m.runtime == nil || m.runtime.Database() == nil {
		return nil
	}
	return m.runtime.Database().Write()
}
func (m *Module) archiveReviewsListSheetsHTTP(ctx context.Context, in *sheetListInput) (*respx.Body[ReviewCursorPage[RequirementReviewSheetView]], error) {
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	after, e := reviewPosition(ctx, "sheets", project, in.ReviewCursorInput)
	if e != nil {
		return nil, requirementHTTPError(ctx, e)
	}
	db := m.reviewDatabase()
	if db == nil {
		return nil, requirementHTTPError(ctx, ErrUnavailable)
	}
	out := []RequirementReviewSheetView{}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindReviewSheets(tx, m.service.names)
		for len(out) <= in.Limit {
			base := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project))
			if after > 0 {
				base = base.Where(q.ID.Lt(after))
			}
			rows, err := base.Order(q.ID.Desc()).Limit(100).Find()
			if err != nil {
				return err
			}
			for _, r := range rows {
				after = r.ID
				_, _, err = m.reviewTargetOn(ctx, tx, project, r.RequirementID, r.WorkItemID)
				if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrWorkForbidden) {
					continue
				}
				if err != nil {
					return err
				}
				v, err := sheetView(r)
				if err != nil {
					return err
				}
				out = append(out, v)
				if len(out) > in.Limit {
					break
				}
			}
			if len(rows) < 100 {
				break
			}
		}
		return nil
	})
	if e != nil {
		return nil, requirementHTTPError(ctx, e)
	}
	return respx.OK(ctx, reviewNext(ctx, "sheets", project, in.Limit, out, func(v RequirementReviewSheetView) guid.ID { return v.ID })), nil
}
func (m *Module) listOpinionsHTTP(ctx context.Context, in *opinionListInput) (*respx.Body[ReviewCursorPage[RequirementReviewOpinionRecord]], error) {
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	sheet, e := guid.RequireID(in.SheetID)
	if e != nil {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	kind := "opinions-" + sheet.String()
	after, e := reviewPosition(ctx, kind, project, in.ReviewCursorInput)
	if e != nil {
		return nil, requirementHTTPError(ctx, e)
	}
	db := m.reviewDatabase()
	if db == nil {
		return nil, requirementHTTPError(ctx, ErrUnavailable)
	}
	out := []RequirementReviewOpinionRecord{}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindReviewSheets(tx, m.service.names)
		row, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project), q.ID.Eq(sheet)).Take()
		if err != nil {
			return err
		}
		if _, _, err = m.reviewTargetOn(ctx, tx, project, row.RequirementID, row.WorkItemID); err != nil {
			return err
		}
		h := query.BindReviewOpinions(tx, m.service.names)
		base := h.WithContext(ctx).Where(h.TenantID.Eq(row.TenantID), h.ProjectID.Eq(project), h.SheetID.Eq(sheet))
		if after > 0 {
			base = base.Where(h.ID.Lt(after))
		}
		rows, err := base.Order(h.ID.Desc()).Limit(in.Limit + 1).Find()
		if err != nil {
			return err
		}
		for _, r := range rows {
			v := RequirementReviewOpinionRecord{ID: r.ID, SupersedesID: r.SupersedesID}
			if err = json.Unmarshal([]byte(r.Document), &v.Opinion); err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	if e != nil {
		return nil, requirementHTTPError(ctx, e)
	}
	return respx.OK(ctx, reviewNext(ctx, kind, project, in.Limit, out, func(v RequirementReviewOpinionRecord) guid.ID { return v.ID })), nil
}
func (m *Module) archiveReviewsListReviewTypesHTTP(ctx context.Context, in *archiveReviewsReviewTypeListInput) (*respx.Body[ReviewCursorPage[RequirementReviewTypeView]], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil || tenant != authn.TenantIDFromContext(ctx) {
		return nil, requirementHTTPError(ctx, ErrInvalidArgument)
	}
	var project guid.ID
	if in.ProjectID != "" {
		project, e = guid.RequireID(in.ProjectID)
		if e != nil {
			return nil, requirementHTTPError(ctx, ErrInvalidArgument)
		}
	}
	after, e := reviewPosition(ctx, "types", project, in.ReviewCursorInput)
	if e != nil {
		return nil, requirementHTTPError(ctx, e)
	}
	db := m.reviewDatabase()
	if db == nil {
		return nil, requirementHTTPError(ctx, ErrUnavailable)
	}
	out := []RequirementReviewTypeView{}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.reviewTypeAuthorityOn(ctx, tx, tenant, "requirement_read"); err != nil {
			return err
		}
		if project > 0 {
			if err := m.sheetAuthorityOn(ctx, tx, project, "requirement_read"); err != nil {
				return err
			}
		}
		q := query.BindReviewTypes(tx, m.service.names)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(after)).Order(q.ID).Limit(in.Limit + 1).Find()
		if err != nil {
			return err
		}
		for _, r := range rows {
			v, err := reviewTypeView(r)
			if err != nil {
				return err
			}
			if project > 0 {
				var d reviewTypeDocument
				if err = json.Unmarshal([]byte(r.Document), &d); err != nil {
					return err
				}
				if snapshot, ok := d.ProjectCopies[project.String()]; ok {
					v = snapshot
				}
			}
			// The immutable collection position is the source row, not a mutable local copy.
			v.ID = r.ID
			out = append(out, v)
		}
		return nil
	})
	if e != nil {
		return nil, requirementHTTPError(ctx, e)
	}
	return respx.OK(ctx, reviewNext(ctx, "types", project, in.Limit, out, func(v RequirementReviewTypeView) guid.ID { return v.ID })), nil
}

// DefectDescription stores reported facts separately from runtime acceptance.
// A resolution is documentation; it never advances state or creates evidence.
type DefectDescription struct {
	Environment           string   `json:"environment" maxLength:"200"`
	Steps                 string   `json:"steps" minLength:"1" maxLength:"20000"`
	Expected              string   `json:"expected" minLength:"1" maxLength:"20000"`
	Actual                string   `json:"actual" minLength:"1" maxLength:"20000"`
	RootCause             string   `json:"root_cause" maxLength:"20000"`
	FixSuggestion         string   `json:"fix_suggestion" maxLength:"20000"`
	Resolution            *string  `json:"resolution" enum:"FIXED,WONT_FIX,INVALID,DUPLICATE,BY_DESIGN"`
	DuplicateOfWorkItemID *guid.ID `json:"duplicate_of_work_item_id"`
}

func (DefectDescription) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	// JSON Schema's nullable type does not override an enum restriction.
	schema.Properties["resolution"].Enum = append(schema.Properties["resolution"].Enum, nil)
	return schema
}

func (in DefectDescription) validate() error {
	for _, s := range []string{in.Steps, in.Expected, in.Actual} {
		if !utf8.ValidString(s) || strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > 20000 {
			return ErrInvalidArgument
		}
	}
	for _, s := range []string{in.RootCause, in.FixSuggestion} {
		if !utf8.ValidString(s) || utf8.RuneCountInString(s) > 20000 {
			return ErrInvalidArgument
		}
	}
	if !utf8.ValidString(in.Environment) || utf8.RuneCountInString(in.Environment) > 200 {
		return ErrInvalidArgument
	}
	if in.Resolution != nil && !slices.Contains([]string{"FIXED", "WONT_FIX", "INVALID", "DUPLICATE", "BY_DESIGN"}, *in.Resolution) {
		return ErrInvalidArgument
	}
	duplicate := in.Resolution != nil && *in.Resolution == "DUPLICATE"
	if duplicate != (in.DuplicateOfWorkItemID != nil) || (in.DuplicateOfWorkItemID != nil && *in.DuplicateOfWorkItemID <= 0) {
		return ErrInvalidArgument
	}
	return nil
}

type DefectDetailView struct {
	WorkItemID       guid.ID            `json:"work_item_id"`
	Version          int                `json:"version"`
	Type             string             `json:"type"`
	Severity         *string            `json:"severity"`
	Priority         *string            `json:"priority"`
	Reproducibility  *string            `json:"reproducibility"`
	FoundVersion     *string            `json:"found_version"`
	FixedVersion     *string            `json:"fixed_version"`
	IntroducedStage  *string            `json:"introduced_stage"`
	IntroducedReason *string            `json:"introduced_reason"`
	Module           *string            `json:"module"`
	Details          *DefectDescription `json:"details" doc:"Null for legacy records with no structured report; missing facts are never inferred from description."`
}

// Huma does not mark referenced object pointers nullable automatically. Keep
// the published legacy response consistent with its explicit details:null.
func (DefectDetailView) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Properties["details"] = &huma.Schema{AnyOf: []*huma.Schema{schema.Properties["details"], {Type: "null"}}}
	return schema
}

type DefectPath struct {
	WorkItemID string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

// The schema hint gives this anonymous update body a stable capability name.
type defectUpdateBody = struct {
	Severity         string            `json:"severity" enum:"FATAL,CRITICAL,NORMAL,MINOR"`
	Priority         string            `json:"priority" enum:"URGENT,HIGH,MEDIUM,LOW"`
	Reproducibility  string            `json:"reproducibility" enum:"ALWAYS,HIGH,MEDIUM,LOW,UNABLE"`
	FoundVersion     *string           `json:"found_version" maxLength:"64"`
	FixedVersion     *string           `json:"fixed_version" maxLength:"64"`
	IntroducedStage  *string           `json:"introduced_stage" maxLength:"32"`
	IntroducedReason *string           `json:"introduced_reason" maxLength:"32"`
	Module           *string           `json:"module" maxLength:"80"`
	Details          DefectDescription `json:"details"`
}
type defectUpdateInput struct {
	DefectPath
	Key     string           `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	IfMatch string           `header:"If-Match" required:"true" doc:"Quoted current WorkItem version."`
	Body    defectUpdateBody `nameHint:"DefectUpdateBody"`
}
type DefectDetailReceipt struct {
	WorkItemID guid.ID `json:"work_item_id"`
	Version    int     `json:"version"`
}

func defectView(row *model.WorkItem) (DefectDetailView, error) {
	v := DefectDetailView{WorkItemID: row.ID, Version: row.Version, Type: row.Type, Severity: row.Severity, Priority: row.Priority, Reproducibility: row.Reproducibility, FoundVersion: row.FoundVersion, FixedVersion: row.FixedVersion, IntroducedStage: row.IntroducedStage, IntroducedReason: row.IntroducedReason, Module: row.Module}
	if row.Type != "BUG" && row.Type != "INCIDENT" {
		return v, ErrInvalidArgument
	}
	if row.DefectDetails != nil {
		v.Details = &DefectDescription{}
		if err := json.Unmarshal([]byte(*row.DefectDetails), v.Details); err != nil {
			return v, err
		}
	}
	return v, nil
}

// WorkItemVisibilityOn checks the current target row's IAM data scope on the
// caller's transaction, rather than treating a valid project as target access.
type WorkItemVisibilityOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error

func (m *Module) workVisibilityOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant != authn.TenantIDFromContext(ctx) {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	_, err := m.visibleWorkOn(ctx, tx, project, id)
	return err
}
func (m *Module) readDefectOn(ctx context.Context, tx *gorm.DB, project, id guid.ID) (DefectDetailView, error) {
	row, err := m.visibleWorkOn(ctx, tx, project, id)
	if err != nil {
		return DefectDetailView{}, err
	}
	return defectView(row)
}
func (m *Module) visibleWorkOn(ctx context.Context, tx *gorm.DB, project, id guid.ID) (*model.WorkItem, error) {
	if m == nil || m.service == nil || m.workPolicy == nil {
		return nil, ErrUnavailable
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	if tenant <= 0 || actor <= 0 || project <= 0 || id <= 0 {
		return nil, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	c, err := m.workPolicy(ctx, tx, tenant, project, "work_item_read", actor)
	if err != nil {
		return nil, err
	}
	scope, err := workScope(c, project, w.CreatedBy)
	if err != nil {
		return nil, err
	}
	base := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(id), w.ArchivedAt.IsNull())
	if scope != nil {
		base = base.Where(scope)
	}
	row, err := base.Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectNotFound
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}
func (m *Module) archiveReviewsRegisterDefectHTTP(api huma.API) {
	owner := authz.ResourceOwnerResolver(m.workOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.defect.get", Method: "GET", Path: "/work-items/{work_item_id}/defect", Summary: "Read structured defect facts", Tags: []string{"work-items"}, Errors: []int{401, 403, 404, 422, 500, 503}}, "work_item_id", owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.getDefectHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.defect.replace", Method: "PUT", Path: "/work-items/{work_item_id}/defect", Summary: "Replace defect report without changing lifecycle", Tags: []string{"work-items"}, Errors: []int{400, 401, 403, 404, 409, 412, 422, 500, 503}}, "work_item_id", owner, authz.Guard{Resource: "work_item", Verb: "write"}, m.replaceDefectHTTP)
}
func (m *Module) getDefectHTTP(ctx context.Context, in *DefectPath) (*respx.Body[DefectDetailView], error) {
	id, e := guid.RequireID(in.WorkItemID)
	if e != nil {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	db := m.reviewDatabase()
	if db == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	var v DefectDetailView
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		v, err = m.readDefectOn(ctx, tx, authn.EntityIDFromContext(ctx), id)
		return err
	})
	if e != nil {
		return nil, workHTTPError(ctx, e)
	}
	return respx.OK(ctx, v), nil
}
func (m *Module) replaceDefectHTTP(ctx context.Context, in *defectUpdateInput) (*deliveryCommandOutput[DefectDetailReceipt], error) {
	project, id, version, e := deliveryEditIDs(authn.EntityIDFromContext(ctx).String(), in.WorkItemID, in.IfMatch)
	if e != nil {
		return nil, workHTTPError(ctx, e)
	}
	if m.reviewDatabase() == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	// Resolve the actual owner before the sorted principal/project/work fence.
	authorize := func(tx *gorm.DB) error {
		w := query.BindWorkItems(tx, m.service.names)
		r, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(id), w.ArchivedAt.IsNull()).Take()
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrProjectNotFound
			}
			return err
		}
		_, _, _, err = m.lockedWorkOn(ctx, tx, tenant, project, id, actor, r.AccountablePrincipalID, "work_item_write")
		return err
	}
	return deliveryCommand(ctx, m, "work_item", workHTTPError, "work_item.defect.replace", "PUT", "/work-items/{work_item_id}/defect", in.Key, 200, struct {
		Version int
		Body    defectUpdateBody `nameHint:"DefectUpdateBody"`
	}{version, in.Body}, map[string]string{"work_item_id": id.String()}, func(tx *gorm.DB) (DefectDetailReceipt, guid.ID, error) {
		w := query.BindWorkItems(tx, m.service.names)
		row, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(id)).Take()
		if err != nil {
			return DefectDetailReceipt{}, 0, err
		}
		receipt, err := m.saveDefectOn(ctx, tx, project, row, version, in.Body)
		return receipt, id, err
	}, authorize)
}

// saveDefectOn runs only after the caller's current-authority and sorted
// principal/project/work locks, in that same transaction.
func (m *Module) saveDefectOn(ctx context.Context, tx *gorm.DB, project guid.ID, row *model.WorkItem, version int, in defectUpdateBody) (DefectDetailReceipt, error) {
	empty := DefectDetailReceipt{}
	if row.Version != version {
		return empty, ErrWorkVersion
	}
	if row.Type != "BUG" && row.Type != "INCIDENT" {
		return empty, ErrInvalidArgument
	}
	if e := in.Details.validate(); e != nil {
		return empty, e
	}
	if !slices.Contains([]string{"FATAL", "CRITICAL", "NORMAL", "MINOR"}, in.Severity) || !slices.Contains([]string{"URGENT", "HIGH", "MEDIUM", "LOW"}, in.Priority) || !slices.Contains([]string{"ALWAYS", "HIGH", "MEDIUM", "LOW", "UNABLE"}, in.Reproducibility) {
		return empty, ErrInvalidArgument
	}
	for _, v := range []struct {
		s   *string
		max int
	}{{in.FoundVersion, 64}, {in.FixedVersion, 64}, {in.IntroducedStage, 32}, {in.IntroducedReason, 32}, {in.Module, 80}} {
		if v.s != nil && (!utf8.ValidString(*v.s) || utf8.RuneCountInString(*v.s) > v.max) {
			return empty, ErrInvalidArgument
		}
	}
	if in.Details.DuplicateOfWorkItemID != nil {
		if *in.Details.DuplicateOfWorkItemID == row.ID {
			return empty, ErrInvalidArgument
		}
		if _, e := m.readDefectOn(ctx, tx, project, *in.Details.DuplicateOfWorkItemID); e != nil {
			return empty, e
		}
	}
	encoded, e := json.Marshal(in.Details)
	if e != nil {
		return empty, e
	}
	raw := string(encoded)
	now := time.Now().UTC().Truncate(time.Millisecond)
	actor := authn.PrincipalIDFromContext(ctx)
	w := query.BindWorkItems(tx, m.service.names)
	result, e := w.WithContext(ctx).Where(w.TenantID.Eq(row.TenantID), w.ProjectID.Eq(project), w.ID.Eq(row.ID), w.Version.Eq(version)).Updates(map[string]any{"defect_details": raw, "severity": in.Severity, "priority": in.Priority, "reproducibility": in.Reproducibility, "found_version": in.FoundVersion, "fixed_version": in.FixedVersion, "introduced_stage": in.IntroducedStage, "introduced_reason": in.IntroducedReason, "module": in.Module, "version": version + 1, "updated_at": now, "updated_by": actor})
	if e != nil {
		return empty, e
	}
	if result.RowsAffected != 1 {
		return empty, ErrWorkVersion
	}
	event := audit.NewDomainEvent(ctx, row.TenantID, "delivery.defect_details_updated", "work_item", row.ID)
	event.Detail["project_id"] = project.String()
	event.Detail["version"] = version + 1
	if e = m.service.audit(ctx, tx, event); e != nil {
		return empty, e
	}
	_, e = m.service.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: project, Type: "work_item.defect_details_updated", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "permission:work_item_write"}, Subject: events.Subject{Kind: "work_item", ID: row.ID}, SubjectVersion: int64(version + 1), Data: map[string]any{"version": version + 1}})
	return DefectDetailReceipt{WorkItemID: row.ID, Version: version + 1}, e
}

// RubricSubject is an exact, source-owned current result fence. Scores never
// mutate work, verification or acceptance states.
type RubricSubject struct {
	ProjectID, WorkItemID, ResultID, PackageID    guid.ID
	Version, ResultVersion                        int
	HeadCommit, Authority, ActorKind, PackageHash string
	EvidenceIDs                                   []guid.ID
}
type RubricClientRequest struct {
	Version              int
	ProjectID, PackageID guid.ID
	ItemID, Stage        string
	Write                bool
}
type RubricClientTarget struct {
	Version                    int
	WorkItemID, ResultID       guid.ID
	Hash, Authority, ActorKind string
	EvidenceIDs                []guid.ID
}
type RubricClientReader func(context.Context, *gorm.DB, RubricClientRequest) (RubricClientTarget, error)
type RubricClientConnector struct {
	Bind func(RubricClientReader) error
}
type RubricSubjects struct {
	WorkOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, string) (RubricSubject, error)
	PackageOn func(context.Context, *gorm.DB, RubricClientRequest) (RubricSubject, error)
}

func (m *Module) provideRubric(s *module.Services) error {
	var client RubricClientReader
	if err := module.Provide[RubricClientConnector](s, RubricClientConnector{Bind: func(f RubricClientReader) error {
		if f == nil || client != nil {
			return ErrInvalidArgument
		}
		client = f
		return nil
	}}); err != nil {
		return err
	}
	return module.Provide[RubricSubjects](s, RubricSubjects{WorkOn: m.rubricWorkOn, PackageOn: func(ctx context.Context, tx *gorm.DB, in RubricClientRequest) (RubricSubject, error) {
		if client == nil {
			return RubricSubject{}, ErrUnavailable
		}
		target, e := client(ctx, tx, in)
		if e != nil {
			return RubricSubject{}, e
		}
		tenant := authn.TenantIDFromContext(ctx)
		q := query.BindWorkItems(tx, m.service.names)
		work, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(in.ProjectID), q.ID.Eq(target.WorkItemID), q.ArchivedAt.IsNull()).First()
		if e != nil {
			return RubricSubject{}, e
		}
		result, e := m.resultOn(ctx, tx, tenant, in.ProjectID, work.ID, target.ResultID)
		if e != nil {
			return RubricSubject{}, e
		}
		// A published package cannot score a replacement result or an unaccepted one.
		if work.CurrentClaimID == nil || *work.CurrentClaimID != result.ID || work.State != "ACCEPTED" || work.AcceptedAt == nil || work.PausedAt != nil {
			return RubricSubject{}, ErrResultState
		}
		if in.Write && target.ActorKind == "HUMAN" {
			actor := authn.PrincipalIDFromContext(ctx)
			if result.ActorID == actor {
				return RubricSubject{}, ErrResultIndependent
			}
			if e = m.teamHumanOn(ctx, tx, tenant, actor); e != nil {
				return RubricSubject{}, e
			}
			target.Authority, e = m.resultAuthorityOn(ctx, tx, work, result, actor, "decision_approve")
			if e != nil {
				return RubricSubject{}, e
			}
		}
		return RubricSubject{PackageID: in.PackageID, ProjectID: in.ProjectID, WorkItemID: work.ID, ResultID: result.ID, Version: target.Version, ResultVersion: result.Sequence, HeadCommit: result.HeadCommit, Authority: target.Authority, ActorKind: target.ActorKind, PackageHash: target.Hash, EvidenceIDs: target.EvidenceIDs}, nil
	}})
}
func (m *Module) rubricWorkOn(ctx context.Context, tx *gorm.DB, project, workID, resultID guid.ID, expected int, stage string) (RubricSubject, error) {
	var out RubricSubject
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	if tenant <= 0 || actor <= 0 || project <= 0 || workID <= 0 || resultID < 0 || stage != "" && resultID == 0 || !slices.Contains([]string{"", "HUMAN_REVIEW", "AGENT_ACCEPTANCE"}, stage) {
		return out, ErrInvalidArgument
	}
	hint := query.BindWorkItems(tx, m.service.names)
	hintRow, e := hint.WithContext(ctx).Select(hint.AccountablePrincipalID).Where(hint.TenantID.Eq(tenant), hint.ProjectID.Eq(project), hint.ID.Eq(workID), hint.ArchivedAt.IsNull()).First()
	if e != nil {
		return out, e
	}
	owner := hintRow.AccountablePrincipalID
	_, work, _, e := m.lockedWorkActorOn(ctx, tx, tenant, project, workID, actor, owner, "work_item_read", stage == "AGENT_ACCEPTANCE" || stage == "")
	if e != nil {
		return out, e
	}
	if work.CurrentClaimID == nil {
		return out, ErrResultState
	}
	if resultID == 0 {
		resultID = *work.CurrentClaimID
	}
	result, e := m.resultOn(ctx, tx, tenant, project, workID, resultID)
	if e != nil {
		return out, e
	}
	if *work.CurrentClaimID != result.ID {
		return out, ErrResultState
	}
	out = RubricSubject{ProjectID: project, WorkItemID: work.ID, ResultID: result.ID, Version: work.Version, ResultVersion: result.Sequence, HeadCommit: result.HeadCommit, EvidenceIDs: []guid.ID{}}
	if stage != "" {
		if expected != work.Version {
			return out, ErrWorkVersion
		}
		if work.PausedAt != nil || work.IsContainer || !slices.Contains([]string{"INTERNAL_QA", "NEEDS_HUMAN_REVIEW"}, work.State) {
			return out, ErrResultState
		}
		if e = m.requireNoAndonOn(ctx, tx, work); e != nil {
			return out, e
		}
		if result.ActorID == actor {
			return out, ErrResultIndependent
		}
		if stage == "HUMAN_REVIEW" {
			if work.State != "NEEDS_HUMAN_REVIEW" {
				return out, ErrResultState
			}
			if e = m.teamHumanOn(ctx, tx, tenant, actor); e != nil {
				return out, e
			}
			out.Authority, e = m.resultAuthorityOn(ctx, tx, work, result, actor, "decision_approve")
			out.ActorKind = "HUMAN"
			if e != nil {
				return out, e
			}
		} else {
			if m.agents.ByPrincipalOn == nil || m.verificationExecutor.ReceiptOn == nil {
				return out, ErrUnavailable
			}
			a, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, actor)
			if e != nil {
				return out, e
			}
			if !found || a.State != "ACTIVE" || a.PrincipalID != actor {
				return out, ErrWorkForbidden
			}
			runs, e := m.verificationRunsOn(ctx, tx, result)
			if e != nil {
				return out, e
			}
			for _, run := range runs {
				if run.Stage != "AGENT_ACCEPTANCE" || run.ReviewerID == nil || *run.ReviewerID != actor || run.TaskID == nil || run.State != "DONE" {
					continue
				}
				r, e := m.verificationExecutor.ReceiptOn(ctx, tx, tenant, project, workID, result.ID, *run.TaskID)
				if e != nil {
					return out, e
				}
				if result.SourceExecutionID == nil || r.ExecutionID != *result.SourceExecutionID || r.Stage != run.Stage || r.ReviewerID != actor || r.ResultID != result.ID || r.ResultVersion != result.Sequence || r.HeadCommit != result.HeadCommit || r.ReceiptHash != run.ReceiptHash {
					return out, ErrResultCoverage
				}
				out.Authority = "verification-task:" + run.TaskID.String()
				out.ActorKind = "AGENT"
				break
			}
			if out.Authority == "" {
				return out, ErrWorkForbidden
			}
		}
	}
	evidence := query.BindResultEvidence(tx, m.service.names)
	rows, e := evidence.WithContext(ctx).Where(evidence.TenantID.Eq(tenant), evidence.ProjectID.Eq(project), evidence.ResultID.Eq(result.ID)).Order(evidence.ID).Find()
	if e != nil {
		return out, e
	}
	for _, r := range rows {
		out.EvidenceIDs = append(out.EvidenceIDs, r.ID)
	}
	return out, nil
}
