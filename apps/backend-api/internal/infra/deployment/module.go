// Package deployment publishes the display identity of this deployment.
//
// A build carries no brand of its own: the product and organization names
// describe whoever deployed it, so they are configuration, and a build that
// configures neither says so with neutral wording rather than with whichever
// brand happened to be first. The console reads them here instead of having
// them compiled in, so one build serves any deployment.
//
// This is deliberately not the application's own name. That one identifies the
// service in logs and on the console banner, and an operator has every reason
// to call the service `iam-api` while the people signing in see something else
// entirely. Binding the two would make the log line and the sign-in page
// impossible to name independently.
package deployment

import (
	"context"
	"net/http"

	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
)

const (
	Name = "deployment"
	Path = "/deployment"
)

// Config is the display identity an operator configures. Both fields are
// optional: a deployment that names neither is served neutral wording.
type Config struct {
	Name         string
	Organization string
}

// DeploymentIdentity is what the console reads before it can render anything of its own.
//
// It answers for the deployment, not for a tenant. Should a deployment ever
// brand itself per tenant, that arrives as a scoped route beside this one and
// this response stays what it is now -- the deployment's default -- so nothing
// reading it today has to change.
type DeploymentIdentity struct {
	// Name is the console's display name, empty when unconfigured. The client
	// owns the neutral fallback, because it is the one rendering it.
	Name string `json:"name" doc:"console display name; empty when the deployment configures none"`
	// Organization is named in the console footer, empty when unconfigured.
	Organization string `json:"organization" doc:"organization named in the console footer; empty when the deployment configures none"`
}

type Module struct {
	runtime *appenv.Runtime
	cfg     Config
}

var (
	_ module.Module        = (*Module)(nil)
	_ module.HTTPRegistrar = (*Module)(nil)
)

// NewModule builds the deployment identity module. It is always registered:
// the console asks for this before it has a session, and a deployment that
// configured nothing still has to be answered rather than left waiting.
func NewModule(rt *appenv.Runtime, cfg Config) *Module {
	return &Module{runtime: rt, cfg: cfg}
}

func (*Module) ModuleName() string { return Name }

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	authz.RegisterPublic(api, huma.Operation{
		OperationID: "deployment-identity",
		Method:      http.MethodGet,
		Path:        Path,
		Summary:     "Return the display identity of this deployment",
		Description: "Names the console renders before a session exists. Public because the sign-in page needs them.",
		Tags:        []string{"system"},
	}, func(ctx context.Context, _ *struct{}) (*respx.Body[DeploymentIdentity], error) {
		return respx.OK(ctx, DeploymentIdentity{Name: m.cfg.Name, Organization: m.cfg.Organization}), nil
	})
	return nil
}
