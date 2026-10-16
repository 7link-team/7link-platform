package delivery

import (
	"fmt"
	"net/url"
	"strings"

	deliveryi18n "7link.com/platform/internal/delivery/i18n"
	"7link.com/platform/internal/infra/guid"
	"github.com/danielgtaylor/huma/v2"
)

// projectCreateBody is the H1 request contract. Declarative constraints belong
// here so Huma validation and OpenAPI/field metadata share the same source.
// Identity, authorization and reference ownership require command-layer checks.
type projectCreateBody struct {
	Name            string                   `json:"name" minLength:"1" maxLength:"200"`
	Code            string                   `json:"code" pattern:"^[A-Z][A-Z0-9-]{1,15}$"`
	Description     *string                  `json:"description,omitempty" maxLength:"2000"`
	Client          *projectClientInput      `json:"client,omitempty"`
	TargetDate      *string                  `json:"target_date,omitempty" format:"date"`
	OriginIntakeID  *guid.ID                 `json:"origin_intake_id,omitempty"`
	Contacts        []projectContactInput    `json:"contacts,omitempty" maxItems:"20"`
	Members         []projectMemberInput     `json:"members,omitempty" maxItems:"50"`
	Repositories    []projectRepositoryInput `json:"repositories,omitempty" maxItems:"10"`
	BudgetCapMicros *int64                   `json:"budget_cap_micros,omitempty" minimum:"1"`
	BudgetNotifyPct int                      `json:"budget_notify_pct,omitempty" default:"80" minimum:"50" maximum:"95"`
	PortalSettings  projectPortalInput       `json:"portal_settings,omitempty"`
}

type projectClientInput struct {
	EntityID *guid.ID `json:"entity_id,omitempty"`
	NewName  *string  `json:"new_name,omitempty" minLength:"1" maxLength:"200"`
}

// Default-true booleans must be pointers: Huma applies defaults to zero values,
// so a plain bool would silently turn an explicitly supplied false into true.
// nullable:false rejects null while omission still receives the tag default.
type projectContactInput struct {
	IntakeContactID    guid.ID `json:"intake_contact_id" nullable:"false"`
	Role               string  `json:"role,omitempty" default:"CC" enum:"PRIMARY,CC"`
	ReceivesDeliveries *bool   `json:"receives_deliveries,omitempty" default:"true" nullable:"false"`
	CanAccept          *bool   `json:"can_accept,omitempty" default:"true" nullable:"false"`
}

type projectMemberInput struct {
	PrincipalID guid.ID `json:"principal_id" nullable:"false"`
	RoleKey     string  `json:"role_key" minLength:"1"`
}

type projectRepositoryInput struct {
	Name              string   `json:"name" minLength:"1" maxLength:"100"`
	RemoteURL         string   `json:"remote_url" minLength:"1" maxLength:"2000"`
	DefaultBranch     string   `json:"default_branch,omitempty" default:"main" minLength:"1" maxLength:"200"`
	ProtectedBranches []string `json:"protected_branches,omitempty" default:"[\"main\"]" maxItems:"20"`
	IsPrimary         bool     `json:"is_primary,omitempty" default:"false"`
}

type projectPortalInput struct {
	ShowAILabel         *bool  `json:"show_ai_label,omitempty" default:"true" nullable:"false"`
	ShowWorkItemTitles  *bool  `json:"show_work_item_titles,omitempty" default:"true" nullable:"false"`
	ShowEvidenceSummary *bool  `json:"show_evidence_summary,omitempty" default:"true" nullable:"false"`
	ShowReleaseNotes    *bool  `json:"show_release_notes,omitempty" default:"true" nullable:"false"`
	ContributorDisplay  string `json:"contributor_display,omitempty" default:"TEAM_ONLY" enum:"TEAM_ONLY,ROLES"`
	MilestonesVisible   *bool  `json:"milestones_visible,omitempty" default:"true" nullable:"false"`
}

func (in *projectCreateBody) Resolve(_ huma.Context, prefix *huma.PathBuffer) []error {
	var errs []error
	add := func(field, key string) {
		errs = append(errs, &huma.ErrorDetail{Location: prefix.String() + "." + field, Message: key})
	}
	if in.Client != nil && (in.Client.EntityID == nil) == (in.Client.NewName == nil) {
		add("client", deliveryi18n.ClientChoice)
	}
	primaryContact := false
	for i, contact := range in.Contacts {
		if contact.Role == "PRIMARY" {
			if primaryContact {
				add(fmt.Sprintf("contacts[%d].role", i), deliveryi18n.PrimaryContact)
			}
			if contact.CanAccept != nil && !*contact.CanAccept {
				add(fmt.Sprintf("contacts[%d].can_accept", i), deliveryi18n.PrimaryAccept)
			}
			primaryContact = true
		}
	}
	names := make(map[string]bool, len(in.Repositories))
	primaryRepository := false
	for i, repository := range in.Repositories {
		if !validRepositoryURL(repository.RemoteURL) {
			add(fmt.Sprintf("repositories[%d].remote_url", i), deliveryi18n.RepositoryURL)
		}
		if names[repository.Name] {
			add(fmt.Sprintf("repositories[%d].name", i), deliveryi18n.RepositoryName)
		}
		names[repository.Name] = true
		if repository.IsPrimary {
			if primaryRepository {
				add(fmt.Sprintf("repositories[%d].is_primary", i), deliveryi18n.PrimaryRepository)
			}
			primaryRepository = true
		}
	}
	return errs
}

// validRepositoryURL accepts the three remote forms specified by BR-07 and
// never echoes a rejected value, which may contain a password, into an error.
func validRepositoryURL(raw string) bool {
	if strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	if strings.HasPrefix(raw, "git@") {
		host, path, ok := strings.Cut(strings.TrimPrefix(raw, "git@"), ":")
		return ok && host != "" && path != "" && !strings.ContainsAny(host, "/@?#")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.Path == "" || parsed.Path == "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	switch parsed.Scheme {
	case "https":
		return parsed.User == nil
	case "ssh":
		if parsed.User == nil {
			return true
		}
		_, password := parsed.User.Password()
		return !password && parsed.User.Username() != ""
	default:
		return false
	}
}

var _ huma.ResolverWithPath = (*projectCreateBody)(nil)
