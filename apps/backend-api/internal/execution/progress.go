package execution

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"maps"
	"reflect"
	"strings"

	"7link.com/platform/internal/execution/model"
	"gorm.io/gorm"
)

// ProgressObservation contains measured content identities, never Agent claims.
// Final snapshots are bound to the exact immutable commit used by the Claim.
type ProgressObservation struct {
	Observable   map[string]string `json:"observable"`
	Dependencies map[string]bool   `json:"dependencies"`
	ChangedFiles map[string]string `json:"changed_files"`
	Truncated    bool              `json:"truncated"`
	HeadCommit   string            `json:"head_commit,omitempty"`
	Final        bool              `json:"final"`
	Baseline     bool              `json:"baseline"`
}
type ProgressState struct {
	Observation                ProgressObservation `json:"observation"`
	DispatchEpoch              int64               `json:"dispatch_epoch"`
	NoProgressCount            int                 `json:"no_progress_count"`
	DependencyMissCount        int                 `json:"dependency_miss_count"`
	StopReason                 string              `json:"stop_reason,omitempty"`
	LastStopReason             string              `json:"last_stop_reason,omitempty"`
	HumanResumeCount           int                 `json:"human_resume_count"`
	TotalUnchangedActions      int64               `json:"total_unchanged_actions"`
	TotalDependencyMissActions int64               `json:"total_dependency_miss_actions"`
	SimilarityRatioPPM         *int                `json:"similarity_ratio_ppm,omitempty"`
	SimilarityUnion            int                 `json:"similarity_union"`
	SimilarityChanged          int                 `json:"similarity_changed"`
}

func (m *Module) pinProgressPolicyOn(ctx context.Context, tx *gorm.DB, row *model.Execution, plan *ExecutionPlan) error {
	plan.ProgressRootCommit = plan.BaseCommit
	if m.templateVerifiers.catalog.ForWorkOn == nil {
		return nil
	}
	binding, found, err := m.templateVerifiers.catalog.ForWorkOn(ctx, tx, row.TenantID, row.ProjectID, row.WorkItemID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	plan.ProgressPolicy, plan.ProgressTemplateVersionID, plan.ProgressDefinitionHash = binding.ProgressPolicy, binding.TemplateVersionID, binding.DefinitionHash
	return nil
}

func progressDigest(v string) bool {
	if v == "" {
		return true
	} // absent/deleted path is a fact, not an empty file hash
	if (len(v) != 64 && len(v) != 40) || strings.ToLower(v) != v {
		return false
	}
	_, e := hex.DecodeString(v)
	return e == nil
}
func advanceProgress(plan ExecutionPlan, prior *ProgressState, obs ProgressObservation) (ProgressState, error) {
	out := ProgressState{}
	if prior != nil {
		out = *prior
	}
	if len(obs.ChangedFiles) > 20000 || obs.Final && !commitHash(obs.HeadCommit) {
		return out, ErrInvalidArgument
	}
	for name, digest := range obs.ChangedFiles {
		if name == "" || !progressDigest(digest) {
			return out, ErrInvalidArgument
		}
	}
	p := plan.ProgressPolicy
	if p == nil {
		if len(obs.Observable) > 0 || len(obs.Dependencies) > 0 {
			return out, ErrInvalidArgument
		}
	} else {
		if len(obs.Observable) != len(p.ObservablePaths) || len(obs.Dependencies) != len(p.RequiredDependencyPaths) {
			return out, ErrInvalidArgument
		}
		missing := false
		for _, name := range p.ObservablePaths {
			v, ok := obs.Observable[name]
			if !ok || !progressDigest(v) {
				return out, ErrInvalidArgument
			}
		}
		for _, name := range p.RequiredDependencyPaths {
			present, ok := obs.Dependencies[name]
			if !ok {
				return out, ErrInvalidArgument
			}
			missing = missing || !present
		}
		// Final flushes persist the output manifest without counting a second action.
		if !obs.Final && !obs.Baseline && out.StopReason == "" {
			if prior != nil && reflect.DeepEqual(prior.Observation.Observable, obs.Observable) {
				out.NoProgressCount++
				out.TotalUnchangedActions++
			} else {
				out.NoProgressCount = 0
			}
			if missing {
				out.DependencyMissCount++
				out.TotalDependencyMissActions++
			} else {
				out.DependencyMissCount = 0
			}
			if p.NoProgressLimit > 0 && out.NoProgressCount >= p.NoProgressLimit {
				out.StopReason = "AUTONOMY_NO_PROGRESS"
			}
			if p.DependencyMissLimit > 0 && out.DependencyMissCount >= p.DependencyMissLimit {
				out.StopReason = "AUTONOMY_DEPENDENCY_MISSING"
			}
		}
	}
	obs.Observable = maps.Clone(obs.Observable)
	obs.Dependencies = maps.Clone(obs.Dependencies)
	obs.ChangedFiles = maps.Clone(obs.ChangedFiles)
	out.Observation = obs
	return out, nil
}

// BR03-67: compare final blobs over the union of root-relative changed paths.
// Truncation, a missing parent Claim or fewer than five files is uncomputable.
func changeSimilarity(parent, child ProgressObservation) (*int, int, int) {
	if !parent.Final || !child.Final || parent.Truncated || child.Truncated {
		return nil, 0, 0
	}
	union := map[string]bool{}
	for p := range parent.ChangedFiles {
		union[p] = true
	}
	for p := range child.ChangedFiles {
		union[p] = true
	}
	if len(union) < 5 {
		return nil, len(union), 0
	}
	changed := 0
	for p := range union {
		a, aok := parent.ChangedFiles[p]
		b, bok := child.ChangedFiles[p]
		if aok != bok || a != b {
			changed++
		}
	}
	ratio := changed * 1000000 / len(union)
	return &ratio, len(union), changed
}

func (m *Module) progressReceiptOn(ctx context.Context, tx *gorm.DB, row *model.Execution, plan ExecutionPlan, obs *ProgressObservation) (ProgressState, error) {
	var prior *ProgressState
	if row.ProgressJSON != nil {
		prior = new(ProgressState)
		if json.Unmarshal([]byte(*row.ProgressJSON), prior) != nil {
			return ProgressState{}, ErrUnavailable
		}
	}
	if obs == nil {
		return ProgressState{}, ErrInvalidArgument
	}
	if obs.Baseline && prior != nil && prior.DispatchEpoch == row.DispatchEpoch {
		return ProgressState{}, ErrConflict
	}
	out, e := advanceProgress(plan, prior, *obs)
	out.DispatchEpoch = row.DispatchEpoch
	return out, e
}

func (m *Module) spinningClaimOn(ctx context.Context, tx *gorm.DB, row *model.Execution, head string) (string, error) {
	if row.BranchReason != "QUALITY" || row.ParentExecutionID == nil || row.ProgressJSON == nil {
		return "", nil
	}
	parent, e := m.rowOn(ctx, tx, row.TenantID, *row.ParentExecutionID, false)
	if e != nil {
		return "", e
	}
	pv, e := view(parent)
	if e != nil {
		return "", e
	}
	cv, e := view(row)
	if e != nil {
		return "", e
	}
	if pv.Plan.ProgressRootCommit != cv.Plan.ProgressRootCommit {
		return "", ErrConflict
	}
	if parent.State != "SUCCEEDED" || parent.StateReason != "CLAIM_SUBMITTED" || parent.ProgressJSON == nil {
		return "", nil
	}
	var a, b ProgressState
	if json.Unmarshal([]byte(*parent.ProgressJSON), &a) != nil || json.Unmarshal([]byte(*row.ProgressJSON), &b) != nil {
		return "", ErrUnavailable
	}
	if b.Observation.HeadCommit != head {
		return "", ErrConflict
	}
	ratio, union, changed := changeSimilarity(a.Observation, b.Observation)
	b.SimilarityRatioPPM, b.SimilarityUnion, b.SimilarityChanged = ratio, union, changed
	reason := ""
	if ratio != nil && *ratio < 80000 {
		reason = "AUTONOMY_SPINNING"
		b.StopReason = reason
	}
	encoded, e := json.Marshal(b)
	if e != nil {
		return "", e
	}
	s := string(encoded)
	row.ProgressJSON = &s
	return reason, nil
}
