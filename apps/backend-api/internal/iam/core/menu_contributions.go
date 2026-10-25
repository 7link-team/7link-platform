package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MenuPage is a code-owned navigation declaration. A route-less declaration
// names a group; Group references a group key from the same module or a core
// branch label. Translations may freeze the owner's embedded locale bundle.
type MenuPage struct {
	Key, Scope, App, Domain, Space, Group       string
	Route, Icon, PermissionCode, TranslationKey string
	SortOrder                                   int
	Version                                     int64
	Translations                                map[string]string
}

type MenuContributor interface {
	module.Module
	MenuPages() []MenuPage
}
type MenuSink interface {
	ContributeMenus(owner string, pages ...MenuPage) error
}

type ownedMenuPage struct {
	Owner string
	MenuPage
}
type menuContributions struct {
	mu       sync.RWMutex
	registry *authz.Registry
	pages    []ownedMenuPage
	sealed   bool
}

var menuOwnerPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var menuKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`)

func validMenuPlacement(app, domain, space, route string) bool {
	if app != "delivery" {
		return domain == "" && space == ""
	}
	if !slices.Contains([]string{"HOME", "CHAT", "PROJECTS", "TEAM", "GOVERNANCE"}, domain) || !slices.Contains([]string{"TEAM", "PROJECT"}, space) {
		return false
	}
	if space == "PROJECT" {
		return domain == "PROJECTS" && (route == "" || (route == "/t/:tenantID/p/:projectID" || strings.HasPrefix(route, "/t/:tenantID/p/:projectID/")))
	}
	return !strings.Contains(route, ":projectID")
}

func (c *menuContributions) ContributeMenus(owner string, pages ...MenuPage) error {
	if c == nil {
		return ErrUnavailable
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sealed || !menuOwnerPattern.MatchString(owner) {
		return fmt.Errorf("%w: menus module %q sealed or invalid owner", ErrInvalidArgument, owner)
	}
	all := slices.Clone(c.pages)
	for _, p := range pages {
		fail := func(field string) error {
			return fmt.Errorf("%w: menus module %s key %q field %s", ErrInvalidArgument, owner, p.Key, field)
		}
		if !menuKeyPattern.MatchString(p.Key) {
			return fail("Key")
		}
		if !menuOwnerPattern.MatchString(p.App) || len(p.App) > 32 {
			return fail("App")
		}
		if !slices.Contains([]string{"TENANT", "PLATFORM", "SYSTEM"}, p.Scope) {
			return fail("Scope")
		}
		if !validMenuPlacement(p.App, p.Domain, p.Space, p.Route) {
			return fail("Domain/Space/Route")
		}
		if p.Version <= 0 {
			return fail("Version")
		}
		if !strings.HasPrefix(p.TranslationKey, owner+".") {
			return fail("TranslationKey")
		}
		if p.SortOrder < -100000 || p.SortOrder > 100000 {
			return fail("SortOrder")
		}
		if len(p.TranslationKey) > 256 || len(p.Group) > 128 {
			return fail("TranslationKey/Group")
		}
		if len(p.Translations) > 0 {
			for _, loc := range []string{"en-US", "zh-CN", "ms-MY"} {
				if strings.TrimSpace(p.Translations[loc]) == "" || !utf8.ValidString(p.Translations[loc]) || len(p.Translations[loc]) > 128 {
					return fail("Translations")
				}
			}
		}
		if len(p.Icon) > 64 {
			return fail("Icon")
		}
		if p.Route != "" {
			u, e := url.Parse(p.Route)
			if e != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || strings.Contains(p.Route, "\\") || !strings.HasPrefix(p.Route, "/") || path.Clean(p.Route) != p.Route || len(p.Route) > 512 {
				return fail("Route")
			}
			if c.registry == nil {
				return fail("PermissionCode")
			}
			a, ok := c.registry.Find(p.PermissionCode)
			want := map[string]string{"TENANT": authz.ActionScopeTenant, "PLATFORM": authz.ActionScopeOperatingPlatform, "SYSTEM": authz.ActionScopeSystem}[p.Scope]
			if !ok || a.Scope != want {
				return fail("PermissionCode/scope")
			}
		} else if p.PermissionCode != "" {
			return fail("group PermissionCode")
		}
		if p.App == "iam" && p.Route != "" {
			for _, corePage := range coreMenuDefaults(p.Scope) {
				if corePage.Route == p.Route {
					return fail("duplicate core Route")
				}
			}
		}
		for _, old := range all {
			if old.Owner == owner && old.Key == p.Key {
				return fail("duplicate Key")
			}
			if p.Route != "" && old.Scope == p.Scope && old.App == p.App && old.Route == p.Route {
				return fail("duplicate Route")
			}
		}
		p.Translations = maps.Clone(p.Translations)
		all = append(all, ownedMenuPage{owner, p})
	}
	// Group declarations may follow their children in the same atomic batch.
	for _, p := range all {
		if p.Group == "" {
			continue
		}
		found := false
		for _, g := range all {
			if g.Owner == p.Owner && g.Key == p.Group && g.Route == "" && g.Group == "" && g.Scope == p.Scope && g.App == p.App && g.Domain == p.Domain && g.Space == p.Space {
				found = true
			}
		}
		for _, g := range coreMenuDefaults(p.Scope) {
			if g.Route == "" && g.Label == p.Group && p.App == "iam" {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%w: menus module %s key %s field Group", ErrInvalidArgument, p.Owner, p.Key)
		}
	}
	c.pages = all
	return nil
}

func (c *menuContributions) snapshot() []ownedMenuPage {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Clone(c.pages)
}

func (p ownedMenuPage) menu(tenant, parent guid.ID) Menu {
	labels := maps.Clone(p.Translations)
	if len(labels) == 0 {
		labels = map[string]string{}
		for _, loc := range platformi18n.Supported() {
			labels[loc.Code] = platformi18n.TContext(platformi18n.WithLocale(context.Background(), loc.Code), p.TranslationKey)
		}
	}
	return Menu{TenantID: tenant, ParentID: parent, App: p.App, Domain: p.Domain, Space: p.Space, Label: labels["en-US"], Translations: labels, Route: p.Route, Icon: p.Icon, SortOrder: p.SortOrder, PermissionCode: p.PermissionCode, Status: MenuActive}
}

// Only administrator-editable navigation fields participate, never IDs or time.
func menuSeedHash(m Menu) string {
	b, _ := json.Marshal(struct {
		Parent                                                     guid.ID
		App, Domain, Space, Label, Route, Icon, Permission, Status string
		Sort                                                       int
		Labels                                                     map[string]string
	}{m.ParentID, menuApp(m.App), m.Domain, m.Space, m.Label, m.Route, m.Icon, m.PermissionCode, string(m.Status), m.SortOrder, m.Translations})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// syncContributedMenus runs in the bootstrap/job transaction. The owner row
// serializes initial installs; current locking menu reads serialize admin edits.
func (r *Repository) syncContributedMenus(ctx context.Context, tenant guid.ID, kind string) (int, error) {
	if r.contributions == nil {
		return 0, nil
	}
	q, db, err := r.tables()
	if err != nil {
		return 0, err
	}
	directory, err := r.directoryTables()
	if err != nil {
		return 0, err
	}
	if _, ok := db.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalidArgument
	}
	var owner struct {
		ID                guid.ID
		ScopeKind, Status string
	}
	if err = db.WithContext(ctx).Table(directory.Tenants.String()).Clauses(clause.Locking{Strength: "UPDATE"}).Select("id, scope_kind, status").Where("id = ?", tenant).Take(&owner).Error; err != nil {
		return 0, err
	}
	if owner.Status != "active" {
		return 0, nil
	}
	if owner.ScopeKind != kind {
		return 0, ErrInvalidArgument
	}
	if err = r.revisions.Lock(ctx, db, tenant); err != nil {
		return 0, err
	}
	rows, err := q.Menus.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.Menus.TenantID.Eq(tenant)).Find()
	if err != nil {
		return 0, err
	}
	ledgers, err := q.MenuContributions.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.MenuContributions.TenantID.Eq(tenant)).Find()
	if err != nil {
		return 0, err
	}
	menus := map[guid.ID]Menu{}
	for _, row := range rows {
		menus[row.ID] = menuFromRow(row)
	}
	ledger := map[string]*model.MenuContribution{}
	for _, l := range ledgers {
		ledger[l.Module+"/"+l.PageKey] = l
	}
	pages := r.contributions.snapshot()
	scope := map[string]string{"tenant": "TENANT", "platform": "PLATFORM", "system": "SYSTEM"}[kind]
	// Groups first, then leaves, with deterministic declaration ordering.
	slices.SortStableFunc(pages, func(a, b ownedMenuPage) int {
		if (a.Route == "") != (b.Route == "") {
			if a.Route == "" {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Owner+"/"+a.Key, b.Owner+"/"+b.Key)
	})
	active := map[string]bool{}
	parents := map[string]guid.ID{}
	changed := 0
	for _, p := range pages {
		if p.Scope != scope {
			continue
		}
		key := p.Owner + "/" + p.Key
		active[key] = true
		parent := guid.ID(0)
		if p.Group != "" {
			parent = parents[p.Owner+"/"+p.Group]
			if parent == 0 {
				for _, m := range menus {
					if m.Route == "" && m.Label == p.Group && menuApp(m.App) == p.App && m.Domain == p.Domain && m.Space == p.Space {
						if parent == 0 || m.ID < parent {
							parent = m.ID
						}
					}
				}
			}
			// A deleted group is a tombstone too: never silently recreate it or
			// install an orphan leaf elsewhere.
			if parent == 0 {
				continue
			}
		}
		desired := p.menu(tenant, parent)
		for _, loc := range []string{"en-US", "zh-CN", "ms-MY"} {
			if strings.TrimSpace(desired.Translations[loc]) == "" || desired.Translations[loc] == p.TranslationKey || !utf8.ValidString(desired.Translations[loc]) || len(desired.Translations[loc]) > 128 {
				return 0, fmt.Errorf("%w: menus module %s key %s missing translation %s", ErrInvalidArgument, p.Owner, p.Key, loc)
			}
		}
		l := ledger[key]
		if l == nil {
			var existing Menu
			for _, m := range menus {
				if menuApp(m.App) == p.App && m.Route == p.Route && (p.Route != "" || m.Label == desired.Label && m.ParentID == parent && m.Domain == p.Domain && m.Space == p.Space) {
					if existing.ID == 0 || m.ID < existing.ID {
						existing = m
					}
				}
			}
			if existing.ID == 0 {
				existing, err = r.CreateMenu(ctx, desired)
				if err != nil {
					return 0, err
				}
				menus[existing.ID] = existing
				changed++
			}
			id, e := r.nextID()
			if e != nil {
				return 0, e
			}
			now := r.now().UTC()
			l = &model.MenuContribution{TenantID: tenant, ID: id, Module: p.Owner, PageKey: p.Key, MenuID: &existing.ID, Version: p.Version, SeededHash: menuSeedHash(desired), Customized: menuSeedHash(existing) != menuSeedHash(desired), CreatedAt: now, UpdatedAt: now}
			if err = q.MenuContributions.WithContext(ctx).Create(l); err != nil {
				return 0, err
			}
			ledger[key] = l
			changed++
		} else if l.MenuID != nil {
			dirty := false
			current, exists := menus[*l.MenuID]
			if !exists {
				dirty = true
				l.MenuID = nil
				changed++
			} else if l.Version < p.Version {
				if menuSeedHash(current) == l.SeededHash {
					l.Customized = false
					desired.ID = current.ID
					updated, e := r.UpdateMenu(ctx, desired)
					if e != nil {
						return 0, e
					}
					menus[updated.ID] = updated
					l.SeededHash = menuSeedHash(desired)
				} else {
					l.Customized = true
				}
				l.Version = p.Version
				dirty = true
				changed++
			}
			if dirty {
				l.UpdatedAt = r.now().UTC()
				if err = q.MenuContributions.WithContext(ctx).Save(l); err != nil {
					return 0, err
				}
			}
		}
		if l.MenuID != nil {
			parents[key] = *l.MenuID
		}
	}
	for key, l := range ledger {
		if active[key] || l.MenuID == nil {
			continue
		}
		current, exists := menus[*l.MenuID]
		if !exists {
			l.MenuID = nil
			l.UpdatedAt = r.now().UTC()
			if err = q.MenuContributions.WithContext(ctx).Save(l); err != nil {
				return 0, err
			}
			changed++
			continue
		}
		if current.Status != MenuDisabled {
			current.Status = MenuDisabled
			if _, err = r.UpdateMenu(ctx, current); err != nil {
				return 0, err
			}
			changed++
		}
	}
	return changed, nil
}

func menuBytesHash(value any) string {
	b, _ := json.Marshal(value)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Hide a removed/disabled module's contributed rows immediately, including the
// interval before the daily sync marks them disabled. Unknown permissions on
// unrelated custom menus keep the existing validation behavior.
func (r *Repository) activeContributedMenus(ctx context.Context, tenant guid.ID, menus []Menu, kind string) ([]Menu, error) {
	if r.contributions == nil {
		return menus, nil
	}
	q, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	rows, err := q.MenuContributions.WithContext(ctx).Where(q.MenuContributions.TenantID.Eq(tenant)).Find()
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	for _, p := range r.contributions.snapshot() {
		if p.Scope == map[string]string{"tenant": "TENANT", "platform": "PLATFORM", "system": "SYSTEM"}[kind] {
			active[p.Owner+"/"+p.Key] = true
		}
	}
	hidden := map[guid.ID]bool{}
	for _, l := range rows {
		if l.MenuID != nil && !active[l.Module+"/"+l.PageKey] {
			hidden[*l.MenuID] = true
		}
	}
	out := make([]Menu, 0, len(menus))
	for _, m := range menus {
		if !hidden[m.ID] {
			out = append(out, m)
		}
	}
	return out, nil
}

// Legacy structural upgrades must not bypass a versioned page's deletion
// ledger. The normal catalog remains intact for callers rendering declarations.
func (r *Repository) unversionedSystemMenuDefaults() []Menu {
	pages := r.contributions.snapshot()
	return slices.DeleteFunc(r.systemMenuDefaults(), func(m Menu) bool {
		for _, p := range pages {
			if p.Scope == "SYSTEM" && p.App == menuApp(m.App) && p.Route != "" && p.Route == m.Route {
				return true
			}
		}
		return false
	})
}

func coreMenuDefaults(scope string) []Menu {
	switch scope {
	case "TENANT":
		return DefaultMenus()
	case "SYSTEM":
		return SystemDefaultMenus()
	case "PLATFORM":
		return bootstrapMenus("platform")
	}
	return nil
}

// WireMenuContributions collects activated modules after all permission
// catalogs have been contributed during Wire, before initialization or seeding.
func WireMenuContributions(registry *module.Registry) error {
	var sink MenuSink
	for _, current := range registry.Modules() {
		provider, ok := current.(MenuContributor)
		if !ok {
			continue
		}
		if sink == nil {
			var err error
			sink, err = module.Resolve[MenuSink](registry.Services())
			if err != nil {
				return fmt.Errorf("menus from %s: %w", current.ModuleName(), err)
			}
		}
		if err := sink.ContributeMenus(current.ModuleName(), provider.MenuPages()...); err != nil {
			return fmt.Errorf("menus from %s: %w", current.ModuleName(), err)
		}
	}
	return nil
}
