package audit

import (
	"regexp"
	"slices"

	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// BusinessFilterInput selects only facts already frozen in the audit chain.
// Missing historical labels never acquire inferred values during a read.
type BusinessFilterInput struct {
	ProjectID       string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"Exact project_id stored in event detail; unlabeled events do not match."`
	Module          string `query:"module" pattern:"^[a-z][a-z0-9_]*$" maxLength:"64" doc:"Exact event-type namespace before the first dot."`
	ActorKind       string `query:"actor_kind" enum:"HUMAN,AGENT,MACHINE,CLIENT,SYSTEM" doc:"Exact actor_kind stored in event detail."`
	AuthoritySource string `query:"authority_source" pattern:"^(ROLE|DELEGATION|POLICY|CLIENT|SYSTEM):[A-Za-z0-9_.:-]{1,80}$" maxLength:"91" doc:"Exact authority_source stored in event detail, including its reference."`
}

var businessModulePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var businessAuthorityPattern = regexp.MustCompile(`^(ROLE|DELEGATION|POLICY|CLIENT|SYSTEM):[A-Za-z0-9_.:-]{1,80}$`)

func withBusinessFilter(filter Filter, in BusinessFilterInput) (Filter, error) {
	project, err := guid.OptionalID(in.ProjectID)
	if err != nil {
		return Filter{}, ErrInvalidFilter
	}
	filter.ProjectID, filter.Module, filter.ActorKind, filter.AuthoritySource = project, in.Module, in.ActorKind, in.AuthoritySource
	if err = validateBusinessFilter(filter); err != nil {
		return Filter{}, err
	}
	return filter, nil
}

func validateBusinessFilter(filter Filter) error {
	if filter.ProjectID < 0 || len(filter.Module) > 64 || (filter.Module != "" && !businessModulePattern.MatchString(filter.Module)) ||
		(filter.ActorKind != "" && !slices.Contains([]string{"HUMAN", "AGENT", "MACHINE", "CLIENT", "SYSTEM"}, filter.ActorKind)) ||
		(filter.AuthoritySource != "" && !businessAuthorityPattern.MatchString(filter.AuthoritySource)) {
		return ErrInvalidFilter
	}
	return nil
}

func applyBusinessFilter(db *gorm.DB, filter Filter) *gorm.DB {
	if filter.Module != "" {
		prefix := filter.Module + "."
		// Equality over the namespace avoids LIKE wildcard semantics for '_'.
		if db.Dialector.Name() == "mysql" {
			db = db.Where("CAST(substr(event_type, 1, ?) AS BINARY) = CAST(? AS BINARY)", len(prefix), prefix)
		} else {
			db = db.Where("substr(event_type, 1, ?) = ?", len(prefix), prefix)
		}
	}
	for _, field := range []struct{ key, value string }{{"project_id", filter.ProjectID.String()}, {"actor_kind", filter.ActorKind}, {"authority_source", filter.AuthoritySource}} {
		if field.value == "" || (field.key == "project_id" && filter.ProjectID == 0) {
			continue
		}
		// Keys are fixed above, values always bound. Normalize JSON numbers and
		// strings to text so old numeric IDs and current string IDs agree.
		var expression string
		switch db.Dialector.Name() {
		case "postgres":
			expression = "(CAST(detail AS jsonb) ->> '" + field.key + "') = ?"
		case "mysql":
			expression = "CAST(JSON_UNQUOTE(JSON_EXTRACT(detail, '$." + field.key + "')) AS BINARY) = CAST(? AS BINARY)"
		default:
			expression = "CAST(json_extract(detail, '$." + field.key + "') AS TEXT) = ?"
		}
		db = db.Where(expression, field.value)
	}
	return db
}
