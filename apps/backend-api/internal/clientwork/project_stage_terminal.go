package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
)

func (m *Module) bindProjectStages(s *module.Services) error {
	c, e := module.Resolve[delivery.ProjectStagePackagesConnector](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	return c.Bind(delivery.ProjectStagePackages{FactsOn: m.projectStageFactsOn, ArchiveOn: m.archiveProjectDraftPackagesOn, DecisionOn: m.projectCancellationDecisionOn})
}
func (m *Module) projectCancellationDecisionOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, version int, reason, source string) error {
	return query.BindCommandDecision(tx, m.names).WithContext(ctx).Create(&model.CommandDecision{TenantID: tenant, ID: id, ProjectID: project, TargetKind: "PROJECT", TargetID: project, Action: "CANCEL", SourceID: project, SourceVersion: version, ActorID: authn.PrincipalIDFromContext(ctx), Reason: reason, AuthoritySource: source, CreatedAt: now()})
}

// The project owner holds its serialization lock. Facts come exclusively from
// immutable published manifests, actual receipts and canonical rework sources.
func (m *Module) projectStageFactsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (delivery.ProjectStagePackageFacts, error) {
	out := delivery.ProjectStagePackageFacts{Outcomes: []delivery.ProjectClientOutcome{}}
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return out, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalid
	}
	q := query.BindDeliveryPackage(tx, m.names)
	rows, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ArchivedAt.IsNull(), q.State.Neq("DRAFT")).Order(q.CreatedAt, q.ID).Find()
	if e != nil {
		return out, e
	}
	receipts := query.BindPackageReceipt(tx, m.names)
	dispositions := query.BindReworkDisposition(tx, m.names)
	for _, p := range rows {
		out.Published = true
		rs, e := receipts.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(receipts.TenantID.Eq(tenant), receipts.ProjectID.Eq(project), receipts.PackageID.Eq(p.ID)).Find()
		if e != nil {
			return out, e
		}
		if len(rs) == 0 {
			out.Pending = true
			continue
		}
		if len(rs) != 1 || rs[0].ContentHash != p.ContentHash {
			return out, ErrConflict
		}
		receipt := rs[0]
		var manifest packageManifest
		if json.Unmarshal([]byte(p.Manifest), &manifest) != nil {
			return out, ErrConflict
		}
		public, e := json.Marshal(manifest.Content)
		if e != nil {
			return out, e
		}
		digest := sha256.Sum256(public)
		if hex.EncodeToString(digest[:]) != p.ContentHash {
			return out, ErrConflict
		}
		items := packageItems(p, manifest.Content)
		if len(items) == 0 || len(items) != len(manifest.Refs) {
			return out, ErrConflict
		}
		outcomes := []PackageItemDecision{}
		if receipt.ItemOutcomes != nil {
			if json.Unmarshal([]byte(*receipt.ItemOutcomes), &outcomes) != nil {
				return out, ErrConflict
			}
		} else {
			for _, item := range items {
				result := "REJECTED"
				if receipt.Decision == "ACCEPT" {
					result = "ACCEPTED"
				}
				outcomes = append(outcomes, PackageItemDecision{ItemID: item.ItemID, Result: result})
			}
		}
		if len(outcomes) != len(items) {
			return out, ErrConflict
		}
		seen := map[string]bool{}
		rejected := false
		for i, item := range items {
			index := slices.IndexFunc(outcomes, func(v PackageItemDecision) bool { return v.ItemID == item.ItemID })
			if index < 0 || seen[item.ItemID] {
				return out, ErrConflict
			}
			seen[item.ItemID] = true
			v := outcomes[index]
			if v.Result != "ACCEPTED" && v.Result != "REJECTED" {
				return out, ErrConflict
			}
			ref := manifest.Refs[i]
			fact := delivery.ProjectClientOutcome{WorkItemID: ref.WorkItemID, ResultID: ref.ResultID, Accepted: v.Result == "ACCEPTED", DecidedAt: receipt.CreatedAt, ReceiptID: receipt.ID}
			out.HasAccepted = out.HasAccepted || fact.Accepted
			if !fact.Accepted {
				rejected = true
				ds, e := dispositions.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(dispositions.TenantID.Eq(tenant), dispositions.ProjectID.Eq(project), dispositions.ReceiptID.Eq(receipt.ID), dispositions.ItemID.Eq(item.ItemID)).Find()
				if e != nil {
					return out, e
				}
				fact.Declined = slices.ContainsFunc(ds, func(d *model.ReworkDisposition) bool { return d.Mode == "DECLINE" && d.ClientMessage != "" })
			}
			out.Outcomes = append(out.Outcomes, fact)
		}
		if rejected {
			if m.reworkSources.ReadOn == nil {
				return out, ErrUnavailable
			}
			source, found, e := m.reworkSources.ReadOn(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), tenant, project, receipt.ID)
			if e != nil {
				return out, e
			}
			out.OpenRework = out.OpenRework || !found || !slices.Contains([]string{"CONVERTED", "MERGED", "DECLINED"}, source.State)
		}
	}
	return out, nil
}
