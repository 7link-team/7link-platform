package clientwork

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrDispatchExpired = errors.New("clientwork: dispatch proposal expired")

type DispatchBody struct {
	WorkItemID          guid.ID `json:"work_item_id"`
	ExpectedWorkVersion int     `json:"expected_work_version" minimum:"1"`
	Trigger             string  `json:"trigger" enum:"MANUAL_REQUEST,CHANNEL_REQUEST,REDISPATCH"`
}
type DispatchDecision struct {
	Decision            string   `json:"decision" enum:"CONFIRM,OVERRIDE,DISMISS,EXPIRE"`
	AssigneePrincipalID *guid.ID `json:"assignee_principal_id,omitempty"`
	Reason              string   `json:"reason" minLength:"1" maxLength:"2000"`
}
type DispatchComponents struct {
	SkillBP     int `json:"skill_bp"`
	FPABP       int `json:"fpa_bp"`
	CostBP      int `json:"cost_bp"`
	LoadBP      int `json:"load_bp"`
	DiversityBP int `json:"diversity_bp"`
}
type DispatchCandidate struct {
	Analytics       *DispatchAnalytics           `json:"analytics,omitempty"`
	Catalog         *agents.SkillCatalogSnapshot `json:"catalog,omitempty"`
	PendingCommands *int                         `json:"pending_commands"`
	QueueObservedAt *time.Time                   `json:"queue_observed_at"`
	fleet.DispatchAgentFact
	ActiveItems     int                `json:"active_items"`
	Applied24H      int                `json:"applied_24h"`
	LastAppliedAt   *time.Time         `json:"last_applied_at"`
	IneligibleCodes []string           `json:"ineligible_codes"`
	Components      DispatchComponents `json:"components"`
	Score           int                `json:"score"`
	// Unknown analytics remain explicit: a neutral ranking prior is not a claim
	// that zero decisions or zero cost were observed.
	HistoryAvailable    bool   `json:"history_available"`
	EstimatedCostMicros *int64 `json:"estimated_cost_micros"`
}
type DispatchSnapshot struct {
	AnalyticsAvailable bool                       `json:"analytics_available"`
	AnalyticsScope     string                     `json:"analytics_scope"`
	HistorySamples90D  *int64                     `json:"history_samples_90d"`
	Work               delivery.DispatchWorkFacts `json:"work"`
	ScoringVersion     string                     `json:"scoring_version"`
	Candidates         []DispatchCandidate        `json:"candidates"`
	GuardReasons       []string                   `json:"guard_reasons"`
	RequiresHuman      bool                       `json:"requires_human"`
	Limitations        []string                   `json:"limitations"`
}
type DispatchView struct {
	Decision      *DispatchActionView `json:"decision"`
	CanExpire     bool                `json:"can_expire"`
	ID            guid.ID             `json:"id"`
	WorkItemID    guid.ID             `json:"work_item_id"`
	WorkVersion   int                 `json:"work_version"`
	Trigger       string              `json:"trigger"`
	State         string              `json:"state"`
	Result        string              `json:"result"`
	RecommendedID *guid.ID            `json:"recommended_id"`
	AppliedID     *guid.ID            `json:"applied_id"`
	AssignmentID  *guid.ID            `json:"assignment_id"`
	InputsDigest  string              `json:"inputs_digest"`
	Snapshot      DispatchSnapshot    `json:"snapshot"`
	Reason        *string             `json:"reason"`
	ExpiresAt     time.Time           `json:"expires_at"`
	Version       int                 `json:"version"`
}

type DispatchActionView struct {
	ID              guid.ID   `json:"id"`
	ProposalID      guid.ID   `json:"proposal_id"`
	ProposalVersion int       `json:"proposal_version"`
	WorkVersion     int       `json:"work_version"`
	Action          string    `json:"action"`
	InputsDigest    string    `json:"inputs_digest"`
	SelectedID      *guid.ID  `json:"selected_id"`
	AssignmentID    *guid.ID  `json:"assignment_id"`
	ActorID         guid.ID   `json:"actor_id"`
	AuthoritySource string    `json:"authority_source"`
	Reason          string    `json:"reason"`
	CreatedAt       time.Time `json:"created_at"`
}

func (m *Module) dispatchActionOn(ctx context.Context, tx *gorm.DB, row *model.DispatchProposal) (*DispatchActionView, error) {
	q := query.BindDispatchAction(tx, m.names)
	a, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(row.ProjectID), q.ProposalID.Eq(row.ID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &DispatchActionView{ID: a.ID, ProposalID: a.ProposalID, ProposalVersion: a.ProposalVersion, WorkVersion: a.WorkVersion, Action: a.Action, InputsDigest: a.InputsDigest, SelectedID: a.SelectedID, AssignmentID: a.AssignmentID, ActorID: a.ActorID, AuthoritySource: a.AuthoritySource, Reason: a.Reason, CreatedAt: a.CreatedAt}, nil
}

// recordDispatchExpirationOn accompanies a successful TTL CAS in the same tx.
// ActorID zero and the system authority distinguish expiry from Human decisions.
func (m *Module) recordDispatchExpirationOn(ctx context.Context, tx *gorm.DB, row *model.DispatchProposal, at time.Time) error {
	id, err := m.nextID()
	if err != nil {
		return err
	}
	return query.BindDispatchAction(tx, m.names).WithContext(ctx).Create(&model.DispatchAction{TenantID: row.TenantID, ID: id, ProjectID: row.ProjectID, ProposalID: row.ID, ProposalVersion: row.Version, WorkItemID: row.WorkItemID, WorkVersion: row.WorkVersion, Action: "EXPIRE", InputsDigest: row.InputsDigest, AuthoritySource: "system:ttl_expiry", Reason: "TTL_ELAPSED", CreatedAt: at})
}

// DispatchCandidatesView is an immediate observation, never an assignment or
// a durable proposal. Creating/applying a proposal rechecks all current facts.
type DispatchCandidatesView struct {
	Snapshot     DispatchSnapshot `json:"snapshot"`
	InputsDigest string           `json:"inputs_digest"`
}

func encodeDispatchSnapshot(snapshot DispatchSnapshot) (string, string, error) {
	data, e := json.Marshal(snapshot)
	if e != nil {
		return "", "", e
	}
	digest := sha256.Sum256(data)
	return string(data), hex.EncodeToString(digest[:]), nil
}

func dispatchView(row *model.DispatchProposal) (DispatchView, error) {
	out := DispatchView{ID: row.ID, WorkItemID: row.WorkItemID, WorkVersion: row.WorkVersion, Trigger: row.Trigger, State: row.State, Result: row.Result, RecommendedID: row.RecommendedID, AppliedID: row.AppliedID, AssignmentID: row.AssignmentID, InputsDigest: row.InputsDigest, Reason: row.Reason, ExpiresAt: row.ExpiresAt, Version: row.Version}
	if out.State == "PROPOSED" && !now().Before(row.ExpiresAt) {
		out.State = "EXPIRED"
		out.CanExpire = true
	}
	e := json.Unmarshal([]byte(row.Snapshot), &out.Snapshot)
	return out, e
}
func dispatchBP(n, d int) int {
	if d <= 0 {
		return 0
	}
	return (10000*n + d/2) / d
}
func dispatchScore(c *DispatchCandidate, work delivery.DispatchWorkFacts) {
	tags := slices.Clone(work.RequiredTags)
	tags = append(tags, work.PreferredTags...)
	slices.Sort(tags)
	tags = slices.Compact(tags)
	matched := 0
	for _, tag := range tags {
		if slices.Contains(c.Tags, tag) {
			matched++
		}
	}
	skill := 10000
	if len(tags) > 0 {
		skill = dispatchBP(matched, len(tags))
	}
	load := 0
	if c.MaxParallel > 0 {
		queued := 0
		if c.PendingCommands != nil {
			queued = *c.PendingCommands
		}
		load = dispatchBP(max(0, c.MaxParallel-c.ActiveItems-queued), c.MaxParallel)
	}
	// No quality/cost projection currently exists for every execution type.
	// Persist this limitation and require a Human; do not claim measured values.
	c.Components = DispatchComponents{SkillBP: skill, FPABP: 5000, CostBP: 10000, LoadBP: load, DiversityBP: dispatchBP(max(0, 5-c.Applied24H), 5)}
	c.Score = 35*skill + 30*c.Components.FPABP + 15*c.Components.CostBP + 15*load + 5*c.Components.DiversityBP
}
func dispatchCandidateLess(a, b DispatchCandidate) bool {
	ae, be := len(a.IneligibleCodes) == 0, len(b.IneligibleCodes) == 0
	if ae != be {
		return ae
	}
	if !ae && len(a.IneligibleCodes) != len(b.IneligibleCodes) {
		return len(a.IneligibleCodes) < len(b.IneligibleCodes)
	}
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.Components.FPABP != b.Components.FPABP {
		return a.Components.FPABP > b.Components.FPABP
	}
	if a.ActiveItems != b.ActiveItems {
		return a.ActiveItems < b.ActiveItems
	}
	if a.LastAppliedAt == nil && b.LastAppliedAt != nil {
		return true
	}
	if a.LastAppliedAt != nil && b.LastAppliedAt == nil {
		return false
	}
	if a.LastAppliedAt != nil && b.LastAppliedAt != nil && !a.LastAppliedAt.Equal(*b.LastAppliedAt) {
		return a.LastAppliedAt.Before(*b.LastAppliedAt)
	}
	return a.ID < b.ID
}
func (m *Module) dispatchSnapshotOn(ctx context.Context, tx *gorm.DB, p, workID guid.ID) (DispatchSnapshot, error) {
	out := DispatchSnapshot{ScoringVersion: "s1-preview", Candidates: []DispatchCandidate{}, GuardReasons: []string{}, RequiresHuman: true, Limitations: []string{"QUALITY_COST_HISTORY_UNAVAILABLE", "SKILL_CATALOG_UNAVAILABLE", "MACHINE_QUEUE_UNAVAILABLE", "AUTO_POLICY_UNAVAILABLE"}}
	if m.dispatch.WorkOn == nil || m.dispatch.LoadsOn == nil || m.dispatchFleet.ListOn == nil {
		return out, ErrUnavailable
	}
	tenant := authn.TenantIDFromContext(ctx)
	workOn := m.dispatch.WorkOn
	if isDispatchAuto(ctx) {
		workOn = m.dispatch.AutoWorkOn
		if workOn == nil {
			return out, ErrUnavailable
		}
	}
	work, e := workOn(ctx, tx, tenant, p, workID)
	if e != nil {
		return out, e
	}
	out.Work = work
	agents, e := m.dispatchFleet.ListOn(ctx, tx, tenant)
	if e != nil {
		return out, e
	}
	queues := map[guid.ID]delivery.DispatchQueueFact{}
	if m.dispatch.QueuesOn != nil {
		machines := []guid.ID{}
		for _, agent := range agents {
			if agent.MachineID != nil {
				machines = append(machines, *agent.MachineID)
			}
		}
		facts, err := m.dispatch.QueuesOn(ctx, tx, tenant, machines)
		if err != nil && !errors.Is(err, delivery.ErrUnavailable) {
			return out, err
		}
		if err == nil {
			for _, fact := range facts {
				queues[fact.MachineID] = fact
			}
			out.Limitations = slices.DeleteFunc(out.Limitations, func(code string) bool { return code == "MACHINE_QUEUE_UNAVAILABLE" })
			out.Limitations = append(out.Limitations, "MACHINE_QUEUE_SCHEDULING_UNAVAILABLE")
		}
	}
	principals := make([]guid.ID, 0, len(agents))
	for _, a := range agents {
		if isDispatchAuto(ctx) {
			state, _ := ctx.Value(dispatchAutoKey{}).(*dispatchAutoState)
			if state == nil || !slices.Contains(state.principals, a.PrincipalID) {
				return out, ErrConflict
			}
		}
		principals = append(principals, a.PrincipalID)
	}
	loads, e := m.dispatch.LoadsOn(ctx, tx, tenant, p, principals)
	if e != nil {
		return out, e
	}
	byPrincipal := map[guid.ID]delivery.DispatchLoadFact{}
	for _, l := range loads {
		byPrincipal[l.PrincipalID] = l
	}
	q := query.BindDispatchProposal(tx, m.names)
	history, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(p), q.CreatedAt.Gte(now().Add(-24*time.Hour))).Order(q.ID).Limit(1001).Find()
	if e != nil {
		return out, e
	}
	if len(history) > 1000 {
		return out, ErrUnavailable
	}
	risk := map[string]int{"LOW": 1, "MEDIUM": 2, "HIGH": 3}
	for _, a := range agents {
		l, found := byPrincipal[a.PrincipalID]
		if !found {
			return out, ErrUnavailable
		}
		c := DispatchCandidate{DispatchAgentFact: a, ActiveItems: l.ActiveItems, IneligibleCodes: []string{}}
		if a.MachineID != nil {
			if fact, ok := queues[*a.MachineID]; ok {
				count, at := fact.PendingCommands, fact.ObservedAt
				c.PendingCommands, c.QueueObservedAt = &count, &at
			}
		}
		fail := func(code string) { c.IneligibleCodes = append(c.IneligibleCodes, code) }
		if !slices.Contains(a.Roles, "EXECUTOR") {
			fail("ROLE")
		}
		if a.State != "ACTIVE" || a.ProcessStatus != "RUNNING" {
			fail("STATE")
		}
		if !a.MachineOnline || a.MachineDraining || a.FreeSlots < 1 {
			fail("MACHINE")
		}
		if !a.ModelListed || (work.RequiredRuntime != nil && *work.RequiredRuntime != "" && *work.RequiredRuntime != a.Runtime) {
			fail("CAPABILITY")
		}
		for _, tag := range work.RequiredTags {
			if !slices.Contains(a.Tags, tag) {
				fail("SKILL_REQUIRED")
				break
			}
		}
		if risk[a.MaxRisk] == 0 || risk[work.Risk] == 0 || risk[work.Risk] > risk[a.MaxRisk] {
			fail("RISK")
		}
		if !l.Member {
			fail("MEMBER")
		}
		if a.MaxParallel <= 0 || l.ActiveItems >= a.MaxParallel {
			fail("CAPACITY")
		}
		if work.Policy == "AUTO" && work.LeadAgentID != nil && *work.LeadAgentID == a.ID {
			fail("NOT_LEAD_AUTO")
		}
		if work.BudgetState == "EXHAUSTED" || work.BudgetRemaining != nil {
			fail("BUDGET_COST_UNAVAILABLE")
		}
		if a.SpawnBudgetUnverified {
			fail("SPAWN_BUDGET")
		}
		for _, h := range history {
			if h.AppliedID != nil && *h.AppliedID == a.PrincipalID && h.DecidedAt != nil && h.DecidedAt.After(now().Add(-24*time.Hour)) {
				c.Applied24H++
				if c.LastAppliedAt == nil || c.LastAppliedAt.Before(*h.DecidedAt) {
					v := *h.DecidedAt
					c.LastAppliedAt = &v
				}
			}
			if h.WorkItemID == workID && h.State == "OVERRIDDEN" && h.RecommendedID != nil && *h.RecommendedID == a.PrincipalID && h.DecidedAt != nil && h.DecidedAt.After(now().Add(-24*time.Hour)) {
				if !slices.Contains(c.IneligibleCodes, "NOT_EXCLUDED") {
					fail("NOT_EXCLUDED")
				}
			}
		}
		dispatchScore(&c, work)
		out.Candidates = append(out.Candidates, c)
	}
	if err := m.enrichDispatchAnalyticsOn(ctx, tx, tenant, p, &out); err != nil {
		return out, err
	}
	slices.SortFunc(out.Candidates, func(a, b DispatchCandidate) int {
		if dispatchCandidateLess(a, b) {
			return -1
		}
		if dispatchCandidateLess(b, a) {
			return 1
		}
		return 0
	})
	if work.Risk == "HIGH" {
		out.GuardReasons = append(out.GuardReasons, "G1")
	}
	if work.Held || work.BudgetState == "EXHAUSTED" {
		out.GuardReasons = append(out.GuardReasons, "G2")
	}
	if work.RequireHumanDecision {
		out.GuardReasons = append(out.GuardReasons, "G6")
	}
	if work.LeadAgentID == nil {
		out.GuardReasons = append(out.GuardReasons, "G9")
	}
	if work.LeadAgentID != nil && len(out.Candidates) > 0 && out.Candidates[0].ID == *work.LeadAgentID && len(out.Candidates[0].IneligibleCodes) == 0 {
		out.GuardReasons = append(out.GuardReasons, "G7")
	}
	applied, overridden := 0, false
	for _, h := range history {
		if h.WorkItemID == workID && slices.Contains([]string{"APPLIED", "OVERRIDDEN"}, h.State) {
			applied++
			overridden = overridden || h.State == "OVERRIDDEN"
		}
	}
	if applied >= 2 {
		out.GuardReasons = append(out.GuardReasons, "G5")
	}
	if overridden {
		out.GuardReasons = append(out.GuardReasons, "G8")
	}
	if work.Policy != "AUTO" {
		out.GuardReasons = append(out.GuardReasons, "MANUAL_POLICY")
	}
	if isDispatchAuto(ctx) {
		for _, candidate := range out.Candidates {
			if len(candidate.IneligibleCodes) == 0 && candidate.PendingCommands == nil {
				out.GuardReasons = append(out.GuardReasons, "MACHINE_QUEUE_UNAVAILABLE")
				break
			}
		}
		out.Limitations = slices.DeleteFunc(out.Limitations, func(code string) bool { return code == "AUTO_POLICY_UNAVAILABLE" })
		if m.dispatch.HistorySamplesOn == nil {
			out.GuardReasons = append(out.GuardReasons, "QUALITY_HISTORY_UNAVAILABLE")
		} else {
			samples, err := m.dispatch.HistorySamplesOn(ctx, tx, tenant, work.Type, now().Add(-90*24*time.Hour))
			if err != nil {
				return out, err
			}
			out.HistorySamples90D = &samples
			if samples > 0 && !out.AnalyticsAvailable {
				out.GuardReasons = append(out.GuardReasons, "QUALITY_HISTORY_INCOMPLETE")
			} else if !out.AnalyticsAvailable {
				out.ScoringVersion = "s1-cold-start"
				out.Limitations = slices.DeleteFunc(out.Limitations, func(code string) bool { return code == "QUALITY_COST_HISTORY_UNAVAILABLE" })
				out.Limitations = append(out.Limitations, "QUALITY_COLD_START_PRIOR", "COST_UNMEASURED_EQUAL_SCORES")
			}
		}
		if work.Stage != "ACTIVE" || work.TeamMode == "HUMAN_ONLY" {
			out.GuardReasons = append(out.GuardReasons, "G2")
		}
		if work.CurrentExecutionID != nil || work.State != "READY" {
			out.GuardReasons = append(out.GuardReasons, "CURRENT_ASSIGNMENT_OR_EXECUTION")
		}
		q := query.BindDispatchAction(tx, m.names)
		count, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(p), q.Action.Eq("AUTO_APPLY"), q.CreatedAt.Gte(now().Add(-time.Hour))).Count()
		if err != nil {
			return out, err
		}
		if count >= 20 {
			out.GuardReasons = append(out.GuardReasons, "G4")
		}
		out.RequiresHuman = len(out.GuardReasons) > 0
	} else {
		out.GuardReasons = append(out.GuardReasons, "AUTO_POLICY_UNAVAILABLE")
	}
	return out, nil
}
func (m *Module) dispatchOn(ctx context.Context, tx *gorm.DB, p, id guid.ID) (*model.DispatchProposal, error) {
	q := query.BindDispatchProposal(tx, m.names)
	row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id)).First()
	if e != nil {
		return nil, e
	}
	workOn := m.dispatch.WorkOn
	if isDispatchAuto(ctx) {
		workOn = m.dispatch.AutoWorkOn
	}
	if workOn == nil {
		return nil, ErrUnavailable
	}
	if _, e = workOn(ctx, tx, row.TenantID, p, row.WorkItemID); e != nil {
		return nil, e
	}
	return row, nil
}
func (m *Module) createDispatchOn(ctx context.Context, tx *gorm.DB, p guid.ID, in DispatchBody) (Receipt, error) {
	if in.WorkItemID <= 0 || in.ExpectedWorkVersion < 1 || (!slices.Contains([]string{"MANUAL_REQUEST", "CHANNEL_REQUEST", "REDISPATCH"}, in.Trigger) && !(isDispatchAuto(ctx) && in.Trigger == "READY")) {
		return Receipt{}, ErrInvalid
	}
	if !isDispatchAuto(ctx) {
		if _, e := m.authorize(ctx, tx, p, "work_item_assign", false); e != nil {
			return Receipt{}, e
		}
	}
	snapshot, e := m.dispatchSnapshotOn(ctx, tx, p, in.WorkItemID)
	if e != nil {
		return Receipt{}, e
	}
	if snapshot.Work.WorkVersion != in.ExpectedWorkVersion {
		return Receipt{}, ErrConflict
	}
	q := query.BindDispatchProposal(tx, m.names)
	old, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ActiveSlot.Eq(in.WorkItemID)).First()
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		return Receipt{}, e
	}
	if old != nil {
		if now().Before(old.ExpiresAt) {
			return Receipt{}, ErrConflict
		}
		changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(old.TenantID), q.ID.Eq(old.ID), q.Version.Eq(old.Version), q.State.Eq("PROPOSED"), q.ActiveSlot.Eq(in.WorkItemID)).UpdateSimple(q.State.Value("EXPIRED"), q.ActiveSlot.Null(), q.Version.Add(1))
		if e != nil {
			return Receipt{}, e
		}
		if changed.RowsAffected != 1 {
			return Receipt{}, ErrConflict
		}
		if e = m.recordDispatchExpirationOn(ctx, tx, old, now()); e != nil {
			return Receipt{}, e
		}
		if e = m.expiryEventOn(ctx, tx, old.TenantID, p, old.ID, "dispatch_expired", old.Version+1, now()); e != nil {
			return Receipt{}, e
		}
	}
	if in.Trigger == "CHANNEL_REQUEST" {
		snapshot.GuardReasons = append(snapshot.GuardReasons, "CHANNEL_REQUEST")
	}
	content, hash, e := encodeDispatchSnapshot(snapshot)
	if e != nil {
		return Receipt{}, e
	}
	id, e := m.nextID()
	if e != nil {
		return Receipt{}, e
	}
	row := &model.DispatchProposal{TenantID: authn.TenantIDFromContext(ctx), ID: id, ProjectID: p, WorkItemID: in.WorkItemID, ActiveSlot: &in.WorkItemID, WorkVersion: in.ExpectedWorkVersion, Trigger: in.Trigger, State: "PROPOSED", Result: "NO_ELIGIBLE", Snapshot: content, InputsDigest: hash, ExpiresAt: now().Add(30 * time.Minute), CreatedAt: now(), Version: 1}
	for _, candidate := range snapshot.Candidates {
		if len(candidate.IneligibleCodes) == 0 {
			principal := candidate.PrincipalID
			row.RecommendedID = &principal
			row.Result = "RECOMMENDED"
			break
		}
	}
	if e = q.WithContext(ctx).Create(row); e != nil {
		return Receipt{}, e
	}
	if e = m.dispatchEventOn(ctx, tx, p, id, "dispatch_proposed", 1); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: 1, State: row.State}, nil
}
func (m *Module) decideDispatchOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, in DispatchDecision) (Receipt, error) {
	if !isDispatchAuto(ctx) {
		if _, e := m.authorize(ctx, tx, p, "work_item_assign", false); e != nil {
			return Receipt{}, e
		}
	}
	if !validText(in.Reason, 2000) {
		return Receipt{}, ErrInvalid
	}
	row, e := m.dispatchOn(ctx, tx, p, id)
	if e != nil {
		return Receipt{}, e
	}
	if row.Version != v || row.State != "PROPOSED" {
		return Receipt{}, ErrConflict
	}
	expired := !now().Before(row.ExpiresAt)
	if expired && in.Decision != "EXPIRE" {
		return Receipt{}, ErrDispatchExpired
	}
	authorityOn := m.dispatch.AuthorityOn
	if isDispatchAuto(ctx) {
		authorityOn = m.dispatch.AutoAuthorityOn
	}
	if authorityOn == nil {
		return Receipt{}, ErrUnavailable
	}
	selected := guid.ID(0)
	if (in.Decision == "CONFIRM" || in.Decision == "AUTO_APPLY") && row.RecommendedID != nil {
		selected = *row.RecommendedID
	}
	if in.Decision == "OVERRIDE" && in.AssigneePrincipalID != nil {
		selected = *in.AssigneePrincipalID
	}
	authority, e := authorityOn(ctx, tx, row.TenantID, p, row.WorkItemID, selected)
	if e != nil {
		return Receipt{}, e
	}
	if authority == "" {
		return Receipt{}, ErrForbidden
	}
	decisionID, e := m.nextID()
	if e != nil {
		return Receipt{}, e
	}
	event := "dispatch_decided"
	switch in.Decision {
	case "EXPIRE":
		if !expired {
			return Receipt{}, ErrConflict
		}
		row.State = "EXPIRED"
		event = "dispatch_expired"
	case "DISMISS":
		row.State = "DISMISSED"
	case "CONFIRM", "OVERRIDE", "AUTO_APPLY":
		if in.Decision == "AUTO_APPLY" && !isDispatchAuto(ctx) {
			return Receipt{}, ErrForbidden
		}
		if m.dispatch.ApplyOn == nil {
			return Receipt{}, ErrUnavailable
		}
		target := row.RecommendedID
		if in.Decision == "OVERRIDE" {
			target = in.AssigneePrincipalID
		}
		if target == nil || *target <= 0 {
			return Receipt{}, ErrInvalid
		}
		current, e := m.dispatchSnapshotOn(ctx, tx, p, row.WorkItemID)
		if e != nil {
			return Receipt{}, e
		}
		if current.Work.WorkVersion != row.WorkVersion {
			return Receipt{}, ErrConflict
		}
		if isDispatchAuto(ctx) && (current.RequiresHuman || row.Trigger != "READY" || current.Work.Policy != "AUTO") {
			return Receipt{}, ErrConflict
		}
		isAgent := false
		for _, candidate := range current.Candidates {
			if candidate.PrincipalID == *target {
				isAgent = true
				if len(candidate.IneligibleCodes) > 0 {
					return Receipt{}, ErrConflict
				}
			}
		}
		if (in.Decision == "CONFIRM" || in.Decision == "AUTO_APPLY") && !isAgent {
			return Receipt{}, ErrConflict
		}
		action := "ASSIGN"
		if row.Trigger == "REDISPATCH" {
			action = "REASSIGN"
		}
		canonical := &model.CommandDecision{TenantID: row.TenantID, ID: decisionID, ProjectID: p, TargetKind: "WORK_ITEM", TargetID: row.WorkItemID, Action: action, SourceID: id, SourceVersion: v, ActorID: authn.PrincipalIDFromContext(ctx), AuthoritySource: authority, Reason: in.Reason, CreatedAt: now()}
		if e = query.BindCommandDecision(tx, m.names).WithContext(ctx).Create(canonical); e != nil {
			return Receipt{}, e
		}
		assignment, e := m.dispatch.ApplyOn(ctx, tx, row.TenantID, p, delivery.DispatchAssignmentInput{Auto: isDispatchAuto(ctx), DecisionID: decisionID, WorkItemID: row.WorkItemID, ProposalID: id, AssigneePrincipalID: *target, ExpectedWorkVersion: row.WorkVersion, Reason: in.Reason, Reassign: row.Trigger == "REDISPATCH"})
		if e != nil {
			return Receipt{}, e
		}
		row.State = "APPLIED"
		if in.Decision == "OVERRIDE" {
			row.State = "OVERRIDDEN"
		}
		row.AppliedID = target
		row.AssignmentID = &assignment
	default:
		return Receipt{}, ErrInvalid
	}
	actor, at := authn.PrincipalIDFromContext(ctx), now()
	action := &model.DispatchAction{TenantID: row.TenantID, ID: decisionID, ProjectID: p, ProposalID: id, ProposalVersion: v, WorkItemID: row.WorkItemID, WorkVersion: row.WorkVersion, Action: in.Decision, InputsDigest: row.InputsDigest, SelectedID: row.AppliedID, AssignmentID: row.AssignmentID, ActorID: actor, AuthoritySource: authority, Reason: in.Reason, CreatedAt: at}
	if e = query.BindDispatchAction(tx, m.names).WithContext(ctx).Create(action); e != nil {
		return Receipt{}, e
	}
	row.DecidedBy = &actor
	row.DecidedAt = &at
	row.Reason = &in.Reason
	row.ActiveSlot = nil
	row.Version++
	q := query.BindDispatchProposal(tx, m.names)
	changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id), q.Version.Eq(v)).Select(q.State, q.ActiveSlot, q.AppliedID, q.AssignmentID, q.DecidedBy, q.DecidedAt, q.Reason, q.Version).Updates(row)
	if e != nil {
		return Receipt{}, e
	}
	if changed.RowsAffected != 1 {
		return Receipt{}, ErrConflict
	}
	if e = m.dispatchEventOn(ctx, tx, p, id, event, row.Version); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: row.Version, State: row.State}, nil
}
