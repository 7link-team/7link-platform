package execution

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
)

type MergeRequest struct {
	TargetBranch string `json:"target_branch,omitempty" maxLength:"200"`
	Push         bool   `json:"push"`
	Reason       string `json:"reason" minLength:"1" maxLength:"2000"`
}
type MergePlan struct {
	ID            guid.ID   `json:"id"`
	Nonce         string    `json:"nonce"`
	ExecutionID   guid.ID   `json:"execution_id"`
	SourceWorkdir string    `json:"source_workdir"`
	Workspace     string    `json:"workspace"`
	SourceCommit  string    `json:"source_commit"`
	TargetBranch  string    `json:"target_branch"`
	Push          bool      `json:"push"`
	DeadlineAt    time.Time `json:"deadline_at"`
}
type MergeObservation struct {
	Contained     bool      `json:"contained"`
	Signature     string    `json:"signature,omitempty" maxLength:"64"`
	PlanHash      string    `json:"plan_hash" minLength:"64" maxLength:"64"`
	State         string    `json:"state" enum:"MERGED,CONFLICT,FAILED"`
	SourceCommit  string    `json:"source_commit" minLength:"40" maxLength:"64"`
	MergeCommit   string    `json:"merge_commit,omitempty" maxLength:"64"`
	Pushed        bool      `json:"pushed"`
	ConflictFiles []string  `json:"conflict_files,omitempty" maxItems:"500"`
	Reason        string    `json:"reason" maxLength:"2000"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
}
type MergeView struct {
	ID              guid.ID           `json:"id"`
	ExecutionID     guid.ID           `json:"execution_id"`
	Attempt         int               `json:"attempt"`
	State           string            `json:"state"`
	TargetBranch    string            `json:"target_branch"`
	SourceCommit    string            `json:"source_commit"`
	Push            bool              `json:"push"`
	AuthoritySource string            `json:"authority_source"`
	Reason          string            `json:"reason"`
	Observation     *MergeObservation `json:"observation,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	DeadlineAt      time.Time         `json:"deadline_at"`
	FinishedAt      *time.Time        `json:"finished_at"`
}

func (m *Module) injectCodeMerge(s *module.Services) error {
	connector, err := module.Resolve[delivery.DefectMergeConnector](s)
	if err == nil {
		if err = connector.Bind(m.defectMergeOn); err != nil {
			return err
		}
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.mergeGuard, err = module.Resolve[delivery.CodeMergeGuard](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	return err
}
func mergeBranch(s string) bool {
	return bounded(s, 200) && !strings.HasPrefix(s, "-") && !strings.HasPrefix(s, "/") && !strings.HasSuffix(s, "/") && !strings.HasSuffix(s, ".") && !strings.Contains(s, "..") && !strings.ContainsAny(s, " ~^:?*[\\") && !strings.Contains(s, "@{")
}
func (m *Module) mergeTable(tx *gorm.DB) *gorm.DB { return tx.Table(m.names.Merges.String()) }
func mergeView(r *model.Merge) (MergeView, error) {
	p, err := decodeMerge(r)
	if err != nil {
		return MergeView{}, err
	}
	v := MergeView{ID: r.ID, ExecutionID: r.ExecutionID, Attempt: r.Attempt, State: r.State, TargetBranch: p.TargetBranch, SourceCommit: p.SourceCommit, Push: p.Push, AuthoritySource: r.AuthoritySource, Reason: r.Reason, CreatedAt: r.CreatedAt, DeadlineAt: r.DeadlineAt, FinishedAt: r.FinishedAt}
	if r.FinishedAt != nil {
		var o MergeObservation
		if hash([]byte(r.ObservationJSON)) != r.ReceiptHash || json.Unmarshal([]byte(r.ObservationJSON), &o) != nil {
			return v, ErrUnavailable
		}
		v.Observation = &o
	}
	return v, nil
}
func decodeMerge(r *model.Merge) (MergePlan, error) {
	var p MergePlan
	if hash([]byte(r.PlanJSON)) != r.PlanHash || json.Unmarshal([]byte(r.PlanJSON), &p) != nil || p.ID != r.ID || p.ExecutionID != r.ExecutionID || !commitHash(p.SourceCommit) || !mergeBranch(p.TargetBranch) {
		return p, ErrUnavailable
	}
	return p, nil
}

func (m *Module) authorizeMergeOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, additionalPrincipals ...guid.ID) (*model.Execution, delivery.CodeMergeFacts, error) {
	empty := delivery.CodeMergeFacts{}
	snapshot, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return nil, empty, err
	}
	v, err := view(snapshot)
	if err != nil {
		return nil, empty, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if m.mergeGuard == nil {
		return nil, empty, ErrCapabilityUnavailable
	}
	principals := append([]guid.ID{actor, snapshot.InitiatorID, v.Plan.ExecutorPrincipalID}, additionalPrincipals...)
	if err = m.workPrincipalLocks(ctx, tx, link(snapshot, actor), principals...); err != nil {
		return nil, empty, err
	}
	facts, err := m.mergeGuard(ctx, tx, tenant, snapshot.ProjectID, snapshot.WorkItemID, id, v.Plan.RepositoryID, v.Plan.ExecutorPrincipalID)
	if err != nil {
		if errors.Is(err, delivery.ErrWorkForbidden) || errors.Is(err, delivery.ErrInvalidMember) {
			return nil, empty, ErrForbidden
		}
		if errors.Is(err, delivery.ErrResultState) || errors.Is(err, delivery.ErrResultCoverage) || errors.Is(err, delivery.ErrVerificationPending) {
			return nil, empty, ErrConflict
		}
		return nil, empty, err
	}
	row, err := m.rowOn(ctx, tx, tenant, id, true)
	if err != nil {
		return nil, empty, err
	}
	if row.State != "SUCCEEDED" || row.PlanHash != snapshot.PlanHash || !commitHash(facts.HeadCommit) {
		return nil, empty, ErrConflict
	}
	return row, facts, nil
}
func (m *Module) RequestMergeOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in MergeRequest) (MergeView, error) {
	var empty MergeView
	if transaction(tx, tenant, id) != nil || !bounded(strings.TrimSpace(in.Reason), 2000) {
		return empty, ErrInvalidArgument
	}
	row, facts, err := m.authorizeMergeOn(ctx, tx, tenant, id)
	if err != nil {
		return empty, err
	}
	v, err := view(row)
	if err != nil {
		return empty, err
	}
	branch := in.TargetBranch
	if branch == "" {
		branch = facts.DefaultBranch
	}
	if !mergeBranch(branch) {
		return empty, ErrInvalidArgument
	}
	var prior []model.Merge
	if err = m.mergeTable(tx).WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND execution_id = ?", tenant, id).Order("attempt DESC").Limit(1).Find(&prior).Error; err != nil {
		return empty, err
	}
	attempt := 1
	if len(prior) > 0 {
		if prior[0].ActiveExecutionID != nil {
			return empty, ErrConflict
		}
		attempt = prior[0].Attempt + 1
	}
	if m.machines.ResolveWorkspaceOn == nil {
		return empty, ErrCapabilityUnavailable
	}
	workspace, err := m.machines.ResolveWorkspaceOn(ctx, tx, tenant, row.MachineID, row.ProjectID, v.Plan.RepositoryID)
	if err != nil {
		return empty, err
	}
	if v.Plan.AuthorizationMode == "READ_ONLY" {
		return empty, ErrConflict
	} // Runner resolves the retained worktree by the exact observed commit.
	evidence := query.BindEvidence(tx, m.names)
	count, err := evidence.WithContext(ctx).Where(evidence.TenantID.Eq(tenant), evidence.ExecutionID.Eq(id), evidence.Kind.Eq("COMMIT"), evidence.Content.Eq(facts.HeadCommit)).Count()
	if err != nil {
		return empty, err
	}
	if count != 1 {
		return empty, ErrConflict
	}
	next, err := m.nextID()
	if err != nil {
		return empty, err
	}
	nonce := make([]byte, 32)
	if _, err = rand.Read(nonce); err != nil {
		return empty, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	deadline := now.Add(10 * time.Minute)
	p := MergePlan{ID: next, Nonce: hex.EncodeToString(nonce), ExecutionID: id, SourceWorkdir: v.Plan.Workdir, Workspace: workspace.Path, SourceCommit: facts.HeadCommit, TargetBranch: branch, Push: in.Push, DeadlineAt: deadline}
	raw, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	r := model.Merge{ID: next, TenantID: tenant, ProjectID: row.ProjectID, ExecutionID: id, MachineID: row.MachineID, ActorID: authn.PrincipalIDFromContext(ctx), Attempt: attempt, ActiveExecutionID: &id, State: "PENDING", AuthoritySource: facts.AuthoritySource, ActorKind: facts.ActorKind, Reason: strings.TrimSpace(in.Reason), PlanJSON: string(raw), PlanHash: hash(raw), ObservationJSON: "", ReceiptHash: "", CreatedAt: now, DeadlineAt: deadline}
	if err = m.mergeTable(tx).Create(&r).Error; err != nil {
		return empty, err
	}
	if err = m.eventOn(ctx, tx, row, "execution.merge_requested", facts.ActorKind, facts.AuthoritySource, map[string]any{"merge_id": next.String(), "source_commit": p.SourceCommit, "target_branch": branch, "push": in.Push, "reason": r.Reason}); err != nil {
		return empty, err
	}
	return mergeView(&r)
}
func (m *Module) pullMergeOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, f RunnerFence) ([]MergePlan, error) {
	if transaction(tx, tenant, f.MachineID) != nil {
		return nil, ErrInvalidArgument
	}
	var candidate model.Merge
	err := m.mergeTable(tx).WithContext(ctx).Where("tenant_id = ? AND machine_id = ? AND state = ?", tenant, f.MachineID, "PENDING").Order("id").First(&candidate).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if e := m.validateRunner(ctx, tx, tenant, f); e != nil {
			return nil, e
		}
		return []MergePlan{}, nil
	}
	if err != nil {
		return nil, err
	}
	// Acquire the same principal/work/execution locks as requests before the
	// merge row. Include the Runner principal in the canonical sorted lock set.
	commandCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, EntityID: candidate.ProjectID, PrincipalID: candidate.ActorID})
	_, facts, authorityErr := m.authorizeMergeOn(commandCtx, tx, tenant, candidate.ExecutionID, authn.PrincipalIDFromContext(ctx))
	if err = m.validateRunner(ctx, tx, tenant, f); err != nil {
		return nil, err
	}
	var r model.Merge
	if err = m.mergeTable(tx).WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND id = ?", tenant, candidate.ID).First(&r).Error; err != nil {
		return nil, err
	}
	if r.State != "PENDING" {
		return []MergePlan{}, nil
	}
	p, err := decodeMerge(&r)
	if err != nil {
		return nil, err
	}
	if !r.DeadlineAt.After(time.Now()) {
		return nil, m.failMergeOn(ctx, tx, &r, "DEADLINE_EXCEEDED")
	}
	if authorityErr != nil {
		if !errors.Is(authorityErr, ErrForbidden) && !errors.Is(authorityErr, ErrConflict) && !errors.Is(authorityErr, ErrNotFound) {
			return nil, authorityErr
		}
		return nil, m.failMergeOn(ctx, tx, &r, "AUTHORITY_REVOKED")
	}
	if facts.HeadCommit != p.SourceCommit {
		return nil, m.failMergeOn(ctx, tx, &r, "CLAIM_CHANGED")
	}
	if m.machines.ReserveSlotOn == nil {
		return nil, ErrCapabilityUnavailable
	}
	if _, err = m.machines.ReserveSlotOn(ctx, tx, tenant, f.MachineID, r.ID); err != nil {
		return nil, err
	}
	if err = m.mergeTable(tx).Where("id = ? AND state = ?", r.ID, "PENDING").Updates(map[string]any{"state": "LEASED", "session_id": f.SessionID, "conn_epoch": f.ConnEpoch}).Error; err != nil {
		return nil, err
	}
	return []MergePlan{p}, nil
}
func (m *Module) failMergeOn(ctx context.Context, tx *gorm.DB, r *model.Merge, reason string) error {
	p, err := decodeMerge(r)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	return m.storeMergeOn(ctx, tx, r, MergeObservation{PlanHash: r.PlanHash, State: "FAILED", SourceCommit: p.SourceCommit, Reason: reason, StartedAt: now, EndedAt: now})
}
func (m *Module) storeMergeOn(ctx context.Context, tx *gorm.DB, r *model.Merge, o MergeObservation) error {
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	finished := time.Now().UTC().Truncate(time.Millisecond)
	var active any
	if o.State == "MERGED" {
		active = r.ExecutionID
	}
	if err = m.mergeTable(tx).Where("tenant_id = ? AND id = ?", r.TenantID, r.ID).Updates(map[string]any{"state": o.State, "active_execution_id": active, "observation_json": string(raw), "receipt_hash": hash(raw), "finished_at": finished}).Error; err != nil {
		return err
	}
	row, err := m.rowOn(ctx, tx, r.TenantID, r.ExecutionID, false)
	if err != nil {
		return err
	}
	kind := "execution.merge_finished"
	if o.State == "MERGED" {
		kind = "execution.merged"
		e := query.BindEvidence(tx, m.names)
		id, err := m.nextID()
		if err != nil {
			return err
		}
		if err = e.WithContext(ctx).Create(&model.Evidence{ID: id, TenantID: r.TenantID, ProjectID: r.ProjectID, ExecutionID: r.ExecutionID, Kind: "COMMIT", Title: "Merge commit", Content: o.MergeCommit, ContentHash: hash([]byte(o.MergeCommit)), State: "ATTACHED", ClaimID: &r.ExecutionID, ProducerKind: "MACHINE", ProducerID: authn.PrincipalIDFromContext(ctx), CreatedAt: finished}); err != nil {
			return err
		}
	}
	return m.eventOn(ctx, tx, row, kind, "SYSTEM", r.AuthoritySource, map[string]any{"merge_id": r.ID.String(), "state": o.State, "merge_commit": o.MergeCommit, "pushed": o.Pushed, "reason": r.Reason, "receipt_hash": hash(raw)})
}
func (m *Module) completeMergeOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, f RunnerFence, nonce string, o MergeObservation) error {
	if transaction(tx, tenant, id) != nil {
		return ErrInvalidArgument
	}
	if err := m.validateRunner(ctx, tx, tenant, f); err != nil {
		return err
	}
	var r model.Merge
	if err := m.mergeTable(tx).WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND id = ? AND machine_id = ?", tenant, id, f.MachineID).First(&r).Error; err != nil {
		return err
	}
	p, err := decodeMerge(&r)
	if err != nil {
		return err
	}
	if p.Nonce != nonce || r.SessionID != f.SessionID || r.ConnEpoch != f.ConnEpoch || r.PlanHash != o.PlanHash || o.SourceCommit != p.SourceCommit {
		return ErrFenced
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	if r.FinishedAt != nil {
		if hash(raw) != r.ReceiptHash {
			return ErrConflict
		}
		return nil
	}
	if r.State != "LEASED" || !r.DeadlineAt.After(time.Now()) || o.StartedAt.Before(r.CreatedAt) || o.EndedAt.Before(o.StartedAt) || o.EndedAt.After(time.Now().Add(time.Minute)) || len(o.ConflictFiles) > 500 || len(o.Reason) > 2000 {
		return ErrFenced
	}
	if o.State != "MERGED" && o.State != "CONFLICT" && o.State != "FAILED" {
		return ErrInvalidArgument
	}
	if o.Contained && !validMergeSignature(p, o) {
		return ErrFenced
	}
	if o.State == "MERGED" {
		if !commitHash(o.MergeCommit) || o.MergeCommit == o.SourceCommit || (o.Pushed && !p.Push) || len(o.ConflictFiles) > 0 {
			return ErrInvalidArgument
		}
	} else if o.MergeCommit != "" || o.Pushed {
		return ErrInvalidArgument
	}
	if m.machines.ReleaseSlotOn == nil {
		return ErrCapabilityUnavailable
	}
	if err = m.machines.ReleaseSlotOn(ctx, tx, tenant, f.MachineID, r.ID); err != nil {
		return err
	}
	return m.storeMergeOn(ctx, tx, &r, o)
}

func (m *Module) expireMerges(ctx context.Context, db *gorm.DB, tenant guid.ID, now time.Time) error {
	for {
		var rows []model.Merge
		if err := m.mergeTable(db).WithContext(ctx).Where("tenant_id = ? AND state IN ? AND deadline_at <= ?", tenant, []string{"PENDING", "LEASED"}, now).Order("id").Limit(50).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, candidate := range rows {
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := m.lockMachineOn(ctx, tx, tenant, candidate.MachineID); err != nil {
					return err
				}
				var r model.Merge
				if err := m.mergeTable(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND id = ?", tenant, candidate.ID).First(&r).Error; err != nil {
					return err
				}
				if r.FinishedAt != nil || r.DeadlineAt.After(now) {
					return nil
				}
				if r.State == "LEASED" {
					if m.machines.ReleaseSlotOn == nil {
						return ErrCapabilityUnavailable
					}
					if err := m.machines.ReleaseSlotOn(ctx, tx, tenant, r.MachineID, r.ID); err != nil {
						return err
					}
				}
				return m.failMergeOn(ctx, tx, &r, "DEADLINE_EXCEEDED_OUTCOME_UNKNOWN")
			}); err != nil {
				return err
			}
		}
	}
}

// The per-command key is delivered only to the authenticated, session-fenced
// machine. Its MAC binds the exact Git observation to that machine command.
func validMergeSignature(p MergePlan, o MergeObservation) bool {
	signature, err := hex.DecodeString(o.Signature)
	if err != nil || len(signature) != sha256.Size {
		return false
	}
	o.Signature = ""
	raw, err := json.Marshal(o)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(p.Nonce))
	_, _ = mac.Write(raw)
	return hmac.Equal(signature, mac.Sum(nil))
}
