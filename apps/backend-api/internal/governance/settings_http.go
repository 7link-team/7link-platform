package governance

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type settingsGetOutput struct {
	ETag string `header:"ETag"`
	Body respx.Envelope[SettingsView]
}
type settingsPutInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch  string `header:"If-Match" required:"true" doc:"Quoted current governance settings version; defaults have version 0."`
	Cookie   string `header:"Cookie"`
	Body     struct {
		SettingsValue
		ReauthProof string `json:"reauth_proof,omitempty" maxLength:"256"`
	}
}

type rulePutInput struct {
	TenantID  string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ProjectID string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Kind      string `path:"kind" maxLength:"48"`
	IfMatch   string `header:"If-Match" required:"true" doc:"Quoted rule version; defaults have version 0."`
	Cookie    string `header:"Cookie"`
	Body      struct {
		EscalationRuleValue
		ReauthProof string `json:"reauth_proof,omitempty" maxLength:"256"`
	}
}

type rulesListInput struct {
	TenantID  string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ProjectID string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func parseGovernanceVersion(value string) (int, error) {
	if len(value) < 3 || !strings.HasPrefix(value, "\"") || !strings.HasSuffix(value, "\"") {
		return 0, ErrInvalidArgument
	}
	n, err := strconv.Atoi(value[1 : len(value)-1])
	if err != nil || n < 0 || value != "\""+strconv.Itoa(n)+"\"" {
		return 0, ErrInvalidArgument
	}
	return n, nil
}

func (m *Module) registerSettingsHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "governance.settings.get", Method: http.MethodGet, Path: "/governance-settings", Summary: "Get governance defaults and limits", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 500, 503}}, authz.Guard{Resource: "policy", Verb: "read"}, m.getSettingsHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "governance.settings.put", Method: http.MethodPut, Path: "/governance-settings", Summary: "Update versioned governance defaults and limits", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 409, 422, 500, 503}}, authz.Guard{Resource: "policy", Verb: "manage"}, m.putSettingsHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "governance.rules.list", Method: http.MethodGet, Path: "/escalation-rules", Summary: "Get tenant or effective project escalation rules", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}, authz.Guard{Resource: "policy", Verb: "read"}, m.getRulesHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "governance.rule.put", Method: http.MethodPut, Path: "/escalation-rules/{kind}", Summary: "Update a tenant rule or stricter project override", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.Guard{Resource: "policy", Verb: "manage"}, m.putRuleHTTP)
}

func (m *Module) getSettingsHTTP(ctx context.Context, in *governanceCatalogInput) (*settingsGetOutput, error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, inboxHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	value, err := m.readSettingsOn(ctx, db, tenant)
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	return &settingsGetOutput{ETag: "\"" + fmtVersion(value.Version) + "\"", Body: respx.OK(ctx, value).Body}, nil
}
func (m *Module) putSettingsHTTP(ctx context.Context, in *settingsPutInput) (*settingsGetOutput, error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, inboxHTTPError(ctx, ErrInvalidArgument)
	}
	version, err := parseGovernanceVersion(in.IfMatch)
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	db, err := m.writer()
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	var value SettingsView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		value, e = m.putSettingsOn(ctx, tx, tenant, version, in.Body.SettingsValue, in.Cookie, in.Body.ReauthProof)
		return e
	})
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	return &settingsGetOutput{ETag: "\"" + fmtVersion(value.Version) + "\"", Body: respx.OK(ctx, value).Body}, nil
}
func (m *Module) getRulesHTTP(ctx context.Context, in *rulesListInput) (*respx.Body[[]EscalationRuleView], error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, inboxHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	var rules []EscalationRuleView
	if in.ProjectID == "" {
		rules, err = m.readRulesOn(ctx, db, tenant)
	} else {
		project, e := guid.RequireID(in.ProjectID)
		if e != nil {
			return nil, inboxHTTPError(ctx, ErrInvalidArgument)
		}
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := m.authorizeProjectRulesOn(ctx, tx, tenant, project, "policy_read"); e != nil {
				return e
			}
			var e error
			rules, e = m.readEffectiveRulesOn(ctx, tx, tenant, project)
			return e
		})
	}
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	return respx.OK(ctx, rules), nil
}
func (m *Module) putRuleHTTP(ctx context.Context, in *rulePutInput) (*respx.Body[EscalationRuleView], error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, inboxHTTPError(ctx, ErrInvalidArgument)
	}
	if authn.TenantIDFromContext(ctx) != tenant {
		return nil, inboxHTTPError(ctx, ErrForbidden)
	}
	version, err := parseGovernanceVersion(in.IfMatch)
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	db, err := m.writer()
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	var rule EscalationRuleView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		if in.ProjectID == "" {
			rule, e = m.putRuleOn(ctx, tx, tenant, in.Kind, version, in.Body.EscalationRuleValue, in.Cookie, in.Body.ReauthProof)
		} else {
			project, err := guid.RequireID(in.ProjectID)
			if err != nil {
				return ErrInvalidArgument
			}
			rule, e = m.putProjectRuleOn(ctx, tx, tenant, project, in.Kind, version, in.Body.EscalationRuleValue, in.Cookie, in.Body.ReauthProof)
		}
		return e
	})
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	return respx.OK(ctx, rule), nil
}
