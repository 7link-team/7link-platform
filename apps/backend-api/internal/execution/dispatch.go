package execution

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

type RunnerFence struct {
	MachineID guid.ID `json:"machine_id"`
	SessionID guid.ID `json:"session_id"`
	ConnEpoch int64   `json:"conn_epoch" minimum:"1"`
}
type ExecutionUsage struct {
	InputTokens  int64 `json:"input_tokens" minimum:"0"`
	OutputTokens int64 `json:"output_tokens" minimum:"0"`
}
type ReceiptInput struct {
	Progress      *ProgressObservation `json:"progress,omitempty"`
	SeenSeq       *int64               `json:"seen_seq,omitempty" minimum:"0"`
	Upload        *ArtifactUpload      `json:"upload,omitempty"`
	prepared      *files.PreparedExecutionArtifact
	Usage         *ExecutionUsage `json:"usage,omitempty" doc:"Actual observed token counts; omitted when runtime cannot report usage."`
	DispatchEpoch int64           `json:"dispatch_epoch" minimum:"1"`
	Seq           int64           `json:"seq" minimum:"1"`
	Kind          string          `json:"kind" enum:"ACCEPTED,HEARTBEAT,LOG,EVIDENCE,COMPLETED,FAILED,LOST,CANCELLED,PAUSED,RESUMED,PROGRESS"` //nolint:misspell // Protocol spelling.
	Content       string          `json:"content" maxLength:"65536"`
	HeadCommit    string          `json:"head_commit" maxLength:"64"`
	EvidenceKind  string          `json:"evidence_kind" enum:",COMMIT,TEST_REPORT,LOG,OBSERVATION,FILE"`
	EvidenceTitle string          `json:"evidence_title" maxLength:"200"`
}
type ReceiptView struct {
	Seq             int64  `json:"seq"`
	State           string `json:"state"`
	Replayed        bool   `json:"replayed"`
	CancelRequested bool   `json:"cancel_requested"`
	PauseRequested  bool   `json:"pause_requested"`
	ResumeRequested bool   `json:"resume_requested"`
}

func (m *Module) validateRunner(ctx context.Context, tx *gorm.DB, tenant guid.ID, f RunnerFence) error {
	if f.MachineID <= 0 || f.SessionID <= 0 || f.ConnEpoch <= 0 {
		return ErrInvalidArgument
	}
	if m.machines.ValidateSessionOn == nil {
		return ErrUnavailable
	}
	return m.machines.ValidateSessionOn(ctx, tx, tenant, f.MachineID, f.SessionID, f.ConnEpoch)
}

// PullOn leases at most one command. Lease expiry never causes automatic
// redispatch: a watchdog marks unknown results INTERRUPTED for Human recovery.
func (m *Module) PullOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, f RunnerFence) ([]ExecutionView, error) {
	out := []ExecutionView{}
	if err := transaction(tx, tenant, f.MachineID); err != nil {
		return nil, err
	}
	if rows, handled, err := m.pullPlanningOn(ctx, tx, tenant, f); handled || err != nil {
		return rows, err
	}
	if rows, handled, err := m.pullHandoverOn(ctx, tx, tenant, f); handled || err != nil {
		return rows, err
	}
	q := query.BindExecutions(tx, m.names)
	candidates, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(f.MachineID), q.BranchReason.Neq("HANDOVER"), q.BranchReason.Neq("PLANNING"), q.BranchReason.Neq("PLAN_REVIEW"), q.State.In("CREATED", "RUNNING"), q.CancelRequestedAt.IsNotNull(), q.LeaseExpiresAt.Gt(time.Now().UTC())).Order(q.ID).Limit(1).Find()
	if err == nil && len(candidates) == 0 {
		candidates, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(f.MachineID), q.BranchReason.Neq("HANDOVER"), q.BranchReason.Neq("PLANNING"), q.BranchReason.Neq("PLAN_REVIEW"), q.State.Eq("CREATED"), q.StateReason.Neq("FULL_AUTO_APPROVAL_REQUIRED"), q.LeaseExpiresAt.IsNull()).Order(q.ID).Limit(1).Find()
	}
	if err != nil {
		return nil, err
	}
	if len(candidates) > 0 {
		principalIDs := []guid.ID{authn.PrincipalIDFromContext(ctx), candidates[0].InitiatorID}
		candidateView, e := view(candidates[0])
		if e != nil {
			return nil, e
		}
		if candidateView.Plan.AuthorizationMode == "FULL_AUTO" && candidates[0].CancelRequestedAt == nil {
			grant, e := m.recordedFullAutoGrantOn(ctx, tx, candidates[0])
			if e != nil {
				return nil, e
			}
			principalIDs = append(principalIDs, grant.ActorID)
		}
		if err = m.workPrincipalLocks(ctx, tx, link(candidates[0], authn.PrincipalIDFromContext(ctx)), principalIDs...); err != nil {
			return nil, err
		}
		if m.work.AuthorizeOn == nil {
			return nil, ErrCapabilityUnavailable
		}
		if err = m.work.AuthorizeOn(ctx, tx, link(candidates[0], authn.PrincipalIDFromContext(ctx)), "DISPATCH"); err != nil {
			return nil, err
		}
	}
	if err = m.validateRunner(ctx, tx, tenant, f); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return out, nil
	}
	row, err := m.rowOn(ctx, tx, tenant, candidates[0].ID, true)
	if err != nil {
		return nil, err
	}
	if active(row.State) && row.CancelRequestedAt != nil && row.LeaseExpiresAt != nil && row.LeaseExpiresAt.After(time.Now().UTC()) {
		v, e := view(row)
		if e != nil {
			return nil, e
		}
		return append(out, v), nil
	}
	if row.State != "CREATED" || row.LeaseExpiresAt != nil || row.CancelRequestedAt != nil {
		return out, nil
	}
	v, err := view(row)
	if err != nil {
		return nil, err
	}
	if v.Plan.AuthorizationMode == "ON_REQUEST" && !validPlanApprovalProtocol(v.Plan) {
		return nil, ErrApprovalUnavailable
	}
	if v.Plan.AuthorizationMode == "FULL_AUTO" {
		grant, e := m.authorizeFullAutoDispatchOn(ctx, tx, row)
		if e != nil {
			return nil, e
		}
		v.FullAutoGrantID = &grant.ID
	}
	if err = m.validateAgentBindingOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)), row.MachineID, v.Plan); err != nil {
		return nil, err
	}
	expiry := time.Now().UTC().Truncate(time.Millisecond).Add(30 * time.Second)
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID)).Updates(map[string]any{"lease_expires_at": expiry, "updated_at": time.Now().UTC().Truncate(time.Millisecond)}); err != nil {
		return nil, err
	}
	row.Version++
	row.LeaseExpiresAt = &expiry
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID)).Update(q.Version, row.Version); err != nil {
		return nil, err
	}
	if err = m.eventOn(ctx, tx, row, "execution.state_changed", "MACHINE", "IAM", map[string]any{"state": "CREATED", "dispatch_epoch": row.DispatchEpoch, "leased": true}); err != nil {
		return nil, err
	}
	if err = m.recordKnowledgeOn(ctx, tx, row, row.ID, "INCLUSION", ""); err != nil {
		return nil, err
	}
	v.Version = row.Version
	v.LeaseExpiresAt = &expiry
	return append(out, v), nil
}

func (m *Module) ReceiptOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, f RunnerFence, in ReceiptInput) (ReceiptView, error) {
	var empty ReceiptView
	if in.DispatchEpoch <= 0 || in.Seq <= 0 || !slices.Contains([]string{"ACCEPTED", "HEARTBEAT", "LOG", "EVIDENCE", "COMPLETED", "FAILED", "LOST", "PAUSED", "RESUMED", "PROGRESS", StateCanceled}, in.Kind) || !utf8.ValidString(in.Content) || len(in.Content) > 65536 {
		return empty, ErrInvalidArgument
	}
	snapshot, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return empty, err
	}
	if snapshot.MachineID != f.MachineID {
		return empty, ErrForbidden
	}
	if snapshot.BranchReason == "PLANNING" || snapshot.BranchReason == "PLAN_REVIEW" {
		return m.receiptPlanningOn(ctx, tx, snapshot, f, in)
	}
	if snapshot.BranchReason == "HANDOVER" {
		return m.receiptHandoverOn(ctx, tx, snapshot, f, in)
	}
	receipts := query.BindReceipts(tx, m.names)
	prior, priorErr := receipts.WithContext(ctx).Where(receipts.TenantID.Eq(tenant), receipts.ExecutionID.Eq(id), receipts.DispatchEpoch.Eq(in.DispatchEpoch), receipts.Seq.Eq(in.Seq)).First()
	if priorErr != nil && !errors.Is(priorErr, gorm.ErrRecordNotFound) {
		return empty, priorErr
	}
	if err = m.workPrincipalLocks(ctx, tx, link(snapshot, authn.PrincipalIDFromContext(ctx)), authn.PrincipalIDFromContext(ctx), snapshot.InitiatorID); err != nil {
		return empty, err
	}
	if prior == nil {
		if m.work.AuthorizeOn == nil {
			return empty, ErrCapabilityUnavailable
		}
		if err = m.work.AuthorizeOn(ctx, tx, link(snapshot, authn.PrincipalIDFromContext(ctx)), receiptOperation(in.Kind)); err != nil {
			return empty, err
		}
	}
	if err = m.validateRunner(ctx, tx, tenant, f); err != nil {
		return empty, err
	}
	// The work lock precedes the execution lock. Replay skips the cursor
	// guard, while current runner/assignment authorization above remains required.
	if prior == nil && in.Kind == "EVIDENCE" {
		execution, e := view(snapshot)
		if e != nil {
			return empty, e
		}
		contextActor := execution.Plan.ExecutorPrincipalID
		contextCtx := ctx
		if contextActor > 0 {
			contextCtx = authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: contextActor, SubjectType: authn.SubjectTypePrincipal})
		}
		if e = m.checkExecutionContextOn(contextCtx, tx, snapshot, in.SeenSeq, false); e != nil {
			return empty, e
		}
	}
	row, err := m.rowOn(ctx, tx, tenant, id, true)
	if err != nil {
		return empty, err
	}
	logical := in
	logical.SeenSeq = nil
	encoded, err := json.Marshal(logical)
	if err != nil {
		return empty, err
	}
	digest := hash(encoded)
	if row.DispatchEpoch != in.DispatchEpoch {
		return empty, ErrFenced
	}
	// Re-read after the execution lock to serialize concurrent duplicate receipts.
	prior, err = receipts.WithContext(ctx).Where(receipts.TenantID.Eq(tenant), receipts.ExecutionID.Eq(id), receipts.DispatchEpoch.Eq(in.DispatchEpoch), receipts.Seq.Eq(in.Seq)).First()
	if err == nil {
		if prior.ContentHash != digest {
			return empty, ErrConflict
		}
		return ReceiptView{Seq: in.Seq, State: prior.State, Replayed: true, CancelRequested: row.CancelRequestedAt != nil, PauseRequested: row.StateReason == "PAUSE_REQUESTED", ResumeRequested: row.StateReason == "RESUME_REQUESTED"}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(now) || row.LastSeq == math.MaxInt64 || in.Seq != row.LastSeq+1 {
		return empty, ErrFenced
	}
	executionView, viewErr := view(row)
	if viewErr != nil {
		err = viewErr
		return empty, err
	}
	actualCost, costErr := actualExecutionCost(executionView.Plan, in.Usage)
	// Recovery reuses this execution's reservation. Per-attempt usage cannot
	// settle earlier unknown consumption from previous dispatch epochs.
	if row.DispatchEpoch > 1 {
		actualCost = nil
	}
	if costErr != nil {
		return empty, costErr
	}
	next, reason, result := row.State, row.StateReason, row.ResultState
	detail := map[string]any{"receipt_kind": in.Kind, "seq": in.Seq}
	switch in.Kind {
	case "ACCEPTED":
		if row.State != "CREATED" || row.CancelRequestedAt != nil {
			return empty, ErrConflict
		}
		next = "RUNNING"
		if m.work.StartedOn == nil {
			return empty, ErrCapabilityUnavailable
		}
		if err = m.work.StartedOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx))); err != nil {
			return empty, err
		}
	case "PAUSED":
		if row.State != "RUNNING" || (row.StateReason != "PAUSE_REQUESTED" && row.CancelRequestedAt == nil) {
			return empty, ErrConflict
		}
		// Native control may finish after cancellation commits. Record the
		// actual process state without discarding the cancellation intent.
		next = "PAUSED"
		if row.CancelRequestedAt == nil {
			reason = "HOLD_ACKNOWLEDGED"
		}
		detail["runner_acknowledged"] = true
	case "RESUMED":
		if row.State != "PAUSED" || (row.StateReason != "RESUME_REQUESTED" && row.StateReason != "HOLD_ACKNOWLEDGED" && row.CancelRequestedAt == nil) {
			return empty, ErrConflict
		}
		next = "RUNNING"
		if row.CancelRequestedAt == nil {
			reason = "HOLD_RELEASE_ACKNOWLEDGED"
			if row.StateReason == "HOLD_ACKNOWLEDGED" {
				// A newer hold can overtake an already-issued resume. Accept
				// the immutable fact, then immediately request another pause.
				reason = "PAUSE_REQUESTED"
			}
		}
		detail["runner_acknowledged"] = true
	case "PROGRESS":
		if row.State != "RUNNING" || row.CancelRequestedAt != nil {
			return empty, ErrConflict
		}
		progress, e := m.progressReceiptOn(ctx, tx, row, executionView.Plan, in.Progress)
		if e != nil {
			return empty, e
		}
		detail["no_progress_count"], detail["dependency_miss_count"] = progress.NoProgressCount, progress.DependencyMissCount
		encodedProgress, e := json.Marshal(progress)
		if e != nil {
			return empty, e
		}
		storedProgress := string(encodedProgress)
		row.ProgressJSON = &storedProgress
		if progress.StopReason != "" {
			if m.work.ProgressStopOn == nil {
				return empty, ErrCapabilityUnavailable
			}
			if e = m.work.ProgressStopOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)), progress.StopReason); e != nil {
				return empty, e
			}
			reason = "PAUSE_REQUESTED"
			detail["progress_stop_reason"] = progress.StopReason
		}
	case "HEARTBEAT":
		if row.State != "RUNNING" && row.State != "PAUSED" {
			return empty, ErrConflict
		}
	case "LOG":
		if row.State != "RUNNING" || in.Content == "" {
			return empty, ErrConflict
		}
		logID, e := m.nextID()
		if e != nil {
			return empty, e
		}
		if err = query.BindLogs(tx, m.names).WithContext(ctx).Create(&model.Log{ID: logID, TenantID: tenant, ExecutionID: id, DispatchEpoch: in.DispatchEpoch, Seq: in.Seq, Content: in.Content, ContentHash: hash([]byte(in.Content)), CreatedAt: now}); err != nil {
			return empty, err
		}
		if m.artifacts.attach == nil || in.prepared == nil {
			return empty, ErrCapabilityUnavailable
		}
		if _, err = m.artifacts.attach(ctx, tx, in.prepared, logID); err != nil {
			return empty, err
		}
	case "EVIDENCE":
		if row.State != "RUNNING" || (in.Content == "" && in.Upload == nil) || !slices.Contains([]string{"COMMIT", "TEST_REPORT", "LOG", "OBSERVATION", "FILE"}, in.EvidenceKind) || !bounded(in.EvidenceTitle, 200) {
			return empty, ErrInvalidArgument
		}
		if in.EvidenceKind == "COMMIT" && !commitHash(strings.TrimSpace(in.Content)) {
			return empty, ErrInvalidArgument
		}
		evidence := query.BindEvidence(tx, m.names)
		if in.EvidenceKind == "FILE" && in.Upload == nil {
			return empty, ErrInvalidArgument
		}
		if err = validateArtifactReceipt(executionView.Plan, in); err != nil {
			return empty, err
		}
		if m.artifacts.attach == nil || in.prepared == nil {
			return empty, ErrCapabilityUnavailable
		}
		contentHash := hash([]byte(in.Content))
		if in.EvidenceKind == "FILE" {
			contentHash = hash(in.Upload.Data)
		}
		existing, e := evidence.WithContext(ctx).Where(evidence.TenantID.Eq(tenant), evidence.ExecutionID.Eq(id), evidence.ContentHash.Eq(contentHash)).First()
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return empty, e
		}
		if e == nil && (existing.Kind != in.EvidenceKind || existing.Title != in.EvidenceTitle) {
			return empty, ErrConflict
		}
		if e == nil && in.EvidenceKind == "FILE" {
			var prior ArtifactFact
			if json.Unmarshal([]byte(existing.Content), &prior) != nil || prior.Coverage != in.Upload.Coverage || prior.Note != in.Upload.Note || (prior.CriterionID == nil) != (in.Upload.CriterionID == nil) || (prior.CriterionID != nil && *prior.CriterionID != *in.Upload.CriterionID) {
				return empty, ErrConflict
			}
		}
		if errors.Is(e, gorm.ErrRecordNotFound) {
			evidenceID, e := m.nextID()
			if e != nil {
				return empty, e
			}
			if e = evidence.WithContext(ctx).Create(&model.Evidence{ID: evidenceID, TenantID: tenant, ProjectID: row.ProjectID, ExecutionID: id, Kind: in.EvidenceKind, ProducerKind: "MACHINE", ProducerID: authn.PrincipalIDFromContext(ctx), Title: in.EvidenceTitle, Content: in.Content, ContentHash: contentHash, CreatedAt: now}); e != nil {
				return empty, e
			}
			if e = m.recordExecutionActivityOn(ctx, tx, row, "EVIDENCE_ADDED", "evidence", evidenceID, "MACHINE"); e != nil {
				return empty, e
			}
			pin, attachErr := m.artifacts.attach(ctx, tx, in.prepared, evidenceID)
			if attachErr != nil {
				return empty, attachErr
			}
			if in.EvidenceKind == "FILE" {
				artifact := ArtifactFact{BindingID: pin.BindingID, SpaceID: pin.SpaceID, FileID: pin.FileID, VersionID: pin.VersionID, FileVersion: 1, CriterionID: in.Upload.CriterionID, Coverage: in.Upload.Coverage, Note: in.Upload.Note, SHA256: pin.SHA256}
				manifest, marshalErr := json.Marshal(artifact)
				if marshalErr != nil {
					return empty, marshalErr
				}
				if _, e = evidence.WithContext(ctx).Where(evidence.ID.Eq(evidenceID)).Update(evidence.Content, string(manifest)); e != nil {
					return empty, e
				}
			}
		}
	case "COMPLETED":
		if row.State != "RUNNING" || row.CancelRequestedAt != nil || !commitHash(in.HeadCommit) {
			return empty, ErrConflict
		}
		evidence := query.BindEvidence(tx, m.names)
		records, e := evidence.WithContext(ctx).Where(evidence.TenantID.Eq(tenant), evidence.ExecutionID.Eq(id)).Order(evidence.ID).Find()
		if e != nil {
			return empty, e
		}
		facts := []EvidenceFact{}
		headFound := false
		for _, r := range records {
			if r.State == "WITHDRAWN" {
				continue
			}
			if r.Kind == "COMMIT" && strings.TrimSpace(r.Content) == in.HeadCommit {
				headFound = true
			}
			fact := EvidenceFact{ID: r.ID, Kind: r.Kind, Title: r.Title, ContentHash: r.ContentHash}
			if r.Kind == "FILE" {
				var artifact ArtifactFact
				if json.Unmarshal([]byte(r.Content), &artifact) != nil {
					return empty, ErrUnavailable
				}
				fact.Artifact = &artifact
			}
			facts = append(facts, fact)
		}
		if !headFound {
			return empty, ErrConflict
		}
		if err = validateArtifactCompletion(executionView.Plan, facts); err != nil {
			return empty, err
		}
		if m.work.FinishedOn == nil {
			return empty, ErrCapabilityUnavailable
		}
		completionReason := "CLAIM_SUBMITTED"
		if row.BranchReason == "SKILL_DISTILLATION" {
			completionReason = "SKILL_DISTILLATION_COMPLETED"
		}
		if row.BranchReason == "ENSEMBLE" {
			completionReason = "ENSEMBLE_CANDIDATE_READY"
		}
		complete := func(scoped context.Context) error {
			if row.BranchReason == "ENSEMBLE" {
				if err := m.saveEnsembleCandidateOn(scoped, tx, row, in.HeadCommit, facts); err != nil {
					return err
				}
			}
			if row.BranchReason != "ENSEMBLE" && row.BranchReason != "SKILL_DISTILLATION" {
				ids := make([]guid.ID, 0, len(facts))
				for _, fact := range facts {
					ids = append(ids, fact.ID)
				}
				if err := m.attachEvidenceOn(scoped, tx, row, row.ID, ids); err != nil {
					return err
				}
			}
			return m.work.FinishedOn(scoped, tx, link(row, authn.PrincipalIDFromContext(ctx)), WorkResult{State: "SUCCEEDED", Reason: completionReason, HeadCommit: in.HeadCommit, Evidence: facts, ActualMicros: actualCost})
		}
		if m.artifacts.references == nil {
			return empty, ErrCapabilityUnavailable
		}
		if err = m.artifacts.references(ctx, tx, artifactAuthority(tenant, row.ProjectID, id, f, in), complete); err != nil {
			return empty, err
		}
		next, reason, result = "SUCCEEDED", completionReason, "CONFIRMED"
	case "FAILED":
		if !active(row.State) {
			return empty, ErrConflict
		}
		next, reason, result = "FAILED", "RUNTIME_ERROR", "CONFIRMED"
		if m.work.FinishedOn == nil {
			return empty, ErrCapabilityUnavailable
		}
		if err = m.work.FinishedOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)), WorkResult{State: next, Reason: reason, ActualMicros: actualCost}); err != nil {
			return empty, err
		}
	case "LOST":
		if !active(row.State) {
			return empty, ErrConflict
		}
		next, reason, result = "INTERRUPTED", "HEARTBEAT_LOST", "UNKNOWN"
	case StateCanceled:
		if row.CancelRequestedAt == nil || !active(row.State) {
			return empty, ErrConflict
		}
		next, reason, result = StateCanceled, "USER_CANCEL", "CONFIRMED"
		if m.work.FinishedOn == nil {
			return empty, ErrCapabilityUnavailable
		}
		if err = m.work.FinishedOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)), WorkResult{State: next, Reason: reason, ActualMicros: actualCost}); err != nil {
			return empty, err
		}
	}
	if in.Kind == "COMPLETED" {
		stop, e := m.spinningClaimOn(ctx, tx, row, in.HeadCommit)
		if e != nil {
			return empty, e
		}
		if stop != "" {
			if m.work.ProgressStopOn == nil {
				return empty, ErrCapabilityUnavailable
			}
			if e = m.work.ProgressStopOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)), stop); e != nil {
				return empty, e
			}
			detail["progress_stop_reason"] = stop
		}
	}
	terminal := next == "SUCCEEDED" || next == "FAILED" || next == StateCanceled || next == "INTERRUPTED"
	if terminal {
		if m.machines.ReleaseSlotOn == nil {
			return empty, ErrUnavailable
		}
		if err = m.machines.ReleaseSlotOn(ctx, tx, tenant, row.MachineID, id); err != nil {
			return empty, err
		}
	}
	expiry := now.Add(30 * time.Second)
	var lease *time.Time = &expiry
	if terminal {
		lease = nil
	}
	q := query.BindExecutions(tx, m.names)
	updated, updateErr := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(row.Version)).Updates(map[string]any{"progress_json": row.ProgressJSON, "state": next, "state_reason": reason, "result_state": result, "last_seq": in.Seq, "lease_expires_at": lease, "version": row.Version + 1, "updated_at": now})
	if updateErr != nil {
		return empty, updateErr
	}
	if updated.RowsAffected != 1 {
		return empty, ErrConflict
	}
	receiptID, err := m.nextID()
	if err != nil {
		return empty, err
	}
	if err = receipts.WithContext(ctx).Create(&model.Receipt{ID: receiptID, TenantID: tenant, ExecutionID: id, DispatchEpoch: in.DispatchEpoch, Seq: in.Seq, Kind: in.Kind, ContentHash: digest, State: next, CreatedAt: now}); err != nil {
		return empty, err
	}
	if err = m.recordOutputKnowledgeOn(ctx, tx, row, in); err != nil {
		return empty, err
	}
	if row.BranchReason == "SKILL_DISTILLATION" && (next == "SUCCEEDED" || next == "FAILED" || next == StateCanceled) {
		if m.work.DistillationCompletedOn == nil {
			return empty, ErrCapabilityUnavailable
		}
		if err = m.work.DistillationCompletedOn(ctx, tx, tenant, id); err != nil {
			return empty, err
		}
	}
	row.State, row.StateReason, row.ResultState, row.Version = next, reason, result, row.Version+1
	detail["state"] = next
	if err = m.eventOn(ctx, tx, row, "execution.state_changed", "MACHINE", "IAM", detail); err != nil {
		return empty, err
	}
	return ReceiptView{Seq: in.Seq, State: next, CancelRequested: row.CancelRequestedAt != nil, PauseRequested: row.StateReason == "PAUSE_REQUESTED", ResumeRequested: row.StateReason == "RESUME_REQUESTED"}, nil
}

// Safety/control receipts remain possible while delivery is held.
func receiptOperation(kind string) string {
	if kind == "HEARTBEAT" || kind == "PAUSED" || kind == "RESUMED" || kind == "CANCELLED" || kind == "LOST" { //nolint:misspell // Frozen protocol state.
		return "CONTROL_RECEIPT"
	}
	return "RECEIPT"
}
