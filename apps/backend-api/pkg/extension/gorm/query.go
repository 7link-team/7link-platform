package gormx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"
)

const queryObserverPluginName = "gormx:query-observer"

type QueryEvent struct {
	Datasource string
	Operation  string
	Table      string
	Statement  string
	StartedAt  time.Time
	Duration   time.Duration
	Rows       int64
	Err        error
}

type QueryActivity struct {
	Datasource string
	Operation  string
	StartedAt  time.Time
}

type QueryObserver interface {
	ObserveQuery(context.Context, QueryEvent)
}

// QueryLifecycleObserver optionally receives balanced query start and finish
// notifications. SQL text is intentionally omitted because GORM usually does
// not build the parameterized statement until its execution callback runs.
type QueryLifecycleObserver interface {
	QueryStarted(context.Context, QueryActivity)
	QueryFinished(context.Context, QueryActivity)
}

func (r *DatasourceRouter) RegisterQueryObserver(observer QueryObserver) error {
	if r == nil || observer == nil {
		return nil
	}
	var errs []error
	for _, node := range r.nodes {
		if node.db == nil {
			continue
		}
		if err := node.db.Use(&queryObserverPlugin{datasource: node.name, observer: observer}); err != nil {
			errs = append(errs, fmt.Errorf("gormx: register query observer for datasource %q: %w", node.name, err))
		}
	}
	return errors.Join(errs...)
}

type queryObserverPlugin struct {
	datasource string
	observer   QueryObserver
}

func (*queryObserverPlugin) Name() string { return queryObserverPluginName }

func (plugin *queryObserverPlugin) Initialize(db *gorm.DB) error {
	if err := registerQueryCallbacks("INSERT", plugin,
		func(name string, callback func(*gorm.DB)) error {
			return db.Callback().Create().Before("gorm:begin_transaction").Register(name, callback)
		},
		func(name string, callback func(*gorm.DB)) error {
			return db.Callback().Create().After("gorm:commit_or_rollback_transaction").Register(name, callback)
		}); err != nil {
		return err
	}
	if err := registerQueryCallbacks("SELECT", plugin,
		func(name string, callback func(*gorm.DB)) error {
			return db.Callback().Query().Before("gorm:query").Register(name, callback)
		},
		func(name string, callback func(*gorm.DB)) error {
			return db.Callback().Query().After("gorm:after_query").Register(name, callback)
		}); err != nil {
		return err
	}
	if err := registerQueryCallbacks("UPDATE", plugin,
		func(name string, callback func(*gorm.DB)) error {
			return db.Callback().Update().Before("gorm:begin_transaction").Register(name, callback)
		},
		func(name string, callback func(*gorm.DB)) error {
			return db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register(name, callback)
		}); err != nil {
		return err
	}
	if err := registerQueryCallbacks("DELETE", plugin,
		func(name string, callback func(*gorm.DB)) error {
			return db.Callback().Delete().Before("gorm:begin_transaction").Register(name, callback)
		},
		func(name string, callback func(*gorm.DB)) error {
			return db.Callback().Delete().After("gorm:commit_or_rollback_transaction").Register(name, callback)
		}); err != nil {
		return err
	}
	if err := registerQueryCallbacks("SELECT", plugin,
		func(name string, callback func(*gorm.DB)) error {
			return db.Callback().Row().Before("gorm:row").Register(name, callback)
		},
		func(name string, callback func(*gorm.DB)) error {
			return db.Callback().Row().After("gorm:row").Register(name, callback)
		}); err != nil {
		return err
	}
	return registerQueryCallbacks("RAW", plugin,
		func(name string, callback func(*gorm.DB)) error {
			return db.Callback().Raw().Before("gorm:raw").Register(name, callback)
		},
		func(name string, callback func(*gorm.DB)) error {
			return db.Callback().Raw().After("gorm:raw").Register(name, callback)
		})
}

type callbackRegistration func(string, func(*gorm.DB)) error

type queryObservation struct {
	activity QueryActivity
	started  time.Time
}

func registerQueryCallbacks(operation string, plugin *queryObserverPlugin, before, after callbackRegistration) error {
	callbackPrefix := plugin.Name() + ":" + strings.ToLower(operation)
	if err := before(callbackPrefix+":before", func(db *gorm.DB) {
		started := time.Now()
		activity := QueryActivity{
			Datasource: plugin.datasource,
			Operation:  queryOperation(operation, db),
			StartedAt:  started.UTC(),
		}
		db.InstanceSet(callbackPrefix+":observation", queryObservation{activity: activity, started: started})
		if observer, ok := plugin.observer.(QueryLifecycleObserver); ok {
			observer.QueryStarted(queryContext(db), activity)
		}
	}); err != nil {
		return err
	}
	return after(callbackPrefix+":after", func(db *gorm.DB) {
		value, ok := db.InstanceGet(callbackPrefix + ":observation")
		observation, valid := value.(queryObservation)
		if !ok || !valid {
			return
		}
		ctx := queryContext(db)
		if observer, lifecycle := plugin.observer.(QueryLifecycleObserver); lifecycle {
			defer observer.QueryFinished(ctx, observation.activity)
		}
		if db.Statement == nil {
			return
		}
		statement := sanitizeSQLTemplate(db.Statement.SQL.String())
		if statement == "" {
			return
		}
		actualOperation := operation
		if operation == "RAW" {
			actualOperation = sqlOperation(statement)
		}
		plugin.observer.ObserveQuery(ctx, QueryEvent{
			Datasource: plugin.datasource,
			Operation:  actualOperation,
			Table:      db.Statement.Table,
			Statement:  statement,
			StartedAt:  observation.activity.StartedAt,
			Duration:   time.Since(observation.started),
			Rows:       db.RowsAffected,
			Err:        db.Error,
		})
	})
}

func queryContext(db *gorm.DB) context.Context {
	if db != nil && db.Statement != nil && db.Statement.Context != nil {
		return db.Statement.Context
	}
	return context.Background()
}

func queryOperation(operation string, db *gorm.DB) string {
	if operation == "RAW" && db != nil && db.Statement != nil {
		if statement := sanitizeSQLTemplate(db.Statement.SQL.String()); statement != "" {
			return sqlOperation(statement)
		}
	}
	return operation
}

func sqlOperation(statement string) string {
	if fields := strings.Fields(statement); len(fields) > 0 {
		operation := strings.ToUpper(fields[0])
		switch operation {
		case "SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "ALTER", "DROP", "PRAGMA", "WITH":
			return operation
		}
	}
	return "RAW"
}

func sanitizeSQLTemplate(statement string) string {
	var result strings.Builder
	result.Grow(len(statement))
	for index := 0; index < len(statement); {
		current := statement[index]
		switch {
		case current == '\'' || current == '"':
			quote := current
			result.WriteByte('?')
			index++
			for index < len(statement) {
				if statement[index] == '\\' && index+1 < len(statement) {
					index += 2
					continue
				}
				if statement[index] == quote {
					if index+1 < len(statement) && statement[index+1] == quote {
						index += 2
						continue
					}
					index++
					break
				}
				index++
			}
		case current == '$':
			delimiter := dollarQuoteDelimiter(statement[index:])
			if delimiter == "" {
				result.WriteByte(current)
				index++
				continue
			}
			closing := strings.Index(statement[index+len(delimiter):], delimiter)
			if closing < 0 {
				result.WriteByte(current)
				index++
				continue
			}
			result.WriteByte('?')
			index += len(delimiter) + closing + len(delimiter)
		case current == '-' && index+1 < len(statement) && statement[index+1] == '-':
			index += 2
			for index < len(statement) && statement[index] != '\n' {
				index++
			}
		case current == '/' && index+1 < len(statement) && statement[index+1] == '*':
			index += 2
			for index+1 < len(statement) && !(statement[index] == '*' && statement[index+1] == '/') {
				index++
			}
			if index+1 < len(statement) {
				index += 2
			}
		case isNumberStart(statement, index):
			result.WriteByte('?')
			index++
			for index < len(statement) && isNumberPart(statement[index]) {
				index++
			}
		default:
			result.WriteByte(current)
			index++
		}
	}
	return strings.Join(strings.Fields(result.String()), " ")
}

func isNumberStart(statement string, index int) bool {
	if index >= len(statement) || !unicode.IsDigit(rune(statement[index])) {
		return false
	}
	if index == 0 {
		return true
	}
	previous := rune(statement[index-1])
	return !unicode.IsLetter(previous) && previous != '_' && previous != '$'
}

func isNumberPart(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F' ||
		value == 'x' || value == 'X' || value == '.' || value == '+' || value == '-'
}

func dollarQuoteDelimiter(value string) string {
	if len(value) < 2 || value[0] != '$' {
		return ""
	}
	for index := 1; index < len(value); index++ {
		switch current := value[index]; {
		case current == '$':
			return value[:index+1]
		case current >= 'a' && current <= 'z', current >= 'A' && current <= 'Z', current == '_':
			continue
		default:
			return ""
		}
	}
	return ""
}
