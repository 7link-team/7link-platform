package templates

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type RubricWorkPath struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkID   string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type rubricWorkCreateInput struct {
	RubricWorkPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           RubricScoreInput
}
type RubricPackagePath struct {
	EntityID  string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	PackageID string `path:"package_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ItemID    string `query:"item_id" required:"true" pattern:"^[a-f0-9]{64}$" maxLength:"64"`
}
type rubricPackageCreateInput struct {
	RubricPackagePath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           RubricScoreInput
}

func (m *Module) registerRubricScoresHTTP(api huma.API) {
	owner := authz.ResourceOwnerResolver(m.rubricOwner)
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"entities"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	work := "/entities/{entity_id}/work-items/{work_item_id}/rubric-scores"
	client := "/entities/{entity_id}/delivery-packages/{package_id}/rubric-scores"
	authz.RegisterOwned(m.registrar, api, op("template.rubric-work-read", "GET", work, "Compare frozen exact-result internal and human rubric scores", 200), authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.rubricWorkReadHTTP)
	authz.RegisterOwned(m.registrar, api, op("template.rubric-work-create", "POST", work, "Append source-authorized exact-result rubric scores without accepting work", 201), authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.rubricWorkCreateHTTP)
	authz.RegisterOwned(m.registrar, api, op("template.rubric-package-read", "GET", client, "Compare published-item rubric scores without private work references", 200), authz.EntityPath, owner, authz.Guard{Resource: "channel", Verb: "read"}, m.rubricPackageReadHTTP)
	authz.RegisterOwned(m.registrar, api, op("template.rubric-package-create", "POST", client, "Append current signer scores for an exact immutable published item", 201), authz.EntityPath, owner, authz.Guard{Resource: "channel", Verb: "read"}, m.rubricPackageCreateHTTP)
}
func rubricSourceError(e error) error {
	switch {
	case errors.Is(e, delivery.ErrWorkVersion), errors.Is(e, delivery.ErrResultState), errors.Is(e, delivery.ErrResultCoverage), errors.Is(e, delivery.ErrResultIndependent), errors.Is(e, delivery.ErrVerificationPending), errors.Is(e, delivery.ErrWorkProjectReadOnly):
		return ErrConflict
	default:
		return e
	}
}
func (m *Module) rubricReadHTTP(ctx context.Context, project, work, packageID guid.ID, item string) (*respx.Body[RubricScoreboard], error) {
	var out RubricScoreboard
	e := m.transaction(ctx, func(tx *gorm.DB) error {
		target, e := m.rubricSubjectOn(ctx, tx, project, work, packageID, item, nil)
		if e != nil {
			return rubricSourceError(e)
		}
		out, e = m.rubricBoardOn(ctx, tx, target, packageID > 0)
		if e != nil {
			return e
		}
		if out.Definition == nil {
			return nil
		}
		if packageID == 0 {
			for _, stage := range []string{"HUMAN_REVIEW", "AGENT_ACCEPTANCE"} {
				_, err := m.rubricSubjectOn(ctx, tx, project, work, 0, "", &RubricScoreInput{ResultID: target.ResultID, WorkVersion: target.Version, Stage: stage})
				if err == nil {
					out.AllowedStages = append(out.AllowedStages, stage)
					continue
				}
				switch {
				case errors.Is(err, delivery.ErrWorkForbidden), errors.Is(err, delivery.ErrInvalidMember), errors.Is(err, delivery.ErrResultState), errors.Is(err, delivery.ErrResultIndependent), errors.Is(err, delivery.ErrVerificationPending):
					continue
				default:
					return rubricSourceError(err)
				}
			}
		} else {
			stage := "CLIENT_ACCEPTANCE"
			if target.ActorKind == "HUMAN" {
				stage = "HUMAN_REVIEW"
			}
			out.AllowedStages = []string{}
			_, err := m.rubricSubjectOn(ctx, tx, project, work, packageID, item, &RubricScoreInput{WorkVersion: target.Version, Stage: stage})
			if err == nil {
				out.AllowedStages = []string{stage}
			} else {
				switch {
				case errors.Is(err, delivery.ErrWorkForbidden), errors.Is(err, delivery.ErrInvalidMember), errors.Is(err, delivery.ErrResultState), errors.Is(err, delivery.ErrResultIndependent), errors.Is(err, delivery.ErrVerificationPending):
				default:
					return rubricSourceError(err)
				}
			}
		}
		return nil
	})
	if e != nil {
		return nil, templateError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) rubricCommandHTTP(ctx context.Context, project, work, packageID guid.ID, item, key string, in RubricScoreInput) (*respx.Body[RubricScoreReceipt], error) {
	operation := "template.rubric:" + project.String() + ":" + work.String() + ":" + packageID.String() + ":" + item
	// Every replay re-resolves current identity, stage, result and package hash.
	out, e := command(m, ctx, key, operation, in, func(tx *gorm.DB) (RubricScoreReceipt, error) {
		target, e := m.rubricSubjectOn(ctx, tx, project, work, packageID, item, &in)
		if e != nil {
			return RubricScoreReceipt{}, rubricSourceError(e)
		}
		return m.recordRubricScoreOn(ctx, tx, target, in)
	}, func(tx *gorm.DB) error {
		_, e := m.rubricSubjectOn(ctx, tx, project, work, packageID, item, &in)
		return rubricSourceError(e)
	})
	if e != nil {
		return nil, e
	}
	if packageID > 0 {
		r := &out.Body.Data
		r.WorkItemID = 0
		r.ResultID = 0
		r.ResultVersion = 0
		r.HeadCommit = ""
		r.Authority = ""
		r.ScorerID = 0
		for i := range r.Scores {
			r.Scores[i].EvidenceRefs = []guid.ID{}
			r.Scores[i].Proofs = nil
		}
	}
	return out, nil
}
func (m *Module) rubricWorkReadHTTP(ctx context.Context, in *RubricWorkPath) (*respx.Body[RubricScoreboard], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	w, e := guid.RequireID(in.WorkID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return m.rubricReadHTTP(ctx, p, w, 0, "")
}
func (m *Module) rubricWorkCreateHTTP(ctx context.Context, in *rubricWorkCreateInput) (*respx.Body[RubricScoreReceipt], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	w, e := guid.RequireID(in.WorkID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return m.rubricCommandHTTP(ctx, p, w, 0, "", in.IdempotencyKey, in.Body)
}
func (m *Module) rubricPackageReadHTTP(ctx context.Context, in *RubricPackagePath) (*respx.Body[RubricScoreboard], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.PackageID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return m.rubricReadHTTP(ctx, p, 0, id, in.ItemID)
}
func (m *Module) rubricPackageCreateHTTP(ctx context.Context, in *rubricPackageCreateInput) (*respx.Body[RubricScoreReceipt], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.PackageID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return m.rubricCommandHTTP(ctx, p, 0, id, in.ItemID, in.IdempotencyKey, in.Body)
}
