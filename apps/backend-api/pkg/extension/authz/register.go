package authz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/extension/policyx"
)

const (
	guardMetadataKey             = "authz.guard"
	tenantMemberMetadataKey      = "authz.tenant-member"
	authenticatedMetadataKey     = "authz.authenticated"
	sessionOnlyMetadataKey       = "authz.session-only"
	principalSelfMetadataKey     = "authz.principal-self"
	platformMetadataKey          = "authz.platform"
	operatingPlatformMetadataKey = "authz.operating-platform"
	publicMetadataKey            = "authz.public"
	ownedResourceMetadataKey     = "authz.owned-resource"
	accountViewDeniedMetadataKey = "authz.account-view-denied"
	GuardExtensionKey            = "x-authz-permission"
	DataFilterExtensionKey       = "x-authz-data-filter"
	TenantPath                   = "tenant_id"
	OperatingPlatformPath        = "platform_id"
	EntityPath                   = "entity_id"
	PrincipalPath                = "principal_id"
	BearerScheme                 = "bearerAuth"
	SessionScheme                = "sessionCookie"
	ClientBasicScheme            = "clientBasic"
	guidTextPattern              = `^[1-9][0-9]*$`
	guidTextMaxLength            = 19
)

// PermissionChecker is the narrow local authorization capability needed on the request path.
type PermissionChecker interface {
	Check(ctx context.Context, tenantID guid.ID, permission string, subject guid.ID) (bool, error)
	CheckPlatform(ctx context.Context, permission string, subject guid.ID) (bool, error)
}

// EntityPermissionChecker evaluates a permission at one tenant entity. It is
// implemented by the IAM owner and consumed by entity-scoped business routes.
type EntityPermissionChecker interface {
	CheckEntity(ctx context.Context, tenantID, entityID guid.ID, permission string, subject guid.ID) (bool, error)
}

type OperatingPlatformPermissionChecker interface {
	CheckOperatingPlatform(context.Context, guid.ID, string, guid.ID) (bool, error)
}

type OperatingPlatformMembershipChecker interface {
	IsOperatingPlatformMemberActive(context.Context, guid.ID, guid.ID) (bool, error)
}

// EntityDataPermissionChecker supplies filters for one active tenant entity.
// Missing/inactive entities or memberships and unknown/non-data permissions
// yield no allowed scopes. An explicit entity deny overrides owner/department
// grants. Callers must enforce the returned constraints on business rows.
type EntityDataPermissionChecker interface {
	EntityConstraint(ctx context.Context, tenantID, entityID guid.ID, permission string, subject guid.ID) (DataConstraint, error)
}

type TokenVerifier interface {
	Authenticate(context.Context, string, string) (*authn.Claims, error)
}

type csrfValidator interface {
	ValidateCSRF(method, origin, cookieHeader, authorization string) error
}

type MembershipChecker interface {
	IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error)
}

// ResourceOwner identifies the authorization boundary of a globally addressed
// resource. EntityID is zero for resources authorized at tenant scope.
type ResourceOwner struct {
	ScopeType   string
	TenantID    guid.ID
	EntityID    guid.ID
	PrincipalID guid.ID
}

// ResourceOwnerResolver resolves a globally unique resource ID to its trusted
// authorization boundary. found is false when the resource does not exist.
// Implementations belong to the module that owns the resource and its storage.
type ResourceOwnerResolver func(context.Context, guid.ID) (owner ResourceOwner, found bool, err error)

type resourceOwnerContextKey struct{}

// ResolvedResourceOwner exposes only the owner verified by owned-route middleware.
func ResolvedResourceOwner(ctx context.Context) ResourceOwner {
	owner, _ := ctx.Value(resourceOwnerContextKey{}).(ResourceOwner)
	return owner
}

type sessionCookieNamer interface {
	SessionCookieName() string
}

// Registrar binds route declarations, OpenAPI metadata, authentication, and
// local authorization from one call site.
type Registrar struct {
	registry        *Registry
	verifier        TokenVerifier
	checker         PermissionChecker
	members         MembershipChecker
	bound           bool
	declarationOnly bool
}

func NewRegistrar(registry *Registry, verifier TokenVerifier, checker PermissionChecker, members MembershipChecker) *Registrar {
	if registry == nil {
		panic("authz registrar requires a registry")
	}
	if verifier == nil || checker == nil || members == nil {
		panic("authz registrar requires verifier, permission checker, and membership checker")
	}
	return &Registrar{registry: registry, verifier: verifier, checker: checker, members: members, bound: true}
}

// NewUnboundRegistrar builds the stable registrar published by a module during
// the provider phase. Bind must complete during service injection, before
// protocol registration can expose any guarded operation.
func NewUnboundRegistrar(registry *Registry) *Registrar {
	if registry == nil {
		panic("authz registrar requires a registry")
	}
	return &Registrar{registry: registry}
}

// Bind completes an unbound registrar exactly once. Wiring is
// single-threaded and finishes before HTTP registration, so runtime requests
// only observe fully bound collaborators and all optional verifier capabilities.
func (r *Registrar) Bind(verifier TokenVerifier, checker PermissionChecker, members MembershipChecker) error {
	if r == nil {
		return errors.New("authz registrar is nil")
	}
	if verifier == nil || checker == nil || members == nil {
		return errors.New("authz registrar verifier, permission checker, and membership checker are required")
	}
	if r.bound {
		return errors.New("authz registrar is already bound")
	}
	r.verifier, r.checker, r.members = verifier, checker, members
	r.bound = true
	return nil
}

// NewAuthenticationOnlyRegistrar builds a registrar for authenticated routes
// that do not perform tenant, entity, or platform authorization.
func NewAuthenticationOnlyRegistrar(verifier TokenVerifier) *Registrar {
	if verifier == nil {
		panic("authz authentication-only registrar requires verifier")
	}
	return &Registrar{verifier: verifier, bound: true}
}

// NewDeclarationOnlyRegistrar creates a registrar that records Guard metadata
// without attaching enforcement. It is intentionally explicit and is only for
// tests and static route validation.
func NewDeclarationOnlyRegistrar(registry *Registry) *Registrar {
	if registry == nil {
		panic("authz declaration registrar requires a registry")
	}
	return &Registrar{registry: registry, declarationOnly: true}
}

func (r *Registrar) Registry() *Registry {
	return r.registry
}

func (r *Registrar) IsDeclarationOnly() bool {
	return r != nil && r.declarationOnly
}

// Register declares and registers one guarded Huma operation.
func Register[I, O any](r *Registrar, api huma.API, op huma.Operation, guard Guard, handler func(context.Context, *I) (*O, error)) {
	if r == nil {
		panic("authz registrar is nil")
	}
	r.prepare(api, &op, guard)
	prepareTenantPath(&op)
	requireResourceTag(&op)
	huma.Register(api, op, handler)
}

// RegisterEntity registers a guarded business operation whose selected entity
// is verified by IAM before the handler receives trusted snowflake claims.
func RegisterEntity[I, O any](r *Registrar, api huma.API, op huma.Operation, guard Guard, handler func(context.Context, *I) (*O, error)) {
	if r == nil {
		panic("authz registrar is nil")
	}
	if r.checker != nil {
		if _, ok := r.checker.(EntityPermissionChecker); !ok && !guard.FilterData {
			panic("entity operation requires entity permission checker")
		}
		if _, ok := r.checker.(EntityDataPermissionChecker); !ok && guard.FilterData {
			panic("data-filtered operation requires entity data permission checker")
		}
	}
	r.prepare(api, &op, guard)
	prepareEntityPath(&op)
	requireResourceTag(&op)
	if r.verifier != nil {
		// Replace the tenant-only guard installed by prepare with the entity guard.
		op.Middlewares[len(op.Middlewares)-1] = r.entityMiddleware(api, guard)
	}
	huma.Register(api, op, handler)
}

// RegisterOwned registers an existing globally addressed resource or one of
// its direct subresources. The owning module resolves the resource ID; the
// registrar then authenticates and authorizes against the trusted owner.
func RegisterOwned[I, O any](r *Registrar, api huma.API, op huma.Operation, resourceParam string, resolve ResourceOwnerResolver, guard Guard, handler func(context.Context, *I) (*O, error)) {
	if r == nil {
		panic("authz registrar is nil")
	}
	if resourceParam == "" || resolve == nil {
		panic("owned resource operation requires resource parameter and owner resolver")
	}
	requireResourceTag(&op)
	if r.checker != nil {
		if _, ok := r.checker.(EntityDataPermissionChecker); !ok && guard.FilterData {
			panic("data-filtered operation requires entity data permission checker")
		}
	}
	r.prepare(api, &op, guard)
	op.Metadata[ownedResourceMetadataKey] = true
	op.Errors = appendUniqueStatus(op.Errors, http.StatusNotFound)
	op.Parameters = append(op.Parameters, pathSelector(resourceParam, "Resource identifier"))
	if r.verifier != nil {
		op.Middlewares[len(op.Middlewares)-1] = r.ownedResourceMiddleware(api, resourceParam, resolve, &guard)
	}
	huma.Register(api, op, handler)
}

// RegisterOwnedTenantMember registers a globally addressed self-service
// resource whose owner tenant must contain the caller, without inventing an
// administrative permission for the operation.
func RegisterOwnedTenantMember[I, O any](r *Registrar, api huma.API, op huma.Operation, resourceParam string, resolve ResourceOwnerResolver, handler func(context.Context, *I) (*O, error)) {
	if r == nil {
		panic("authz registrar is nil")
	}
	if resourceParam == "" || resolve == nil {
		panic("owned tenant member operation requires resource parameter and owner resolver")
	}
	requireResourceTag(&op)
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[tenantMemberMetadataKey] = true
	op.Metadata[ownedResourceMetadataKey] = true
	r.prepareAuthenticated(api, &op, true)
	op.Errors = appendUniqueStatus(op.Errors, http.StatusNotFound)
	op.Parameters = append(op.Parameters, pathSelector(resourceParam, "Resource identifier"))
	if r.verifier != nil {
		op.Middlewares = append(op.Middlewares, r.ownedResourceMiddleware(api, resourceParam, resolve, nil))
	}
	huma.Register(api, op, handler)
}

// RegisterEntityOrAuthenticated registers one resource URI with two explicit
// contexts: both tenant/entity selectors use the ordinary entity guard; no
// selectors uses authenticated self-service. A partial selector never falls
// back to the self-service context.
func RegisterEntityOrAuthenticated[I, O any](r *Registrar, api huma.API, op huma.Operation, guard Guard, handler func(context.Context, *I) (*O, error)) {
	if r == nil {
		panic("authz registrar is nil")
	}
	if r.checker != nil {
		if _, ok := r.checker.(EntityPermissionChecker); !ok && !guard.FilterData {
			panic("entity operation requires entity permission checker")
		}
		if _, ok := r.checker.(EntityDataPermissionChecker); !ok && guard.FilterData {
			panic("data-filtered operation requires entity data permission checker")
		}
	}
	// The self twin is the same resource reached without a tenant selector,
	// so it belongs to the same published section. Forcing it into a "me"
	// section split one resource across two places in the document.
	self := op
	self.OperationID += "-self"
	self.Path = selfPath(self.Path)
	RegisterPrincipalAuthenticated(r, api, self, handler)
	r.prepare(api, &op, guard)
	prepareEntityPath(&op)
	requireResourceTag(&op)
	if r.verifier != nil {
		op.Middlewares[len(op.Middlewares)-1] = r.entityMiddleware(api, guard)
	}
	huma.Register(api, op, handler)
}

// RegisterTenantEntityOrAuthenticated exposes one resource shape in personal,
// tenant, and entity contexts. Each URI has exactly one authorization model.
func RegisterTenantEntityOrAuthenticated[I, O any](r *Registrar, api huma.API, op huma.Operation, guard Guard, handler func(context.Context, *I) (*O, error)) {
	// The self twin is the same resource reached without a tenant selector,
	// so it belongs to the same published section. Forcing it into a "me"
	// section split one resource across two places in the document.
	self := op
	self.OperationID += "-self"
	self.Path = selfPath(self.Path)
	RegisterPrincipalAuthenticated(r, api, self, handler)

	tenant := op
	tenant.OperationID += "-tenant"
	Register(r, api, tenant, guard, handler)

	RegisterEntity(r, api, op, guard, handler)
}

// RegisterEntityAdapter registers an entity-authorized operation that needs
// direct access to the Huma transport context, such as WebSocket upgrades or
// streaming protocols. It executes the same global and entity authorization
// middleware chain as typed Huma handlers before invoking the raw handler.
func RegisterEntityAdapter(r *Registrar, api huma.API, op huma.Operation, guard Guard, handler func(huma.Context)) {
	if r == nil || api == nil || handler == nil {
		panic("entity adapter operation requires registrar, api, and handler")
	}
	if guard.FilterData {
		panic("data filtering requires a typed entity operation")
	}
	if r.checker != nil {
		if _, ok := r.checker.(EntityPermissionChecker); !ok {
			panic("entity operation requires entity permission checker")
		}
	}
	r.prepare(api, &op, guard)
	prepareEntityPath(&op)
	requireResourceTag(&op)
	if r.verifier != nil {
		op.Middlewares[len(op.Middlewares)-1] = r.entityMiddleware(api, guard)
	}
	api.OpenAPI().AddOperation(&op)
	api.Adapter().Handle(&op, api.Middlewares().Handler(op.Middlewares.Handler(handler)))
}

// RegisterTenantMember registers a self-service operation available to every
// authenticated active member of the selected tenant. The handler remains
// tenant scoped, but does not require an administrative permission.
func RegisterTenantMember[I, O any](r *Registrar, api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	if r == nil {
		panic("authz registrar is nil")
	}
	if op.OperationID == "" {
		panic("tenant member operation requires operation id")
	}
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[tenantMemberMetadataKey] = true
	r.prepareAuthenticated(api, &op, true)
	prepareTenantPath(&op)
	requireResourceTag(&op)
	if r.verifier != nil {
		op.Middlewares = append(op.Middlewares, r.middleware(api, nil))
	}
	preparePrincipalScope(api, &op)
	huma.Register(api, op, handler)
}

// RegisterSessionAuthenticated requires a browser cookie for personal security
// operations. Both runtime enforcement and OpenAPI exclude bearer credentials.
func RegisterSessionAuthenticated[I, O any](r *Registrar, api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[sessionOnlyMetadataKey] = true
	RegisterAuthenticated(r, api, op, handler)
}

// RegisterAuthenticated declares an authenticated operation that does NOT bind a
// tenant (no tenant resource context or membership check). Claims are injected so
// the handler can resolve the caller. Use for personal/cross-tenant queries like
// "my tenants".
func RegisterAuthenticated[I, O any](r *Registrar, api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	if r == nil {
		panic("authz registrar is nil")
	}
	if op.OperationID == "" {
		panic("authenticated operation requires operation id")
	}
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[authenticatedMetadataKey] = true
	r.prepareAuthenticated(api, &op, false)
	if r.verifier != nil {
		op.Middlewares = append(op.Middlewares, r.authenticatedOnly(api))
	}
	preparePrincipalScope(api, &op)
	huma.Register(api, op, handler)
}

// Authenticated principal mounts address the caller explicitly, without granting
// administrative access to another principal. Run after the existing auth guard.
func preparePrincipalScope(api huma.API, op *huma.Operation) {
	if scoped, _ := op.Metadata[principalSelfMetadataKey].(bool); !scoped {
		return
	}
	op.Parameters = append(op.Parameters, pathSelector(PrincipalPath, "Authenticated principal identifier"))
	op.Errors = appendUniqueStatus(op.Errors, http.StatusNotFound, http.StatusUnprocessableEntity)
	op.Middlewares = append(op.Middlewares, func(ctx huma.Context, next func(huma.Context)) {
		claims, _ := authn.FromContext(ctx.Context())
		if err := PrincipalScopeError(claims, ctx.Param(PrincipalPath)); err != nil {
			_ = huma.WriteErr(api, ctx, err.GetStatus(), err.Error())
			return
		}
		next(ctx)
	})
}

// RegisterPrincipalAuthenticated explicitly restricts a principal mount to the caller.
func RegisterPrincipalAuthenticated[I, O any](r *Registrar, api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	markPrincipalScope(&op)
	RegisterAuthenticated(r, api, op, handler)
}

// RegisterPrincipalSessionAuthenticated restricts a principal mount to its browser session owner.
func RegisterPrincipalSessionAuthenticated[I, O any](r *Registrar, api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	markPrincipalScope(&op)
	RegisterSessionAuthenticated(r, api, op, handler)
}

// RegisterPrincipalTenantMember restricts a tenant member mount to the caller.
func RegisterPrincipalTenantMember[I, O any](r *Registrar, api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	markPrincipalScope(&op)
	RegisterTenantMember(r, api, op, handler)
}

func markPrincipalScope(op *huma.Operation) {
	if !strings.Contains(op.Path, "/principals/{principal_id}/") {
		panic("principal self operation requires an explicit principal path")
	}
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[principalSelfMetadataKey] = true
}

// PrincipalScopeError validates an explicit self-service principal address.
// Specialized authentication flows can reuse it with their verified claims.
func PrincipalScopeError(claims *authn.Claims, text string) huma.StatusError {
	if claims == nil || claims.PrincipalID.Zero() {
		return huma.Error401Unauthorized(respx.Unauthorized)
	}
	id, err := guid.Parse(text)
	if err != nil || id.Zero() || id.String() != text {
		return huma.Error422UnprocessableEntity("invalid_principal_id")
	}
	if id != claims.PrincipalID {
		return huma.Error404NotFound(respx.NotFound)
	}
	return nil
}

// authenticatedOnly verifies the session and injects claims without requiring a
// tenant selector or membership.
func (r *Registrar) authenticatedOnly(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		claims, ok := r.authenticate(api, ctx)
		if !ok {
			return
		}
		if self, _ := ctx.Operation().Metadata[principalSelfMetadataKey].(bool); self {
			owner := ResourceOwner{PrincipalID: claims.PrincipalID}
			if strings.Contains(ctx.Operation().Path, "/tenants/{tenant_id}/entities/{entity_id}/") {
				var err error
				owner.TenantID, err = guid.Parse(ctx.Param(TenantPath))
				if err != nil || owner.TenantID.Zero() {
					_ = huma.WriteErr(api, ctx, http.StatusUnprocessableEntity, "invalid_tenant_id")
					return
				}
				owner.EntityID, err = guid.Parse(ctx.Param(EntityPath))
				if err != nil || owner.EntityID.Zero() {
					_ = huma.WriteErr(api, ctx, http.StatusUnprocessableEntity, "invalid_entity_id")
					return
				}
			}
			if !allowAccountViewScope(api, ctx, claims, owner, false) {
				return
			}
		}
		if claims.AccountView != nil && ctx.Method() != http.MethodGet && ctx.Method() != http.MethodHead {
			if !allowAccountViewScope(api, ctx, claims, ResourceOwner{PrincipalID: claims.PrincipalID}, false) {
				return
			}
		}
		trusted := policyx.WithTrustedContext(ctx.Context(), policyx.TrustedContext{
			ACR: claims.ACR, AMR: claims.AMR, ClientID: claims.ClientID, NetworkZone: claims.NetworkZone,
		})
		next(huma.WithContext(ctx, authn.WithClaims(trusted, claims)))
	}
}

// RegisterPlatform declares an authenticated platform operation. Platform
// routes never accept a tenant selector and authorize against platform grants.
func RegisterPlatform[I, O any](r *Registrar, api huma.API, op huma.Operation, guard Guard, handler func(context.Context, *I) (*O, error)) {
	if r == nil {
		panic("authz registrar is nil")
	}
	if guard.FilterData {
		panic("data filtering requires a typed entity operation")
	}
	if op.OperationID == "" {
		panic("platform operation requires operation id")
	}
	action, ok := r.registry.Find(guard.Code())
	if !ok || action.Scope != "platform" {
		panic("platform operation uses non-platform permission: " + guard.Code())
	}
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[guardMetadataKey] = guard.Code()
	op.Metadata[platformMetadataKey] = true
	if op.Extensions == nil {
		op.Extensions = map[string]any{}
	}
	op.Extensions[GuardExtensionKey] = guard.Code()
	r.prepareAuthenticated(api, &op, false)
	if r.verifier != nil {
		op.Middlewares = append(op.Middlewares, r.platformMiddleware(api, guard))
	}
	huma.Register(api, op, handler)
}

// RegisterOperatingPlatform authorizes inside one independently operated root.
func RegisterOperatingPlatform[I, O any](r *Registrar, api huma.API, op huma.Operation, guard Guard, handler func(context.Context, *I) (*O, error)) {
	if r == nil || r.registry == nil {
		panic("operating platform operation requires registrar")
	}
	action, ok := r.registry.Find(guard.Code())
	if !ok || !IsOperatingPlatformPermission(action) || guard.FilterData {
		panic("operating platform operation uses unsupported permission: " + guard.Code())
	}
	if op.OperationID == "" {
		panic("operating platform operation requires operation id")
	}
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[guardMetadataKey] = guard.Code()
	op.Metadata[operatingPlatformMetadataKey] = true
	if op.Extensions == nil {
		op.Extensions = map[string]any{}
	}
	op.Extensions[GuardExtensionKey] = guard.Code()
	r.prepareAuthenticated(api, &op, false)
	op.Path = "/operating-platforms/{" + OperatingPlatformPath + "}" + op.Path
	op.Parameters = append(op.Parameters, pathSelector(OperatingPlatformPath, "Operating platform identifier"))
	requireResourceTag(&op)
	if r.verifier != nil {
		checker, checkerOK := r.checker.(OperatingPlatformPermissionChecker)
		members, membersOK := r.members.(OperatingPlatformMembershipChecker)
		if !checkerOK || !membersOK {
			panic("operating platform operation requires scoped authorization capabilities")
		}
		op.Middlewares = append(op.Middlewares, r.operatingPlatformMiddleware(api, guard, checker, members))
	}
	huma.Register(api, op, handler)
}

func RegisterOperatingPlatformMember[I, O any](r *Registrar, api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	if r == nil || op.OperationID == "" {
		panic("operating platform member operation requires registrar and operation id")
	}
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[authenticatedMetadataKey] = true
	op.Metadata[operatingPlatformMetadataKey] = true
	r.prepareAuthenticated(api, &op, false)
	op.Path = "/operating-platforms/{" + OperatingPlatformPath + "}" + op.Path
	op.Parameters = append(op.Parameters, pathSelector(OperatingPlatformPath, "Operating platform identifier"))
	requireResourceTag(&op)
	if r.verifier != nil {
		checker, checkerOK := r.checker.(OperatingPlatformPermissionChecker)
		members, membersOK := r.members.(OperatingPlatformMembershipChecker)
		if !checkerOK || !membersOK {
			panic("operating platform member operation requires scoped capabilities")
		}
		op.Middlewares = append(op.Middlewares, r.operatingPlatformMiddleware(api, Guard{}, checker, members))
	}
	huma.Register(api, op, handler)
}

// RegisterPublic registers an operation that intentionally bypasses the
// authorization Guard gateway. It may still perform authentication in its handler.
func RegisterPublic[I, O any](api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	Public(&op)
	ensureDefaultSecuritySchemes(api)
	huma.Register(api, op, handler)
}

func (r *Registrar) prepare(api huma.API, op *huma.Operation, guard Guard) {
	if !r.declarationOnly && (!r.bound || r.checker == nil || r.members == nil) {
		panic("authz registrar is not bound")
	}
	if op.OperationID == "" {
		panic("guarded operation requires operation id")
	}
	action, ok := r.registry.Find(guard.Code())
	if !ok {
		panic("guarded operation uses unknown permission: " + guard.Code())
	}
	// The mirror of the check RegisterPlatform already makes. A platform-scope
	// action guarding a tenant route is not a stricter route; it is a different
	// one. The middleware here authorizes inside the caller's tenant, so the
	// permission is looked for in tenant grants -- where a platform-only action
	// is never granted. The route then answers only to whoever happens to hold
	// it, and the tenant administrator loses an operation on their own tenant
	// while the declaration still reads as if it were guarded correctly.
	if action.Scope == "platform" || action.Scope == "operating_platform" {
		panic("tenant operation uses platform permission: " + guard.Code())
	}
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[guardMetadataKey] = guard.Code()
	if op.Extensions == nil {
		op.Extensions = map[string]any{}
	}
	op.Extensions[GuardExtensionKey] = guard.Code()
	r.prepareAuthenticated(api, op, true)
	if guard.FilterData {
		if !action.DataScoped {
			panic("data-filtered operation uses non-data permission: " + guard.Code())
		}
		op.Extensions[DataFilterExtensionKey] = true
	}

	if r.verifier != nil {
		op.Middlewares = append(op.Middlewares, r.middleware(api, &guard))
	}
}

func (r *Registrar) prepareAuthenticated(api huma.API, op *huma.Operation, tenantScoped bool) {
	if !r.declarationOnly && (!r.bound || r.verifier == nil) {
		panic("authz registrar is not bound")
	}
	op.Errors = appendUniqueStatus(op.Errors, http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable)
	op.Security = BearerSecurity()
	if r.verifier == nil {
		op.Security = UserSecurity()
	}
	if provider, ok := r.verifier.(sessionCookieNamer); ok && provider.SessionCookieName() != "" {
		op.Security = UserSecurity()
	}
	if sessionOnly, _ := op.Metadata[sessionOnlyMetadataKey].(bool); sessionOnly {
		op.Security = []map[string][]string{{SessionScheme: {}}}
	}
	EnsureSecuritySchemes(api, r.SessionCookieName())
	_ = tenantScoped
}

func tenantPath(path string) string {
	if path == "" || path == "/" {
		return "/tenants/{" + TenantPath + "}"
	}
	return "/tenants/{" + TenantPath + "}" + path
}

func entityPath(path string) string {
	return "/tenants/{" + TenantPath + "}/entities/{" + EntityPath + "}" + path
}

func selfPath(path string) string {
	if strings.HasPrefix(path, "/principals/{principal_id}/") {
		return path
	}
	return "/principals/{principal_id}" + path
}

func pathSelector(name, description string) *huma.Param {
	return &huma.Param{Name: name, In: "path", Description: description, Required: true,
		Schema: &huma.Schema{Type: huma.TypeString, Pattern: guidTextPattern, MaxLength: intPointer(guidTextMaxLength)}}
}

// requireResourceTag makes the Swagger group an explicit property of the
// operation instead of a value the registrar infers from the URI it is about
// to build, so that moving an operation between paths cannot silently move it
// between published sections. Exactly one tag: none leaves the operation out
// of every section, and a second would publish it twice with no owning one.
func requireResourceTag(op *huma.Operation) {
	if len(op.Tags) != 1 {
		panic("operation requires exactly one explicit resource tag")
	}
}

func prepareTenantPath(op *huma.Operation) {
	op.Path = tenantPath(op.Path)
	op.Parameters = append(op.Parameters, pathSelector(TenantPath, "Tenant resource identifier"))
}

func prepareEntityPath(op *huma.Operation) {
	op.Path = entityPath(op.Path)
	op.Parameters = append(op.Parameters,
		pathSelector(TenantPath, "Tenant resource identifier"),
		pathSelector(EntityPath, "Entity resource identifier"),
	)
}

// SessionCookieName reports the browser session cookie this registrar
// authenticator actually reads. It falls back to the package default only
// when no authenticator is bound or the bound one runs no browser sessions,
// which is also the only case where the default is the honest answer.
func (r *Registrar) SessionCookieName() string {
	if r != nil {
		if provider, ok := r.verifier.(sessionCookieNamer); ok && provider.SessionCookieName() != "" {
			return provider.SessionCookieName()
		}
	}
	return authn.DefaultSessionCookieName
}

// EnsureSecuritySchemes declares the authentication mechanisms used across the API.
func EnsureSecuritySchemes(api huma.API, cookieName string) {
	components := api.OpenAPI().Components
	if components.SecuritySchemes == nil {
		components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	components.SecuritySchemes[BearerScheme] = &huma.SecurityScheme{Type: "http", Scheme: "bearer", BearerFormat: "JWT"}
	components.SecuritySchemes[SessionScheme] = &huma.SecurityScheme{Type: "apiKey", In: "cookie", Name: cookieName}
	components.SecuritySchemes[ClientBasicScheme] = &huma.SecurityScheme{Type: "http", Scheme: "basic"}
}

func ensureDefaultSecuritySchemes(api huma.API) {
	components := api.OpenAPI().Components
	if components.SecuritySchemes == nil {
		components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	components.SecuritySchemes[BearerScheme] = &huma.SecurityScheme{Type: "http", Scheme: "bearer", BearerFormat: "JWT"}
	if _, exists := components.SecuritySchemes[SessionScheme]; !exists {
		components.SecuritySchemes[SessionScheme] = &huma.SecurityScheme{Type: "apiKey", In: "cookie", Name: authn.DefaultSessionCookieName}
	}
	components.SecuritySchemes[ClientBasicScheme] = &huma.SecurityScheme{Type: "http", Scheme: "basic"}
}

// UserSecurity documents endpoints whose authenticator accepts either a bearer
// token or the configured browser session cookie.
func UserSecurity() []map[string][]string {
	return []map[string][]string{{BearerScheme: {}}, {SessionScheme: {}}}
}

func BearerSecurity() []map[string][]string {
	return []map[string][]string{{BearerScheme: {}}}
}

func ClientSecurity() []map[string][]string {
	return []map[string][]string{{ClientBasicScheme: {}}}
}

func intPointer(value int) *int { return &value }

func (r *Registrar) authenticate(api huma.API, ctx huma.Context) (*authn.Claims, bool) {
	if sessionOnly, _ := ctx.Operation().Metadata[sessionOnlyMetadataKey].(bool); sessionOnly && ctx.Header("Authorization") != "" {
		_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, respx.Unauthorized)
		return nil, false
	}
	if validator, ok := r.verifier.(csrfValidator); ok {
		if err := validator.ValidateCSRF(ctx.Method(), ctx.Header("Origin"), ctx.Header("Cookie"), ctx.Header("Authorization")); err != nil {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.CSRFRejected)
			return nil, false
		}
	}
	claims, err := r.verifier.Authenticate(ctx.Context(), ctx.Header("Authorization"), ctx.Header("Cookie"))
	if err == nil {
		if claims != nil && claims.AccountView != nil {
			if denied, _ := ctx.Operation().Metadata[accountViewDeniedMetadataKey].(bool); denied {
				_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
				return nil, false
			}
			if !claims.AccountView.Valid() || claims.PrincipalID.Zero() {
				_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, respx.Unauthorized)
				return nil, false
			}
			if claims.AccountView.ReadOnly && ctx.Method() != http.MethodGet && ctx.Method() != http.MethodHead {
				_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
				return nil, false
			}
		}
		return claims, true
	}
	slog.Debug("authn token rejected", "operation", ctx.Operation().OperationID, "err", err)
	if errors.Is(err, authn.ErrUnavailable) {
		_ = huma.WriteErr(api, ctx, http.StatusServiceUnavailable, respx.AuthenticationUnavailable)
		return nil, false
	}
	_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, respx.Unauthorized)
	return nil, false
}

// authorizationFailed reports an authorization decision that could not be
// reached, and separates a caller who went away from a dependency that broke.
//
// A canceled request context means the browser navigated or closed the
// connection before the decision came back. Nobody reads the response, so the
// status is academic, but logging it as an error is not: the acceptance run
// that found this produced ERROR lines for ordinary page navigation, and real
// failures have to be picked out from among them.
func authorizationFailed(api huma.API, ctx huma.Context, message string, attrs ...any) {
	if requestContext := ctx.Context(); requestContext != nil && requestContext.Err() != nil {
		slog.Debug("authorization abandoned by the caller",
			append([]any{"operation", ctx.Operation().OperationID, "err", requestContext.Err()}, attrs...)...)
		_ = huma.WriteErr(api, ctx, http.StatusServiceUnavailable, respx.AuthorizationUnavailable)
		return
	}
	slog.Error(message, append([]any{"operation", ctx.Operation().OperationID}, attrs...)...)
	_ = huma.WriteErr(api, ctx, http.StatusServiceUnavailable, respx.AuthorizationUnavailable)
}

func (r *Registrar) middleware(api huma.API, guard *Guard) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if guard != nil {
			tenantID, _ := guid.Parse(ctx.Param(TenantPath))
			ctx = huma.WithContext(ctx, authn.WithPersonalTokenScope(ctx.Context(), tenantID, guard.Code()))
		}
		claims, ok := r.authenticate(api, ctx)
		if !ok {
			return
		}
		trusted := policyx.TrustedContext{
			ACR: claims.ACR, AMR: claims.AMR, ClientID: claims.ClientID, NetworkZone: claims.NetworkZone,
		}
		requestContext := policyx.WithTrustedContext(ctx.Context(), trusted)
		tenantText := ctx.Param(TenantPath)
		tenantID, tenantErr := guid.Parse(tenantText)
		if tenantErr != nil || tenantID.Zero() || claims.PrincipalID.Zero() {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
			return
		}
		if !allowAccountViewScope(api, ctx, claims, ResourceOwner{TenantID: tenantID}, false) {
			return
		}
		active, err := r.members.IsMemberActive(requestContext, tenantID, claims.PrincipalID)
		if err != nil {
			authorizationFailed(api, ctx, "tenant membership check failed", "err", err)
			return
		}
		if !active {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.InactiveTenantMembership)
			return
		}
		if guard != nil {
			allowed, err := r.checker.Check(requestContext, tenantID, guard.Code(), claims.PrincipalID)
			if err != nil {
				authorizationFailed(api, ctx, "authz check failed", "permission", guard.Code(), "err", err)
				return
			}
			if !allowed {
				_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
				return
			}
		}
		trustedClaims := *claims
		trustedClaims.TenantID = tenantID
		trustedClaims.EntityID = 0
		next(huma.WithContext(ctx, authn.WithClaims(requestContext, &trustedClaims)))
	}
}

func (r *Registrar) entityMiddleware(api huma.API, guard Guard) func(huma.Context, func(huma.Context)) {
	return r.entityMiddlewareFor(api, guard, func(ctx huma.Context) (string, string) {
		return ctx.Param(TenantPath), ctx.Param(EntityPath)
	})
}

func (r *Registrar) ownedResourceMiddleware(api huma.API, resourceParam string, resolve ResourceOwnerResolver, guard *Guard) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		resourceID, parseErr := guid.Parse(ctx.Param(resourceParam))
		owner := ResourceOwner{}
		found := false
		var resolveErr error
		if parseErr == nil && !resourceID.Zero() {
			owner, found, resolveErr = resolve(ctx.Context(), resourceID)
		}
		permission := ""
		if guard != nil {
			permission = guard.Code()
		}
		ctx = huma.WithContext(ctx, authn.WithPersonalTokenScope(ctx.Context(), owner.TenantID, permission))
		claims, ok := r.authenticate(api, ctx)
		if !ok {
			return
		}
		if resolveErr != nil {
			authorizationFailed(api, ctx, "resource owner resolution failed", "err", resolveErr)
			return
		}
		if parseErr != nil || resourceID.Zero() {
			_ = huma.WriteErr(api, ctx, http.StatusUnprocessableEntity, respx.InvalidID)
			return
		}
		personalOwner := !owner.PrincipalID.Zero() && owner.TenantID.Zero() && owner.EntityID.Zero()
		tenantOwner := owner.PrincipalID.Zero() && !owner.TenantID.Zero()
		platformOwner := owner.ScopeType == "platform" && tenantOwner && owner.EntityID.Zero()
		systemOwner := owner.ScopeType == "system" && owner.TenantID.Zero() && owner.EntityID.Zero() && owner.PrincipalID.Zero()
		if !found || (!personalOwner && !tenantOwner && !systemOwner) || (owner.ScopeType != "" && owner.ScopeType != "tenant" && !platformOwner && !systemOwner) {
			_ = huma.WriteErr(api, ctx, http.StatusNotFound, respx.NotFound)
			return
		}
		principalID := claims.PrincipalID
		if principalID.Zero() {
			principalID, parseErr = guid.Parse(claims.Subject)
		}
		if parseErr != nil || principalID.Zero() {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
			return
		}
		if !allowAccountViewScope(api, ctx, claims, owner, systemOwner) {
			return
		}
		if systemOwner {
			if guard == nil {
				_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
				return
			}
			permission := "system_" + guard.Code()
			if action, exists := r.registry.Find(permission); !exists || action.Scope != "platform" {
				_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
				return
			}
			allowed, err := r.checker.CheckPlatform(ctx.Context(), permission, principalID)
			if err != nil {
				authorizationFailed(api, ctx, "system resource authorization failed", "err", err)
				return
			}
			if !allowed {
				_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
				return
			}
			trustedClaims := *claims
			trustedClaims.PrincipalID, trustedClaims.TenantID, trustedClaims.EntityID = principalID, 0, 0
			requestContext := context.WithValue(ctx.Context(), resourceOwnerContextKey{}, owner)
			next(huma.WithContext(ctx, authn.WithClaims(requestContext, &trustedClaims)))
			return
		}
		if personalOwner {
			if principalID != owner.PrincipalID {
				_ = huma.WriteErr(api, ctx, http.StatusNotFound, respx.NotFound)
				return
			}
			trustedClaims := *claims
			trustedClaims.TenantID = 0
			trustedClaims.EntityID = 0
			trustedClaims.PrincipalID = principalID
			next(huma.WithContext(ctx, authn.WithClaims(ctx.Context(), &trustedClaims)))
			return
		}
		requestContext := policyx.WithTrustedContext(ctx.Context(), policyx.TrustedContext{
			ACR: claims.ACR, AMR: claims.AMR, ClientID: claims.ClientID, NetworkZone: claims.NetworkZone,
		})
		var active bool
		var err error
		if platformOwner {
			members, ok := r.members.(OperatingPlatformMembershipChecker)
			if !ok {
				authorizationFailed(api, ctx, "operating platform membership capability unavailable")
				return
			}
			active, err = members.IsOperatingPlatformMemberActive(requestContext, owner.TenantID, principalID)
		} else {
			active, err = r.members.IsMemberActive(requestContext, owner.TenantID, principalID)
		}
		if err != nil {
			authorizationFailed(api, ctx, "resource membership check failed", "err", err)
			return
		}
		if !active {
			_ = huma.WriteErr(api, ctx, http.StatusNotFound, respx.NotFound)
			return
		}
		if guard != nil {
			var allowed bool
			if platformOwner {
				checker, ok := r.checker.(OperatingPlatformPermissionChecker)
				if !ok {
					authorizationFailed(api, ctx, "operating platform authorization capability unavailable")
					return
				}
				allowed, err = checker.CheckOperatingPlatform(requestContext, owner.TenantID, guard.Code(), principalID)
			} else if owner.EntityID.Zero() {
				allowed, err = r.checker.Check(requestContext, owner.TenantID, guard.Code(), principalID)
			} else {
				allowed, err = r.checkEntityAccess(requestContext, owner.TenantID, owner.EntityID, *guard, principalID)
			}
			if err != nil {
				authorizationFailed(api, ctx, "resource authz check failed", "permission", guard.Code(), "err", err)
				return
			}
			if !allowed {
				_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
				return
			}
		}
		trustedClaims := *claims
		trustedClaims.TenantID = owner.TenantID
		trustedClaims.EntityID = owner.EntityID
		trustedClaims.PrincipalID = principalID
		next(huma.WithContext(ctx, authn.WithClaims(requestContext, &trustedClaims)))
	}
}

func (r *Registrar) entityMiddlewareFor(api huma.API, guard Guard, selectors func(huma.Context) (string, string)) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		tenantText, entityText := selectors(ctx)
		tenantID, tenantErr := guid.Parse(tenantText)
		ctx = huma.WithContext(ctx, authn.WithPersonalTokenScope(ctx.Context(), tenantID, guard.Code()))
		claims, ok := r.authenticate(api, ctx)
		if !ok {
			return
		}
		var err error
		entityID, entityErr := guid.Parse(entityText)
		principalID := claims.PrincipalID
		if principalID.Zero() {
			principalID, err = guid.Parse(claims.Subject)
		}
		if tenantErr != nil || entityErr != nil || err != nil || tenantID.Zero() || entityID.Zero() || principalID.Zero() {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
			return
		}
		if !allowAccountViewScope(api, ctx, claims, ResourceOwner{TenantID: tenantID, EntityID: entityID}, false) {
			return
		}
		requestContext := policyx.WithTrustedContext(ctx.Context(), policyx.TrustedContext{
			ACR: claims.ACR, AMR: claims.AMR, ClientID: claims.ClientID, NetworkZone: claims.NetworkZone,
		})
		active, err := r.members.IsMemberActive(requestContext, tenantID, principalID)
		if err != nil {
			authorizationFailed(api, ctx, "entity membership check failed", "tenant", tenantID, "entity", entityID, "err", err)
			return
		}
		if !active {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.InactiveTenantMembership)
			return
		}
		allowed, err := r.checkEntityAccess(requestContext, tenantID, entityID, guard, principalID)
		if err != nil {
			authorizationFailed(api, ctx, "entity authz check failed", "tenant", tenantID, "entity", entityID, "permission", guard.Code(), "err", err)
			return
		}
		if !allowed {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
			return
		}
		trustedClaims := *claims
		trustedClaims.TenantID = tenantID
		trustedClaims.EntityID = entityID
		trustedClaims.PrincipalID = principalID
		next(huma.WithContext(ctx, authn.WithClaims(requestContext, &trustedClaims)))
	}
}

func (r *Registrar) checkEntityAccess(ctx context.Context, tenantID, entityID guid.ID, guard Guard, principalID guid.ID) (bool, error) {
	if guard.FilterData {
		checker, ok := r.checker.(EntityDataPermissionChecker)
		if !ok {
			return false, errors.New("entity data permission checker unavailable")
		}
		constraint, err := checker.EntityConstraint(ctx, tenantID, entityID, guard.Code(), principalID)
		return constraint.AllowsEntityRows(entityID), err
	}
	checker, ok := r.checker.(EntityPermissionChecker)
	if !ok {
		return false, errors.New("entity permission checker unavailable")
	}
	return checker.CheckEntity(ctx, tenantID, entityID, guard.Code(), principalID)
}

func (r *Registrar) platformMiddleware(api huma.API, guard Guard) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		claims, ok := r.authenticate(api, ctx)
		if !ok {
			return
		}
		if !allowAccountViewScope(api, ctx, claims, ResourceOwner{}, true) {
			return
		}
		requestContext := policyx.WithTrustedContext(ctx.Context(), policyx.TrustedContext{
			ACR: claims.ACR, AMR: claims.AMR, ClientID: claims.ClientID, NetworkZone: claims.NetworkZone,
		})
		allowed, err := r.checker.CheckPlatform(requestContext, guard.Code(), claims.PrincipalID)
		if err != nil {
			authorizationFailed(api, ctx, "platform authz check failed", "permission", guard.Code(), "err", err)
			return
		}
		if !allowed {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
			return
		}
		next(huma.WithContext(ctx, authn.WithClaims(requestContext, claims)))
	}
}

func (r *Registrar) operatingPlatformMiddleware(api huma.API, guard Guard, checker OperatingPlatformPermissionChecker, members OperatingPlatformMembershipChecker) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		claims, ok := r.authenticate(api, ctx)
		if !ok {
			return
		}
		text := ctx.Param(OperatingPlatformPath)
		id, err := guid.Parse(text)
		if err != nil || id <= 0 || id.String() != text || claims.PrincipalID <= 0 {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
			return
		}
		if view := claims.AccountView; view != nil && (view.ScopeType != "platform" || view.ScopeID != id) {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
			return
		}
		requestContext := policyx.WithTrustedContext(ctx.Context(), policyx.TrustedContext{
			ACR: claims.ACR, AMR: claims.AMR, ClientID: claims.ClientID, NetworkZone: claims.NetworkZone,
		})
		active, err := members.IsOperatingPlatformMemberActive(requestContext, id, claims.PrincipalID)
		if err != nil {
			authorizationFailed(api, ctx, "operating platform membership check failed", "err", err)
			return
		}
		if !active {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
			return
		}
		allowed := true
		if guard.Resource != "" {
			allowed, err = checker.CheckOperatingPlatform(requestContext, id, guard.Code(), claims.PrincipalID)
		}
		if err != nil {
			authorizationFailed(api, ctx, "operating platform authorization failed", "err", err)
			return
		}
		if !allowed {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
			return
		}
		trusted := *claims
		trusted.TenantID, trusted.EntityID = id, 0
		next(huma.WithContext(ctx, authn.WithClaims(requestContext, &trusted)))
	}
}

func IsOperatingPlatformOperation(op *huma.Operation) bool {
	if op == nil || op.Metadata == nil {
		return false
	}
	value, _ := op.Metadata[operatingPlatformMetadataKey].(bool)
	return value
}

// Resolve the resource's real owner before comparing a view's immutable scope.
// Existing platform routes are deployment-global system operations.
func allowAccountViewScope(api huma.API, ctx huma.Context, claims *authn.Claims, owner ResourceOwner, system bool) bool {
	v := claims.AccountView
	if v == nil {
		return true
	}
	allowed := false
	switch v.ScopeType {
	case "system":
		allowed = system
	case "tenant":
		allowed = !system && owner.ScopeType != "platform" && owner.TenantID == v.ScopeID
	case "entity":
		allowed = !system && owner.ScopeType != "platform" && owner.EntityID == v.ScopeID
	case "platform":
		allowed = !system && owner.ScopeType == "platform" && owner.TenantID == v.ScopeID
	case "personal":
		allowed = !system && owner.TenantID.Zero() && owner.EntityID.Zero() && owner.PrincipalID == claims.PrincipalID && v.ScopeID == claims.PrincipalID
	}
	if !allowed {
		_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
	}
	return allowed
}

// Public marks an operation as intentionally outside the authorization Guard gateway.
// The route may still authenticate using another flow, such as /authn/me.
func Public(op *huma.Operation) {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[publicMetadataKey] = true
}

// DenyAccountView protects authentication callbacks and other operations that
// require the account holder's own authenticated session, including GET writes.
func DenyAccountView(op *huma.Operation) {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[accountViewDeniedMetadataKey] = true
}

func appendUniqueStatus(existing []int, values ...int) []int {
	for _, value := range values {
		found := false
		for _, current := range existing {
			if current == value {
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, value)
		}
	}
	return existing
}

func guardOf(op *huma.Operation) (string, bool) {
	if op == nil || op.Metadata == nil {
		return "", false
	}
	code, ok := op.Metadata[guardMetadataKey].(string)
	return code, ok && code != ""
}

func isPublic(op *huma.Operation) bool {
	if op == nil || op.Metadata == nil {
		return false
	}
	value, _ := op.Metadata[publicMetadataKey].(bool)
	return value
}

func isAuthenticated(op *huma.Operation) bool {
	value, _ := op.Metadata[authenticatedMetadataKey].(bool)
	return value
}

func isTenantMember(op *huma.Operation) bool {
	if op == nil || op.Metadata == nil {
		return false
	}
	value, _ := op.Metadata[tenantMemberMetadataKey].(bool)
	return value
}

// IsTenantMemberOperation reports whether an operation uses the authenticated
// active-member declaration. It is exposed for repository-wide OpenAPI gates.
func IsTenantMemberOperation(op *huma.Operation) bool {
	return isTenantMember(op)
}

// IsOwnedResourceOperation reports whether an operation resolves its trusted
// authorization boundary from a globally addressed resource ID.
func IsOwnedResourceOperation(op *huma.Operation) bool {
	if op == nil || op.Metadata == nil {
		return false
	}
	value, _ := op.Metadata[ownedResourceMetadataKey].(bool)
	return value
}

// IsAuthenticatedOperation reports whether an operation requires an
// authenticated principal without binding a tenant.
func IsAuthenticatedOperation(op *huma.Operation) bool {
	if op == nil || op.Metadata == nil {
		return false
	}
	return isAuthenticated(op)
}

// IsPublicOperation reports whether an operation intentionally bypasses the
// authorization Guard gateway.
func IsPublicOperation(op *huma.Operation) bool {
	return isPublic(op)
}

// OperationGuard reports the permission declared for a guarded operation.
func OperationGuard(op *huma.Operation) (string, bool) {
	return guardOf(op)
}

// IsPlatformOperation reports whether an operation uses platform authorization.
func IsPlatformOperation(op *huma.Operation) bool {
	if op == nil || op.Metadata == nil {
		return false
	}
	value, _ := op.Metadata[platformMetadataKey].(bool)
	return value
}

func invalidGuardMessage(op *huma.Operation, code string) string {
	return fmt.Sprintf("%s uses undeclared permission %q", op.OperationID, code)
}
