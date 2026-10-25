package files

import "7link.com/platform/internal/iam/core"

// MenuPages participates in the versioned IAM ledger. The legacy SystemMenus
// declaration stays available to older composition clients; reconciliation
// adopts its identical route instead of creating a second page.
func (*Module) MenuPages() []core.MenuPage {
	p := systemFileMenu()
	return []core.MenuPage{{Key: "system-files", Scope: "SYSTEM", App: "iam", Group: p.Branch, Route: p.Menu.Route, Icon: p.Menu.Icon, SortOrder: p.Menu.SortOrder, PermissionCode: p.Menu.PermissionCode, TranslationKey: "files.menu_system_files", Translations: p.Menu.Translations, Version: 1}}
}

var _ core.MenuContributor = (*Module)(nil)
