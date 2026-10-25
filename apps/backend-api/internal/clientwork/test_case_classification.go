package clientwork

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"gorm.io/gorm"
	"strings"
	"unicode/utf8"
)

func (m *Module) validateTestCaseClassificationOn(ctx context.Context, tx *gorm.DB, p guid.ID, in TestCaseBody) error {
	if !utf8.ValidString(in.Preconditions) || !utf8.ValidString(in.CriterionSource) || utf8.RuneCountInString(in.Preconditions) > 8000 || utf8.RuneCountInString(in.CriterionSource) > 2000 || len(in.Priority) != 2 || in.Priority[0] != 'P' || in.Priority[1] < '0' || in.Priority[1] > '5' {
		return ErrInvalid
	}
	if in.RequirementID == nil {
		if in.CriterionID != nil || strings.TrimSpace(in.CriterionSource) != "" {
			return ErrInvalid
		}
		return nil
	}
	if *in.RequirementID <= 0 {
		return ErrInvalid
	}
	scope, err := m.projects.TestCaseScopeOn(ctx, tx, authn.TenantIDFromContext(ctx), p, in.WorkItemID, *in.RequirementID)
	if err != nil {
		return err
	}
	if in.CriterionID == nil {
		return nil
	}
	for _, c := range scope.Criteria {
		if c.ID == *in.CriterionID {
			return nil
		}
	}
	return ErrInvalid
}
