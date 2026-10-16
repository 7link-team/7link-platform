// Package authz holds the application's authorization catalog.
//
// The catalog is intentionally code-first: route guards, the management UI,
// menu binding, and the relational authorizer all read the same declarations
// so permission codes cannot drift between enforcement and administration.
package authz

import (
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strings"
)

var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Action.Scope values. They name the highest scope that owns a grant and are
// stored and published as-is (D89), so the literal "platform" is kept even though
// it denotes system-level authority: it is unrelated to the operating-platform
// organization kind (scope_kind / ScopeType "platform").
const (
	// ActionScopeSystem ("platform") marks system-level authority granted only
	// through platform grants and system roles, never by tenant or
	// operating-platform roles.
	ActionScopeSystem = "platform"
	// ActionScopeOperatingPlatform marks authority inside one independently
	// operated platform.
	ActionScopeOperatingPlatform = "operating_platform"
	// ActionScopeTenant marks authority inside one tenant.
	ActionScopeTenant = "tenant"
	// ActionScopeMerchant marks authority inside one merchant of a tenant.
	ActionScopeMerchant = "merchant"
)

// Action is one interface/menu/data permission exposed by the platform.
//
// Scope is one of the ActionScope* values: ActionScopeSystem ("platform",
// system-level authority), ActionScopeOperatingPlatform ("operating_platform"),
// ActionScopeTenant ("tenant") or ActionScopeMerchant ("merchant").
type Action struct {
	Resource         string   `json:"resource" doc:"resource name, e.g. store"`
	Verb             string   `json:"verb" doc:"action verb, e.g. view"`
	Code             string   `json:"code" doc:"stable permission code, resource_verb"`
	Scope            string   `json:"scope" doc:"highest scope that owns the grant"`
	Summary          string   `json:"summary" doc:"human-readable permission summary"`
	AllowedRelations []string `json:"allowed_relations" doc:"relationship grants that satisfy this action"`
	DataScoped       bool     `json:"data_scoped" doc:"true when object/data filtering also applies"`
	Menu             bool     `json:"menu" doc:"true when this action may drive menu visibility"`
}

// Guard is the compact value handlers attach to a route.
type Guard struct {
	Resource string
	Verb     string
	// CreationAttributes supplies code-owned facts for a new resource on a
	// tenant POST route. A non-nil map enables creation context: Type is Resource,
	// Owner is the authenticated principal, and ID is absent. Never populate
	// this declaration from request input. Ordinary routes leave it nil.
	CreationAttributes map[string]string
	// FilterData opts a typed entity route into row-scoped authorization. Its
	// repository must enforce EntityConstraint on every read and write.
	FilterData bool
}

// Code returns the canonical permission code for the guard.
func (g Guard) Code() string {
	return PermissionCode(g.Resource, g.Verb)
}

// PermissionCode joins resource and verb into the stable tenant permission
// name used by endpoint checks and role grants.
func PermissionCode(resource, verb string) string {
	return resource + "_" + verb
}

func validRelationshipRelation(value string) bool {
	return value == "owner" || value == "editor" || value == "viewer"
}

// Registry stores a validated, lookup-friendly action catalog.
type Registry struct {
	actions map[string]Action
	fields  map[string]Field
}

// NewRegistry builds a registry from actions. Codes are derived when omitted.
func NewRegistry(actions ...Action) (*Registry, error) {
	r := &Registry{actions: make(map[string]Action, len(actions))}
	for _, a := range actions {
		if err := r.Register(a); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// MustRegistry is for process startup declarations.
func MustRegistry(actions ...Action) *Registry {
	r, err := NewRegistry(actions...)
	if err != nil {
		panic(err)
	}
	return r
}

// Register adds one action after validating its stable code.
func (r *Registry) Register(a Action) error {
	if r.actions == nil {
		r.actions = map[string]Action{}
	}
	if a.Resource == "" || a.Verb == "" {
		return fmt.Errorf("authz action requires resource and verb")
	}
	expectedCode := PermissionCode(a.Resource, a.Verb)
	if a.Code == "" {
		a.Code = expectedCode
	} else if a.Code != expectedCode {
		return fmt.Errorf("authz code %q must equal canonical code %q", a.Code, expectedCode)
	}
	if !codePattern.MatchString(a.Resource) {
		return fmt.Errorf("invalid authz resource %q", a.Resource)
	}
	if !codePattern.MatchString(a.Verb) {
		return fmt.Errorf("invalid authz verb %q", a.Verb)
	}
	if strings.HasSuffix(a.Verb, "_role") {
		return fmt.Errorf("invalid authz verb %q: suffix _role is reserved for generated relations", a.Verb)
	}
	if !codePattern.MatchString(a.Code) {
		return fmt.Errorf("invalid authz code %q", a.Code)
	}
	seenRelations := map[string]bool{}
	for _, relation := range a.AllowedRelations {
		if !validRelationshipRelation(relation) || seenRelations[relation] {
			return fmt.Errorf("invalid or duplicate authz relation %q", relation)
		}
		seenRelations[relation] = true
	}
	sort.Strings(a.AllowedRelations)
	if _, exists := r.actions[a.Code]; exists {
		return fmt.Errorf("duplicate authz code %q", a.Code)
	}
	r.actions[a.Code] = a
	return nil
}

// Find returns an action by canonical permission code.
func (r *Registry) Find(code string) (Action, bool) {
	a, ok := r.actions[code]
	return a, ok
}

// MustFind returns an action or panics. It is suitable for route declarations.
func (r *Registry) MustFind(code string) Action {
	a, ok := r.Find(code)
	if !ok {
		panic("unknown authz code: " + code)
	}
	return a
}

// Contribute adds one module's actions and field declarations as a unit: either
// every declaration is valid and new, or the registry is left unchanged. It is
// the single path through which a module outside the IAM core catalog adds its
// permissions to the shared registry, and it runs during InjectServices, after
// the module has resolved the shared *Registrar and before any route is
// registered or request served.
func (r *Registry) Contribute(actions []Action, fields []Field) error {
	if r == nil {
		return fmt.Errorf("authz contribution requires a registry")
	}
	staged := &Registry{actions: maps.Clone(r.actions), fields: maps.Clone(r.fields)}
	for _, action := range actions {
		if err := staged.Register(action); err != nil {
			return err
		}
	}
	for _, field := range fields {
		if err := staged.RegisterField(field); err != nil {
			return err
		}
	}
	r.actions, r.fields = staged.actions, staged.fields
	return nil
}

// All returns actions sorted by code for stable API and schema output.
func (r *Registry) All() []Action {
	out := make([]Action, 0, len(r.actions))
	for _, a := range r.actions {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// DefaultRegistry is the IAM core catalog: platform, tenant, merchant, store,
// user, role, dept, and menu management. Business modules do not appear here;
// they add their own actions and fields to the shared registry through
// Contribute. Production builds it once, as the seed of the shared registry the
// IAM core module publishes; every other production consumer resolves that
// registry instead of building another one.
func DefaultRegistry() *Registry {
	r := MustRegistry(DefaultActions()...)
	for _, f := range DefaultFields() {
		if err := r.RegisterField(f); err != nil {
			panic(err)
		}
	}
	return r
}

// DefaultActions is deliberately small but complete enough for the first IAM
// screen: endpoint grants, data-scoped resources, and menu visibility all share
// these same codes.
func DefaultActions() []Action {
	return []Action{
		{Resource: "account_view", Verb: "create", Scope: "platform", Summary: "open a scoped account view"},
		// system_account covers every global account of every category, not
		// only the subset the "System accounts" menu (/system/accounts/system)
		// lists.
		{Resource: "system_account", Verb: "create", Scope: "platform", Summary: "create global accounts of any category"},
		{Resource: "system_account", Verb: "view", Scope: "platform", Summary: "view all global accounts, not only the System accounts menu"},
		{Resource: "system_account", Verb: "update", Scope: "platform", Summary: "update the profile of any global account"},
		{Resource: "system_account", Verb: "delete", Scope: "platform", Summary: "suspend any global account"},
		{Resource: "system_menu", Verb: "view", Scope: "platform", Summary: "view system menus", Menu: true},
		{Resource: "system_menu", Verb: "create", Scope: "platform", Summary: "create system menus"},
		{Resource: "system_menu", Verb: "update", Scope: "platform", Summary: "edit and move system menus"},
		{Resource: "system_menu", Verb: "delete", Scope: "platform", Summary: "delete system menus"},
		{Resource: "system_menu", Verb: "bind_permission", Scope: "platform", Summary: "bind system menu permissions"},
		{Resource: "system_role", Verb: "view", Scope: "platform", Summary: "view system roles", Menu: true},
		{Resource: "system_role", Verb: "create", Scope: "platform", Summary: "create system roles"},
		{Resource: "system_role", Verb: "update", Scope: "platform", Summary: "edit system roles"},
		{Resource: "system_role", Verb: "delete", Scope: "platform", Summary: "delete system roles"},
		{Resource: "system_role", Verb: "grant_permission", Scope: "platform", Summary: "manage system role permissions"},
		{Resource: "system_role", Verb: "manage_member", Scope: "platform", Summary: "assign system roles to accounts"},
		{Resource: "system_department", Verb: "view", Scope: "platform", Summary: "view system departments", Menu: true},
		{Resource: "system_department", Verb: "create", Scope: "platform", Summary: "create system departments"},
		{Resource: "system_department", Verb: "update", Scope: "platform", Summary: "edit system departments"},
		{Resource: "system_department", Verb: "delete", Scope: "platform", Summary: "delete system departments"},
		{Resource: "system_member", Verb: "view", Scope: "platform", Summary: "view system account memberships"},
		{Resource: "system_member", Verb: "create", Scope: "platform", Summary: "add system account memberships"},
		{Resource: "system_member", Verb: "update", Scope: "platform", Summary: "edit system account memberships"},
		{Resource: "system_audit_event", Verb: "view", Scope: "platform", Summary: "view system audit events and verify their chain", Menu: true},
		{Resource: "system_audit_event", Verb: "export", Scope: "platform", Summary: "export system audit events"},
		{Resource: "operating_platform", Verb: "administer", Scope: "operating_platform", Summary: "administer an operating platform"},
		{Resource: "operating_platform", Verb: "create", Scope: "platform", Summary: "create operating platforms"},
		{Resource: "operating_platform", Verb: "view", Scope: "platform", Summary: "view operating platforms"},
		{Resource: "operating_platform", Verb: "update", Scope: "platform", Summary: "update operating platforms"},
		{Resource: "operating_platform", Verb: "delete", Scope: "platform", Summary: "delete operating platforms"},
		{Resource: "platform_member", Verb: "create", Scope: "operating_platform", Summary: "attach existing principals to an operating platform"},
		{Resource: "platform_member", Verb: "view", Scope: "operating_platform", Summary: "view operating platform members", Menu: true},
		{Resource: "platform_member", Verb: "update", Scope: "operating_platform", Summary: "update operating platform memberships"},
		{Resource: "platform_member", Verb: "delete", Scope: "operating_platform", Summary: "disable operating platform memberships"},
		{Resource: "field_policy", Verb: "view", Scope: "tenant", Summary: "view field policies"},
		{Resource: "field_policy", Verb: "update", Scope: "tenant", Summary: "manage field read and write policies"},
		{Resource: "platform", Verb: "administer", Scope: "platform", Summary: "platform administration"},
		{Resource: "tenant", Verb: "create", Scope: "platform", Summary: "create tenants"},
		{Resource: "tenant", Verb: "view", Scope: "platform", Summary: "view tenants", DataScoped: true},
		{Resource: "tenant", Verb: "update", Scope: "platform", Summary: "update tenants", DataScoped: true},
		{Resource: "tenant", Verb: "delete", Scope: "platform", Summary: "delete tenants", DataScoped: true},
		{Resource: "tenant", Verb: "administer", Scope: "tenant", Summary: "tenant administration", DataScoped: true},

		{Resource: "entity", Verb: "create", Scope: "tenant", Summary: "create tenant entities", DataScoped: true},
		{Resource: "entity", Verb: "view", Scope: "tenant", Summary: "view tenant entities", AllowedRelations: []string{"owner", "editor", "viewer"}, DataScoped: true, Menu: true},
		{Resource: "entity", Verb: "update", Scope: "tenant", Summary: "update tenant entities", AllowedRelations: []string{"owner", "editor"}, DataScoped: true},
		{Resource: "entity", Verb: "delete", Scope: "tenant", Summary: "delete tenant entities", AllowedRelations: []string{"owner"}, DataScoped: true},
		{Resource: "entity", Verb: "manage_binding", Scope: "tenant", Summary: "manage entity role bindings", AllowedRelations: []string{"owner"}, DataScoped: true},

		{Resource: "merchant", Verb: "create", Scope: "tenant", Summary: "create merchants", DataScoped: true},
		{Resource: "merchant", Verb: "view", Scope: "tenant", Summary: "view merchants", AllowedRelations: []string{"owner", "editor", "viewer"}, DataScoped: true, Menu: true},
		{Resource: "merchant", Verb: "update", Scope: "tenant", Summary: "update merchants", AllowedRelations: []string{"owner", "editor"}, DataScoped: true},
		{Resource: "merchant", Verb: "delete", Scope: "tenant", Summary: "delete merchants", AllowedRelations: []string{"owner"}, DataScoped: true},

		{Resource: "store", Verb: "create", Scope: "merchant", Summary: "create stores", DataScoped: true},
		{Resource: "store", Verb: "view", Scope: "merchant", Summary: "view stores", AllowedRelations: []string{"owner", "editor", "viewer"}, DataScoped: true, Menu: true},
		{Resource: "store", Verb: "update", Scope: "merchant", Summary: "update stores", AllowedRelations: []string{"owner", "editor"}, DataScoped: true},
		{Resource: "store", Verb: "delete", Scope: "merchant", Summary: "delete stores", AllowedRelations: []string{"owner"}, DataScoped: true},

		{Resource: "dept", Verb: "create", Scope: "tenant", Summary: "create departments", DataScoped: true},
		{Resource: "dept", Verb: "view", Scope: "tenant", Summary: "view departments", DataScoped: true, Menu: true},
		{Resource: "dept", Verb: "update", Scope: "tenant", Summary: "update departments", DataScoped: true},
		{Resource: "dept", Verb: "delete", Scope: "tenant", Summary: "delete departments", DataScoped: true},

		{Resource: "group", Verb: "create", Scope: "tenant", Summary: "create groups", DataScoped: true},
		{Resource: "group", Verb: "view", Scope: "tenant", Summary: "view groups", DataScoped: true, Menu: true},
		{Resource: "group", Verb: "update", Scope: "tenant", Summary: "update groups", DataScoped: true},
		{Resource: "group", Verb: "delete", Scope: "tenant", Summary: "delete groups", DataScoped: true},
		{Resource: "group", Verb: "manage_member", Scope: "tenant", Summary: "assign and remove group members", DataScoped: true},

		{Resource: "user", Verb: "create", Scope: "tenant", Summary: "create users", DataScoped: true},
		{Resource: "user", Verb: "view", Scope: "tenant", Summary: "view users", DataScoped: true, Menu: true},
		{Resource: "user", Verb: "update", Scope: "tenant", Summary: "update users", DataScoped: true},
		{Resource: "user", Verb: "delete", Scope: "tenant", Summary: "delete users", DataScoped: true},

		{Resource: "service_account", Verb: "create", Scope: "tenant", Summary: "create service accounts"},
		{Resource: "service_account", Verb: "view", Scope: "tenant", Summary: "view service accounts", Menu: true},
		{Resource: "service_account", Verb: "update", Scope: "tenant", Summary: "update service accounts"},
		{Resource: "service_account", Verb: "delete", Scope: "tenant", Summary: "delete service accounts"},
		{Resource: "service_account", Verb: "manage_credential", Scope: "tenant", Summary: "create and revoke service account credentials"},

		{Resource: "invitation", Verb: "create", Scope: "tenant", Summary: "create tenant invitations"},
		{Resource: "invitation", Verb: "view", Scope: "tenant", Summary: "view tenant invitations", Menu: true},
		{Resource: "invitation", Verb: "revoke", Scope: "tenant", Summary: "revoke tenant invitations"},
		{Resource: "invitation", Verb: "resend", Scope: "tenant", Summary: "rotate tenant invitation credentials"},

		{Resource: "role", Verb: "create", Scope: "tenant", Summary: "create roles"},
		{Resource: "role", Verb: "view", Scope: "tenant", Summary: "view roles", Menu: true},
		{Resource: "role", Verb: "update", Scope: "tenant", Summary: "update roles"},
		{Resource: "role", Verb: "delete", Scope: "tenant", Summary: "delete roles"},
		{Resource: "role", Verb: "grant_permission", Scope: "tenant", Summary: "grant and revoke role permissions"},
		{Resource: "role", Verb: "manage_member", Scope: "tenant", Summary: "add and remove role members"},
		{Resource: "role", Verb: "manage_assignee", Scope: "tenant", Summary: "assign groups to roles"},

		{Resource: "menu", Verb: "create", Scope: "tenant", Summary: "create menus"},
		{Resource: "menu", Verb: "view", Scope: "tenant", Summary: "view menus", Menu: true},
		{Resource: "menu", Verb: "update", Scope: "tenant", Summary: "update menus"},
		{Resource: "menu", Verb: "delete", Scope: "tenant", Summary: "delete menus"},
		{Resource: "menu", Verb: "bind_permission", Scope: "tenant", Summary: "bind menu permissions"},

		{Resource: "oauth_client", Verb: "create", Scope: "tenant", Summary: "create OAuth clients"},
		{Resource: "oauth_client", Verb: "view", Scope: "tenant", Summary: "view OAuth clients", Menu: true},
		{Resource: "oauth_client", Verb: "update", Scope: "tenant", Summary: "rotate OAuth client credentials"},
		{Resource: "oauth_client", Verb: "delete", Scope: "tenant", Summary: "delete OAuth clients"},
		{Resource: "identity_provider", Verb: "create", Scope: "tenant", Summary: "create identity providers"},
		{Resource: "identity_provider", Verb: "view", Scope: "tenant", Summary: "view identity providers", Menu: true},
		{Resource: "identity_provider", Verb: "update", Scope: "tenant", Summary: "update identity providers"},
		{Resource: "identity_provider", Verb: "delete", Scope: "tenant", Summary: "delete identity providers"},

		{Resource: "webhook_subscription", Verb: "create", Scope: "tenant", Summary: "create webhook subscriptions"},
		{Resource: "webhook_subscription", Verb: "view", Scope: "tenant", Summary: "view webhook subscriptions and their delivery attempts", Menu: true},
		{Resource: "webhook_subscription", Verb: "update", Scope: "tenant", Summary: "update webhook subscriptions"},
		{Resource: "webhook_subscription", Verb: "delete", Scope: "tenant", Summary: "delete webhook subscriptions"},

		{Resource: "oauth_device_request", Verb: "view", Scope: "tenant", Summary: "read a device authorization request awaiting approval"},
		{Resource: "oauth_device_request", Verb: "approve", Scope: "tenant", Summary: "approve or refuse a device authorization request"},
		{Resource: "oauth_exchange_grant", Verb: "create", Scope: "tenant", Summary: "permit a client to act as a subject"},
		{Resource: "oauth_exchange_grant", Verb: "view", Scope: "tenant", Summary: "view token exchange grants", Menu: true},
		{Resource: "oauth_exchange_grant", Verb: "delete", Scope: "tenant", Summary: "withdraw a token exchange grant"},

		{Resource: "access_request", Verb: "view", Scope: "tenant", Summary: "view tenant access requests", Menu: true},
		{Resource: "access_request", Verb: "approve", Scope: "tenant", Summary: "approve, reject, and revoke temporary access"},
		{Resource: "access_review", Verb: "view", Scope: "tenant", Summary: "view tenant access review campaigns", Menu: true},
		{Resource: "access_review", Verb: "create", Scope: "tenant", Summary: "create tenant access review campaigns"},
		{Resource: "access_review", Verb: "decide", Scope: "tenant", Summary: "keep or revoke reviewed tenant role grants"},
		{Resource: "access_review", Verb: "manage", Scope: "tenant", Summary: "complete and cancel tenant access reviews"},

		{Resource: "audit_event", Verb: "view", Scope: "tenant", Summary: "view and verify tenant audit events", Menu: true},
		{Resource: "audit_event", Verb: "export", Scope: "tenant", Summary: "export tenant audit events"},
		{Resource: "audit_event", Verb: "anchor", Scope: "tenant", Summary: "anchor tenant audit heads into the external WORM store"},
		{Resource: "audit_event", Verb: "manage", Scope: "tenant", Summary: "configure audit retention policy and sign WORM root commitments"},
	}
}

// IsOperatingPlatformPermission is the shared grant and route boundary for
// independently operated platforms. New tenant actions are excluded by default.
func IsOperatingPlatformPermission(action Action) bool {
	switch action.Code {
	case "operating_platform_administer",
		"platform_member_create", "platform_member_view", "platform_member_update", "platform_member_delete",
		"role_create", "role_view", "role_update", "role_delete", "role_grant_permission", "role_manage_member", "role_manage_assignee",
		"menu_create", "menu_view", "menu_update", "menu_delete", "menu_bind_permission",
		"dept_create", "dept_view", "dept_update", "dept_delete",
		"group_create", "group_view", "group_update", "group_delete", "group_manage_member":
		return true
	default:
		return false
	}
}
