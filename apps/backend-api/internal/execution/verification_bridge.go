package execution

import (
	"7link.com/platform/internal/delivery"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
)

type verificationAdapter struct{ enabled bool }

func (*verificationAdapter) ModuleName() string { return "execution_verification_bridge" }
func (m *verificationAdapter) Enabled() bool    { return m.enabled }
func (*verificationAdapter) ModuleDependencies() []string {
	return []string{delivery.Name, Name}
}
func (*verificationAdapter) InjectServices(s *module.Services) error {
	e, err := module.Resolve[VerificationExecutor](s)
	if err != nil {
		return err
	}
	c, err := module.Resolve[delivery.VerificationExecutorConnector](s)
	if err != nil {
		return err
	}
	quality, err := module.Resolve[QualityLauncher](s)
	if err != nil {
		return err
	}
	return c.Bind(delivery.VerificationExecutor{
		QualityPrincipalsOn: func(ctx context.Context, tx *gorm.DB, tenant, parent guid.ID) ([]guid.ID, error) {
			if quality.PrincipalsOn == nil {
				return nil, delivery.ErrUnavailable
			}
			ids, e := quality.PrincipalsOn(ctx, tx, tenant, parent)
			return ids, verificationBridgeError(e)
		},
		LaunchQualityOn: func(ctx context.Context, tx *gorm.DB, in delivery.QualityLaunchRequest) (guid.ID, error) {
			if quality.LaunchOn == nil {
				return 0, delivery.ErrUnavailable
			}
			id, e := quality.LaunchOn(ctx, tx, QualityLaunchRequest{TenantID: in.TenantID, ProjectID: in.ProjectID, WorkItemID: in.WorkItemID, ParentExecutionID: in.ParentExecutionID, SourceDecisionID: in.SourceDecisionID, HeadCommit: in.HeadCommit, Feedback: in.Feedback})
			return id, verificationBridgeError(e)
		},
		ReviewersOn: func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, stage string) ([]guid.ID, error) {
			if e.ReviewersOn == nil {
				return nil, delivery.ErrUnavailable
			}
			ids, err := e.ReviewersOn(ctx, tx, tenant, id, stage)
			return ids, verificationBridgeError(err)
		},
		SourcePrincipalOn: func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (guid.ID, error) {
			if e.SourcePrincipalOn == nil {
				return 0, delivery.ErrUnavailable
			}
			principal, err := e.SourcePrincipalOn(ctx, tx, tenant, id)
			return principal, verificationBridgeError(err)
		},
		QueueOn: func(ctx context.Context, tx *gorm.DB, r delivery.VerificationRequest) (guid.ID, error) {
			id, err := e.QueueOn(ctx, tx, VerificationRequest{AllowFamilyFallback: r.AllowFamilyFallback, TenantID: r.TenantID, ProjectID: r.ProjectID, WorkItemID: r.WorkItemID, ResultID: r.ResultID, ExecutionID: r.ExecutionID, CriterionID: r.CriterionID, ResultVersion: r.ResultVersion, Method: r.Method, HeadCommit: r.HeadCommit, Argv: r.Argv, TimeoutSec: r.TimeoutSec, ReportPath: r.ReportPath, ReviewerPrincipalID: r.ReviewerPrincipalID, Stage: r.Stage, CriterionText: r.CriterionText})
			return id, verificationBridgeError(err)
		},
		ReceiptOn: func(ctx context.Context, tx *gorm.DB, tenant, project, work, result, task guid.ID) (delivery.VerificationReceipt, error) {
			r, err := e.ReceiptOn(ctx, tx, tenant, project, work, result, task)
			return delivery.VerificationReceipt{TaskID: r.TaskID, ResultID: r.ResultID, ExecutionID: r.ExecutionID, CriterionID: r.CriterionID, ResultVersion: r.ResultVersion, Method: r.Method, Verdict: r.Verdict, Reason: r.Reason, ReceiptHash: r.ReceiptHash, PlanHash: r.PlanHash, HeadCommit: r.HeadCommit, ExitCode: r.ExitCode, ReviewerID: r.ReviewerID, Model: r.Model, Family: r.Family, Stage: r.Stage, FamilyFallback: r.FamilyFallback}, verificationBridgeError(err)
		},
	})
}
func verificationBridgeError(err error) error {
	switch {
	case errors.Is(err, ErrConflict), errors.Is(err, ErrQueueFull):
		return delivery.ErrResultState
	case errors.Is(err, ErrInvalidArgument):
		return delivery.ErrInvalidArgument
	case errors.Is(err, ErrForbidden):
		return delivery.ErrWorkForbidden
	case errors.Is(err, ErrNotFound):
		return delivery.ErrProjectNotFound
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrCapabilityUnavailable):
		return delivery.ErrUnavailable
	default:
		return err
	}
}

// NewVerificationBridge registers the existing capability adapter.
func NewVerificationBridge(enabled bool) module.Module { return &verificationAdapter{enabled: enabled} }

var _ module.Enabler = (*verificationAdapter)(nil)

var _ module.Depender = (*verificationAdapter)(nil)

var _ module.ServiceInjector = (*verificationAdapter)(nil)
