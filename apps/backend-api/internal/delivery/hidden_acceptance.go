package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Completed execution reviews validate the current result, without requiring
// the executor's former lease or current_execution_id (cleared at completion).
// Caller has already locked all participating principals before project locks.
func (m *Module) authorizeVisualReviewOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, result guid.ID, version int, criterion guid.ID, head string) error {
	if tx == nil || tx.Statement == nil || l.TenantID <= 0 || l.ProjectID <= 0 || l.WorkItemID <= 0 || l.ExecutionID <= 0 || result <= 0 || version < 1 || criterion <= 0 || authn.TenantIDFromContext(ctx) != l.TenantID || authn.PrincipalIDFromContext(ctx) != l.ActorID {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if m == nil || m.service == nil || m.workPolicy == nil || m.members == nil {
		return ErrUnavailable
	}
	member, err := m.members.IsMemberActiveOn(ctx, tx, l.TenantID, l.ActorID)
	if err != nil {
		return err
	}
	if !member {
		return ErrWorkForbidden
	}
	c, err := m.workPolicy(ctx, tx, l.TenantID, l.ProjectID, "verification_run", l.ActorID)
	if err != nil {
		return err
	}
	w := query.BindWorkItems(tx, m.service.names)
	scope, err := workScope(c, l.ProjectID, w.CreatedBy)
	if err != nil {
		return err
	}
	p := query.BindProjects(tx, m.service.names)
	project, err := p.WithContext(ctx).Where(p.TenantID.Eq(l.TenantID), p.ProjectID.Eq(l.ProjectID), p.ArchivedAt.IsNull()).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if err != nil {
		return err
	}
	if project.Stage != "ACTIVE" {
		return ErrWorkProjectReadOnly
	}
	stmt := w.WithContext(ctx).Where(w.TenantID.Eq(l.TenantID), w.ProjectID.Eq(l.ProjectID), w.ID.Eq(l.WorkItemID), w.ArchivedAt.IsNull()).Clauses(clause.Locking{Strength: "UPDATE"})
	if scope != nil {
		stmt = stmt.Where(scope)
	}
	work, err := stmt.First()
	if err != nil {
		return err
	}
	if work.CurrentClaimID == nil || *work.CurrentClaimID != result || work.BaselineStale || work.PausedAt != nil || work.IsContainer || !slices.Contains([]string{"INTERNAL_QA", "NEEDS_HUMAN_REVIEW"}, work.State) {
		return ErrResultState
	}
	r, err := m.resultOn(ctx, tx, l.TenantID, l.ProjectID, l.WorkItemID, result)
	if err != nil {
		return err
	}
	if r.Sequence != version || r.SourceExecutionID == nil || *r.SourceExecutionID != l.ExecutionID || r.HeadCommit != head || r.BaselineVersionNo != work.BaselineVersionNo || r.ActorID == l.ActorID {
		return ErrResultState
	}
	criteria := query.BindWorkCriteria(tx, m.service.names)
	_, err = criteria.WithContext(ctx).Where(criteria.TenantID.Eq(l.TenantID), criteria.ProjectID.Eq(l.ProjectID), criteria.WorkItemID.Eq(l.WorkItemID), criteria.ID.Eq(criterion)).First()
	return err
}

var ErrHiddenAcceptanceFailed = fmt.Errorf("%w: hidden acceptance verification failed", ErrVerificationPending)

var ErrHiddenAcceptanceUnavailable = fmt.Errorf("%w: isolated hidden acceptance verifier is unavailable", ErrUnavailable)

type HiddenAcceptanceRequest struct {
	TenantID, ProjectID, WorkItemID, ResultID, ExecutionID guid.ID
	SuiteHash, HeadCommit                                  string
	Cases                                                  []RequirementCaseSnapshot
}
type HiddenAcceptanceVerifier struct {
	PrincipalID func() guid.ID
	DispatchOn  func(context.Context, *gorm.DB, HiddenAcceptanceRequest) error
	PassedOn    func(context.Context, *gorm.DB, HiddenAcceptanceRequest) (bool, error)
	VerdictOn   func(context.Context, *gorm.DB, HiddenAcceptanceRequest) (string, error)
}
type HiddenAcceptanceConnector struct {
	Bind   func(HiddenAcceptanceVerifier) error
	ReadOn func(context.Context, *gorm.DB, HiddenAcceptanceRequest) (HiddenAcceptanceRequest, error)
}

func (m *Module) provideHiddenAcceptance(s *module.Services) error {
	return module.Provide[HiddenAcceptanceConnector](s, HiddenAcceptanceConnector{ReadOn: m.readHiddenAcceptanceRequestOn, Bind: func(v HiddenAcceptanceVerifier) error {
		if v.DispatchOn == nil || v.PassedOn == nil || m.hiddenVerifier.DispatchOn != nil {
			return ErrInvalidArgument
		}
		m.hiddenVerifier = v
		return nil
	}})
}

// A normal deterministic or Agent receipt proves nothing about hidden cases.
// Until a separate platform verifier can execute and attest the frozen bytes,
// neither internal QA nor Human acceptance may silently skip these cases.
func (m *Module) hiddenAcceptanceGateOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, results ...*model.ResultVersion) error {
	request, err := m.hiddenAcceptanceRequestOn(ctx, tx, work, results...)
	if err != nil || len(request.Cases) == 0 {
		return err
	}
	if m.hiddenVerifier.PassedOn == nil {
		return ErrHiddenAcceptanceUnavailable
	}
	if m.hiddenVerifier.VerdictOn != nil {
		verdict, e := m.hiddenVerifier.VerdictOn(ctx, tx, request)
		if e != nil {
			return ErrHiddenAcceptanceUnavailable
		}
		if verdict == "FAILED" {
			return ErrHiddenAcceptanceFailed
		}
	}
	passed, err := m.hiddenVerifier.PassedOn(ctx, tx, request)
	if err != nil {
		return ErrHiddenAcceptanceUnavailable
	}
	if !passed {
		return ErrHiddenAcceptanceUnavailable
	}
	return nil
}

func (m *Module) dispatchHiddenAcceptanceOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, result *model.ResultVersion) error {
	request, err := m.hiddenAcceptanceRequestOn(ctx, tx, work, result)
	if err != nil || len(request.Cases) == 0 {
		return err
	}
	if m.hiddenVerifier.DispatchOn == nil {
		return nil
	} // The gate remains explicitly unavailable.
	return m.hiddenVerifier.DispatchOn(ctx, tx, request)
}

func (m *Module) hiddenAcceptanceRequestOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, results ...*model.ResultVersion) (HiddenAcceptanceRequest, error) {
	request := HiddenAcceptanceRequest{TenantID: work.TenantID, ProjectID: work.ProjectID, WorkItemID: work.ID}
	if len(results) > 0 && results[0] != nil {
		r := results[0]
		if r.WorkItemID != work.ID || r.ProjectID != work.ProjectID || r.TenantID != work.TenantID || r.BaselineVersionNo != work.BaselineVersionNo {
			return request, ErrInvalidArgument
		}
		request.ResultID, request.HeadCommit = r.ID, r.HeadCommit
		if r.SourceExecutionID != nil {
			request.ExecutionID = *r.SourceExecutionID
		}
	}
	if work.RequirementID == nil || work.BaselineVersionNo <= 0 {
		return request, nil
	}
	q := query.BindRequirementVersions(tx, m.service.names)
	version, err := q.WithContext(ctx).Where(q.TenantID.Eq(work.TenantID), q.ProjectID.Eq(work.ProjectID), q.RequirementID.Eq(*work.RequirementID), q.VersionNo.Eq(work.BaselineVersionNo)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Legacy baselines predate stored suites. This hook adds no claim about
		// them and preserves their existing verification rules.
		return request, nil
	}
	if err != nil {
		return request, err
	}
	if _, err := frozenSuiteCaseCount(version); err != nil {
		return request, err
	}
	if version.AcceptanceSuite == "" || version.AcceptanceSuite == "{}" {
		return request, nil
	}
	var suite requirementFrozenSuite
	if err := json.Unmarshal([]byte(version.AcceptanceSuite), &suite); err != nil {
		return request, err
	}
	if err := validateHiddenRequirementCases(suite.Cases); err != nil {
		return request, err
	}
	for _, c := range suite.Cases {
		if c.IsHiddenAcceptance && c.WorkItemID == work.ID {
			request.Cases = append(request.Cases, c)
		}
	}
	request.SuiteHash = version.SuiteHash
	return request, nil
}

// The case owner supplies hidden content hashes. Reject a faulty connector
// before its plaintext could enter immutable requirement history or contexts.
func validateHiddenRequirementCases(cases []RequirementCaseSnapshot) error {
	for _, c := range cases {
		if !c.IsHiddenAcceptance {
			continue
		}
		digest, err := hex.DecodeString(c.ContentHash)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != c.ContentHash || c.ID <= 0 || c.Version <= 0 || c.WorkItemID <= 0 || c.Title != "" || c.Steps != "" || c.Expected != "" || c.RequirementID != nil || c.CriterionID != nil || c.CriterionSource != "" || c.Preconditions != "" || c.Priority != "" {
			return ErrInvalidArgument
		}
	}
	return nil
}

// readHiddenAcceptanceRequestOn reconstructs the immutable baseline and result
// through the owning module. Job payloads cannot choose cases, hashes or HEAD.
func (m *Module) readHiddenAcceptanceRequestOn(ctx context.Context, tx *gorm.DB, in HiddenAcceptanceRequest) (HiddenAcceptanceRequest, error) {
	empty := HiddenAcceptanceRequest{}
	p := query.BindProjects(tx, m.service.names)
	if _, err := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(in.TenantID), p.ProjectID.Eq(in.ProjectID), p.ArchivedAt.IsNull()).First(); err != nil {
		return empty, err
	}
	w := query.BindWorkItems(tx, m.service.names)
	work, err := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(in.TenantID), w.ProjectID.Eq(in.ProjectID), w.ID.Eq(in.WorkItemID), w.ArchivedAt.IsNull()).First()
	if err != nil {
		return empty, err
	}
	r := query.BindResultVersions(tx, m.service.names)
	result, err := r.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(r.TenantID.Eq(in.TenantID), r.ProjectID.Eq(in.ProjectID), r.WorkItemID.Eq(in.WorkItemID), r.ID.Eq(in.ResultID)).First()
	if err != nil {
		return empty, err
	}
	if work.BaselineStale || work.CurrentClaimID == nil || *work.CurrentClaimID != result.ID || !slices.Contains([]string{"INTERNAL_QA", "NEEDS_HUMAN_REVIEW"}, work.State) {
		return empty, ErrResultState
	}
	return m.hiddenAcceptanceRequestOn(ctx, tx, work, result)
}

func (m *Module) hiddenAcceptancePrincipalsOn(ctx context.Context, tx *gorm.DB, tenant, project, workID guid.ID) ([]guid.ID, error) {
	if m.hiddenVerifier.PrincipalID == nil || m.hiddenVerifier.PrincipalID() <= 0 {
		return nil, nil
	}
	q := query.BindWorkItems(tx, m.service.names)
	work, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(workID)).First()
	if err != nil {
		return nil, err
	}
	request, err := m.hiddenAcceptanceRequestOn(ctx, tx, work)
	if err != nil {
		return nil, err
	}
	if len(request.Cases) == 0 {
		return nil, nil
	}
	return []guid.ID{m.hiddenVerifier.PrincipalID()}, nil
}
