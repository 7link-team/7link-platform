package provisioning

import (
	"strings"
	"time"

	scimfilter "github.com/scim2/filter-parser/v2"
	"gorm.io/gorm"
)

const maxFilterBytes = 4096

type filterTarget struct {
	expression string
	caseExact  bool
	boolean    bool
	timestamp  bool
}

// applySCIMFilter compiles a SCIM filter onto query. tables carries the names
// the owners published, because a member filter reads a table this module does
// not own; a name that was never published fails the filter rather than
// reaching the database.
func applySCIMFilter(query *gorm.DB, raw, resourceType, dialect string, tables *foreignTables) (*gorm.DB, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return query, nil
	}
	if len(raw) > maxFilterBytes {
		return nil, ErrInvalidFilter
	}
	expression, err := scimfilter.ParseFilter([]byte(raw))
	if err != nil {
		return nil, ErrInvalidFilter
	}
	nodes := 0
	condition, args, err := compileFilterExpression(expression, resourceType, dialect, tables, false, 0, &nodes)
	if err != nil {
		return nil, ErrInvalidFilter
	}
	return query.Where(condition, args...), nil
}

func compileFilterExpression(expression scimfilter.Expression, resourceType, dialect string, tables *foreignTables, negate bool, depth int, nodes *int) (string, []any, error) {
	(*nodes)++
	if depth > 16 || *nodes > 64 {
		return "", nil, ErrInvalidFilter
	}
	var condition string
	var args []any
	switch value := expression.(type) {
	case *scimfilter.AttributeExpression:
		var err error
		condition, args, err = compileAttribute(*value, resourceType, dialect, tables, "")
		if err != nil {
			return "", nil, err
		}
	case *scimfilter.LogicalExpression:
		if value.Operator != scimfilter.AND && value.Operator != scimfilter.OR {
			return "", nil, ErrInvalidFilter
		}
		left, leftArgs, err := compileFilterExpression(value.Left, resourceType, dialect, tables, false, depth+1, nodes)
		if err != nil {
			return "", nil, err
		}
		right, rightArgs, err := compileFilterExpression(value.Right, resourceType, dialect, tables, false, depth+1, nodes)
		if err != nil {
			return "", nil, err
		}
		operator := " AND "
		if value.Operator == scimfilter.OR {
			operator = " OR "
		}
		condition, args = "("+left+operator+right+")", append(leftArgs, rightArgs...)
	case *scimfilter.NotExpression:
		return compileFilterExpression(value.Expression, resourceType, dialect, tables, !negate, depth+1, nodes)
	case *scimfilter.ValuePath:
		attribute := strings.ToLower(value.AttributePath.AttributeName)
		if attribute != "emails" && attribute != "members" {
			return "", nil, ErrInvalidFilter
		}
		inner, ok := value.ValueFilter.(*scimfilter.AttributeExpression)
		if !ok {
			return "", nil, ErrInvalidFilter
		}
		var err error
		condition, args, err = compileAttribute(*inner, resourceType, dialect, tables, attribute)
		if err != nil {
			return "", nil, err
		}
	default:
		return "", nil, ErrInvalidFilter
	}
	if negate {
		condition = "NOT (" + condition + ")"
	}
	return condition, args, nil
}

func compileAttribute(expression scimfilter.AttributeExpression, resourceType, dialect string, tables *foreignTables, parent string) (string, []any, error) {
	path := expression.AttributePath
	if path.URIPrefix != nil {
		allowed := UserSchema
		if resourceType == ResourceGroup {
			allowed = GroupSchema
		}
		if !strings.EqualFold(*path.URIPrefix, allowed) {
			return "", nil, ErrInvalidFilter
		}
	}
	name := strings.ToLower(path.AttributeName)
	if path.SubAttribute != nil {
		name += "." + strings.ToLower(*path.SubAttribute)
	}
	if parent != "" {
		name = strings.ToLower(parent) + "." + name
	}
	if resourceType == ResourceGroup && name == "members.value" {
		return compileMemberFilter(expression.Operator, expression.CompareValue, tables)
	}
	target, ok := filterAttributes(resourceType)[name]
	if !ok {
		return "", nil, ErrInvalidFilter
	}
	return compileComparison(target, expression.Operator, expression.CompareValue, dialect)
}

func filterAttributes(resourceType string) map[string]filterTarget {
	common := map[string]filterTarget{
		"id":                {expression: "resource.resource_id", caseExact: true},
		"externalid":        {expression: "resource.external_id", caseExact: true},
		"meta.created":      {expression: "resource.created_at", timestamp: true},
		"meta.lastmodified": {expression: "resource.updated_at", timestamp: true},
	}
	if resourceType == ResourceUser {
		common["username"] = filterTarget{expression: "principal.login_name"}
		common["displayname"] = filterTarget{expression: "principal.display_name"}
		common["name.formatted"] = filterTarget{expression: "principal.display_name"}
		common["emails.value"] = filterTarget{expression: "principal.email"}
		common["active"] = filterTarget{expression: "(principal.status = 'active' AND member.status = 'active')", boolean: true}
	} else if resourceType == ResourceGroup {
		common["displayname"] = filterTarget{expression: "scim_group.name"}
	}
	return common
}

func compileComparison(target filterTarget, operator scimfilter.CompareOperator, value any, dialect string) (string, []any, error) {
	if operator == scimfilter.PR {
		if target.boolean {
			return "1 = 1", nil, nil
		}
		return target.expression + " <> ''", nil, nil
	}
	if target.boolean {
		boolean, ok := value.(bool)
		if !ok || operator != scimfilter.EQ && operator != scimfilter.NE {
			return "", nil, ErrInvalidFilter
		}
		if boolean == (operator == scimfilter.EQ) {
			return target.expression, nil, nil
		}
		return "NOT " + target.expression, nil, nil
	}
	if value == nil {
		if operator == scimfilter.EQ {
			return target.expression + " IS NULL", nil, nil
		}
		if operator == scimfilter.NE {
			return target.expression + " IS NOT NULL", nil, nil
		}
		return "", nil, ErrInvalidFilter
	}
	text, ok := value.(string)
	if !ok {
		return "", nil, ErrInvalidFilter
	}
	switch operator {
	case scimfilter.CO, scimfilter.SW, scimfilter.EW:
		// An empty substring matches every row, and SUBSTR(column, -0) is
		// dialect-defined, so the substring operators require a value.
		if text == "" {
			return "", nil, ErrInvalidFilter
		}
	}
	if target.timestamp {
		parsed, err := time.Parse(time.RFC3339, text)
		if err != nil || operator == scimfilter.CO || operator == scimfilter.SW || operator == scimfilter.EW {
			return "", nil, ErrInvalidFilter
		}
		return comparison(target.expression, operator, parsed.UTC(), dialect)
	}
	column, argument := target.expression, text
	if !target.caseExact {
		column, argument = "LOWER("+column+")", strings.ToLower(text)
	}
	return comparison(column, operator, argument, dialect)
}

func comparison(column string, operator scimfilter.CompareOperator, argument any, dialect string) (string, []any, error) {
	switch operator {
	case scimfilter.EQ:
		return column + " = ?", []any{argument}, nil
	case scimfilter.NE:
		return column + " <> ?", []any{argument}, nil
	case scimfilter.GT:
		return column + " > ?", []any{argument}, nil
	case scimfilter.GE:
		return column + " >= ?", []any{argument}, nil
	case scimfilter.LT:
		return column + " < ?", []any{argument}, nil
	case scimfilter.LE:
		return column + " <= ?", []any{argument}, nil
	case scimfilter.CO:
		if dialect == "mysql" {
			return "LOCATE(?, " + column + ") > 0", []any{argument}, nil
		}
		if dialect == "postgres" {
			return "STRPOS(" + column + ", ?) > 0", []any{argument}, nil
		}
		return "INSTR(" + column + ", ?) > 0", []any{argument}, nil
	case scimfilter.SW:
		return "SUBSTR(" + column + ", 1, LENGTH(?)) = ?", []any{argument, argument}, nil
	case scimfilter.EW:
		return "SUBSTR(" + column + ", -LENGTH(?)) = ?", []any{argument, argument}, nil
	default:
		return "", nil, ErrInvalidFilter
	}
}

func compileMemberFilter(operator scimfilter.CompareOperator, value any, tables *foreignTables) (string, []any, error) {
	members := tables.directory.GroupMembers.String()
	exists := "EXISTS (SELECT 1 FROM " + members + " AS filter_member WHERE filter_member.tenant_id = directory.tenant_id AND filter_member.group_id = resource.resource_id"
	if operator == scimfilter.PR {
		return exists + ")", nil, nil
	}
	memberID, ok := value.(string)
	if !ok || memberID == "" || operator != scimfilter.EQ && operator != scimfilter.NE {
		return "", nil, ErrInvalidFilter
	}
	condition := exists + " AND filter_member.principal_id = ?)"
	if operator == scimfilter.NE {
		condition = "NOT " + condition
	}
	return condition, []any{memberID}, nil
}

func normalizeDialect(db *gorm.DB) string {
	if db == nil || db.Dialector == nil {
		return ""
	}
	return db.Dialector.Name()
}
