package templates

import (
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"gorm.io/gorm"
)

type VerifierPluginPage struct {
	Items   []VerifierPlugin `json:"items"`
	HasMore bool             `json:"has_more"`
}

func (m *Module) verifierPluginsHTTP(ctx context.Context, _ *struct{}) (*respx.Body[VerifierPluginPage], error) {
	if e := m.transaction(ctx, func(tx *gorm.DB) error { return m.authorizeOn(ctx, tx, "template_read", 0) }); e != nil {
		return nil, templateError(ctx, e)
	}
	return respx.OK(ctx, VerifierPluginPage{Items: installedVerifierPlugins()}), nil
}
