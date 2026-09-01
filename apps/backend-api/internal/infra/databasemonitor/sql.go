package databasemonitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	gormx "7link.com/platform/pkg/extension/gorm"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

const maxSQLTemplateBytes = 4096

type queryStat struct {
	Datasource      string
	Fingerprint     string
	Operation       string
	Table           string
	Statement       string
	Executions      uint64
	Errors          uint64
	TotalRows       int64
	MaxRows         int64
	TotalDuration   time.Duration
	MinDuration     time.Duration
	MaxDuration     time.Duration
	FirstExecutedAt time.Time
	LastExecutedAt  time.Time
	LastErrorClass  string
	LastErrorAt     time.Time
	DurationBuckets DurationBuckets
}

type SQLStat struct {
	Datasource        string          `json:"datasource"`
	Fingerprint       string          `json:"fingerprint"`
	Operation         string          `json:"operation"`
	Table             string          `json:"table,omitempty"`
	Statement         string          `json:"statement"`
	Executions        uint64          `json:"executions"`
	Successes         uint64          `json:"successes"`
	Errors            uint64          `json:"errors"`
	ErrorRate         float64         `json:"error_rate"`
	TotalRows         int64           `json:"total_rows"`
	MaxRows           int64           `json:"max_rows"`
	MinimumDurationMS float64         `json:"minimum_duration_ms"`
	AverageDurationMS float64         `json:"average_duration_ms"`
	MaxDurationMS     float64         `json:"max_duration_ms"`
	TotalDurationMS   float64         `json:"total_duration_ms"`
	DurationBuckets   DurationBuckets `json:"duration_buckets"`
	FirstExecutedAt   time.Time       `json:"first_executed_at"`
	LastExecutedAt    time.Time       `json:"last_executed_at"`
	LastErrorClass    string          `json:"last_error_class,omitempty"`
	LastErrorAt       *time.Time      `json:"last_error_at,omitempty"`
}

type SlowQuery struct {
	Datasource  string    `json:"datasource"`
	Fingerprint string    `json:"fingerprint"`
	Operation   string    `json:"operation"`
	Table       string    `json:"table,omitempty"`
	Statement   string    `json:"statement"`
	DurationMS  float64   `json:"duration_ms"`
	Rows        int64     `json:"rows"`
	Failed      bool      `json:"failed"`
	ErrorClass  string    `json:"error_class,omitempty"`
	TraceID     string    `json:"trace_id,omitempty"`
	ExecutedAt  time.Time `json:"executed_at"`
}

type SQLReport struct {
	GeneratedAt     time.Time       `json:"generated_at"`
	StatisticsSince time.Time       `json:"statistics_since"`
	Capacity        int             `json:"capacity"`
	Tracked         int             `json:"tracked"`
	SlowCapacity    int             `json:"slow_capacity"`
	SlowThresholdMS float64         `json:"slow_threshold_ms"`
	Summary         SQLSummary      `json:"summary"`
	Operations      []OperationStat `json:"operations"`
	Statements      []SQLStat       `json:"statements"`
	SlowQueries     []SlowQuery     `json:"slow_queries"`
}

type SQLSummary struct {
	Executions        uint64  `json:"executions"`
	Successes         uint64  `json:"successes"`
	Errors            uint64  `json:"errors"`
	ErrorRate         float64 `json:"error_rate"`
	Running           uint64  `json:"running"`
	ConcurrentMax     uint64  `json:"concurrent_max"`
	TotalRows         int64   `json:"total_rows"`
	MinimumDurationMS float64 `json:"minimum_duration_ms"`
	AverageDurationMS float64 `json:"average_duration_ms"`
	MaxDurationMS     float64 `json:"max_duration_ms"`
	TotalDurationMS   float64 `json:"total_duration_ms"`
}

type OperationStat struct {
	Datasource        string          `json:"datasource"`
	Operation         string          `json:"operation"`
	Executions        uint64          `json:"executions"`
	Successes         uint64          `json:"successes"`
	Errors            uint64          `json:"errors"`
	ErrorRate         float64         `json:"error_rate"`
	Running           uint64          `json:"running"`
	ConcurrentMax     uint64          `json:"concurrent_max"`
	TotalRows         int64           `json:"total_rows"`
	MaxRows           int64           `json:"max_rows"`
	MinimumDurationMS float64         `json:"minimum_duration_ms"`
	AverageDurationMS float64         `json:"average_duration_ms"`
	MaxDurationMS     float64         `json:"max_duration_ms"`
	TotalDurationMS   float64         `json:"total_duration_ms"`
	DurationBuckets   DurationBuckets `json:"duration_buckets"`
	FirstExecutedAt   *time.Time      `json:"first_executed_at,omitempty"`
	LastExecutedAt    *time.Time      `json:"last_executed_at,omitempty"`
}

type DurationBuckets struct {
	LessThan1MS     uint64 `json:"lt_1_ms"`
	From1To10MS     uint64 `json:"from_1_to_10_ms"`
	From10To100MS   uint64 `json:"from_10_to_100_ms"`
	From100MSTo1S   uint64 `json:"from_100_ms_to_1_s"`
	From1To10S      uint64 `json:"from_1_to_10_s"`
	GreaterEqual10S uint64 `json:"gte_10_s"`
}

func (buckets *DurationBuckets) Observe(duration time.Duration) {
	switch {
	case duration < time.Millisecond:
		buckets.LessThan1MS++
	case duration < 10*time.Millisecond:
		buckets.From1To10MS++
	case duration < 100*time.Millisecond:
		buckets.From10To100MS++
	case duration < time.Second:
		buckets.From100MSTo1S++
	case duration < 10*time.Second:
		buckets.From1To10S++
	default:
		buckets.GreaterEqual10S++
	}
}

func (buckets DurationBuckets) CumulativeSeconds() map[float64]uint64 {
	lessThan1MS := buckets.LessThan1MS
	lessThan10MS := lessThan1MS + buckets.From1To10MS
	lessThan100MS := lessThan10MS + buckets.From10To100MS
	lessThan1S := lessThan100MS + buckets.From100MSTo1S
	lessThan10S := lessThan1S + buckets.From1To10S
	return map[float64]uint64{
		0.001: lessThan1MS,
		0.01:  lessThan10MS,
		0.1:   lessThan100MS,
		1:     lessThan1S,
		10:    lessThan10S,
	}
}

type queryMetricKey struct {
	Datasource string
	Operation  string
}

type QueryMetric struct {
	Datasource      string
	Operation       string
	Executions      uint64
	Errors          uint64
	Running         uint64
	ConcurrentMax   uint64
	TotalRows       int64
	MaxRows         int64
	TotalDuration   time.Duration
	MinDuration     time.Duration
	MaxDuration     time.Duration
	FirstExecutedAt time.Time
	LastExecutedAt  time.Time
	DurationBuckets DurationBuckets
}

func (m *Module) QueryStarted(_ context.Context, activity gormx.QueryActivity) {
	m.sqlMu.Lock()
	defer m.sqlMu.Unlock()
	metric := m.queryMetric(activity.Datasource, activity.Operation)
	metric.Running++
	if metric.Running > metric.ConcurrentMax {
		metric.ConcurrentMax = metric.Running
	}
	m.runningSQL++
	if m.runningSQL > m.maxRunning {
		m.maxRunning = m.runningSQL
	}
}

func (m *Module) QueryFinished(_ context.Context, activity gormx.QueryActivity) {
	m.sqlMu.Lock()
	defer m.sqlMu.Unlock()
	key := queryMetricKey{Datasource: activity.Datasource, Operation: activity.Operation}
	metric := m.queryTotals[key]
	if metric != nil && metric.Running > 0 {
		metric.Running--
	}
	if metric != nil && metric.Executions == 0 && metric.Running == 0 {
		delete(m.queryTotals, key)
	}
	if m.runningSQL > 0 {
		m.runningSQL--
	}
}

func (m *Module) ObserveQuery(ctx context.Context, event gormx.QueryEvent) {
	statement := truncateSQL(strings.TrimSpace(event.Statement), maxSQLTemplateBytes)
	if statement == "" {
		return
	}
	fingerprint := sqlFingerprint(event.Operation + "\x00" + event.Table + "\x00" + statement)
	key := event.Datasource + "\x00" + fingerprint
	m.sqlMu.Lock()
	defer m.sqlMu.Unlock()
	stat := m.sqlStats[key]
	if stat == nil {
		if len(m.sqlStats) >= m.config.SQLCapacity {
			m.evictOldestSQL()
		}
		stat = &queryStat{
			Datasource: event.Datasource, Fingerprint: fingerprint,
			Operation: event.Operation, Table: event.Table, Statement: statement,
		}
		m.sqlStats[key] = stat
	}
	stat.Executions++
	if event.Err != nil {
		stat.Errors++
		stat.LastErrorClass = classifyQueryError(event.Err)
		stat.LastErrorAt = event.StartedAt
	}
	if event.Rows > 0 {
		stat.TotalRows += event.Rows
		if event.Rows > stat.MaxRows {
			stat.MaxRows = event.Rows
		}
	}
	stat.TotalDuration += event.Duration
	if stat.Executions == 1 || event.Duration < stat.MinDuration {
		stat.MinDuration = event.Duration
	}
	if event.Duration > stat.MaxDuration {
		stat.MaxDuration = event.Duration
	}
	if stat.FirstExecutedAt.IsZero() || event.StartedAt.Before(stat.FirstExecutedAt) {
		stat.FirstExecutedAt = event.StartedAt
	}
	stat.LastExecutedAt = event.StartedAt
	stat.DurationBuckets.Observe(event.Duration)

	metric := m.queryMetric(event.Datasource, event.Operation)
	metric.Executions++
	metric.TotalDuration += event.Duration
	if metric.Executions == 1 || event.Duration < metric.MinDuration {
		metric.MinDuration = event.Duration
	}
	if event.Duration > metric.MaxDuration {
		metric.MaxDuration = event.Duration
	}
	if metric.FirstExecutedAt.IsZero() || event.StartedAt.Before(metric.FirstExecutedAt) {
		metric.FirstExecutedAt = event.StartedAt
	}
	metric.LastExecutedAt = event.StartedAt
	metric.DurationBuckets.Observe(event.Duration)
	if event.Rows > 0 {
		metric.TotalRows += event.Rows
		if event.Rows > metric.MaxRows {
			metric.MaxRows = event.Rows
		}
	}
	if event.Err != nil {
		metric.Errors++
	}

	if event.Duration >= m.config.SlowThreshold {
		traceID := trace.SpanContextFromContext(ctx).TraceID()
		sample := SlowQuery{
			Datasource: event.Datasource, Fingerprint: fingerprint,
			Operation: event.Operation, Table: event.Table, Statement: statement,
			DurationMS: milliseconds(event.Duration), Rows: event.Rows,
			Failed: event.Err != nil, ErrorClass: classifyQueryError(event.Err), ExecutedAt: event.StartedAt,
		}
		if traceID.IsValid() {
			sample.TraceID = traceID.String()
		}
		if len(m.slowSQL) >= m.config.SlowCapacity {
			copy(m.slowSQL, m.slowSQL[1:])
			m.slowSQL[len(m.slowSQL)-1] = sample
		} else {
			m.slowSQL = append(m.slowSQL, sample)
		}
	}
}

func (m *Module) queryMetric(datasource, operation string) *QueryMetric {
	key := queryMetricKey{Datasource: datasource, Operation: operation}
	metric := m.queryTotals[key]
	if metric == nil {
		metric = &QueryMetric{Datasource: datasource, Operation: operation}
		m.queryTotals[key] = metric
	}
	return metric
}

func classifyQueryError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, gorm.ErrRecordNotFound):
		return "not_found"
	case errors.Is(err, gorm.ErrDuplicatedKey):
		return "duplicate_key"
	case errors.Is(err, gorm.ErrForeignKeyViolated):
		return "foreign_key"
	case errors.Is(err, gorm.ErrInvalidTransaction):
		return "invalid_transaction"
	default:
		return "database_error"
	}
}

func (m *Module) evictOldestSQL() {
	var oldestKey string
	var oldest time.Time
	for key, stat := range m.sqlStats {
		if oldestKey == "" || stat.LastExecutedAt.Before(oldest) {
			oldestKey, oldest = key, stat.LastExecutedAt
		}
	}
	delete(m.sqlStats, oldestKey)
}

func (m *Module) SQLReport() SQLReport {
	m.sqlMu.RLock()
	defer m.sqlMu.RUnlock()
	report := SQLReport{
		GeneratedAt: time.Now().UTC(), StatisticsSince: m.statisticsSince,
		Capacity: m.config.SQLCapacity, SlowCapacity: m.config.SlowCapacity,
		SlowThresholdMS: milliseconds(m.config.SlowThreshold),
		Tracked:         len(m.sqlStats), Operations: make([]OperationStat, 0, len(m.queryTotals)),
		Statements:  make([]SQLStat, 0, len(m.sqlStats)),
		SlowQueries: append([]SlowQuery{}, m.slowSQL...),
	}
	report.Summary.Running = m.runningSQL
	report.Summary.ConcurrentMax = m.maxRunning
	summaryMinimumSet := false
	for _, metric := range m.queryTotals {
		average := averageDuration(metric.TotalDuration, metric.Executions)
		operation := OperationStat{
			Datasource: metric.Datasource, Operation: metric.Operation,
			Executions: metric.Executions, Successes: metric.Executions - metric.Errors,
			Errors: metric.Errors, ErrorRate: errorRate(metric.Errors, metric.Executions),
			Running: metric.Running, ConcurrentMax: metric.ConcurrentMax,
			TotalRows: metric.TotalRows, MaxRows: metric.MaxRows,
			MinimumDurationMS: milliseconds(metric.MinDuration), AverageDurationMS: milliseconds(average),
			MaxDurationMS: milliseconds(metric.MaxDuration), TotalDurationMS: milliseconds(metric.TotalDuration),
			DurationBuckets: metric.DurationBuckets,
			FirstExecutedAt: timePointer(metric.FirstExecutedAt), LastExecutedAt: timePointer(metric.LastExecutedAt),
		}
		report.Operations = append(report.Operations, operation)
		report.Summary.Executions += metric.Executions
		report.Summary.Errors += metric.Errors
		report.Summary.TotalRows += metric.TotalRows
		report.Summary.TotalDurationMS += milliseconds(metric.TotalDuration)
		if metric.Executions > 0 && (!summaryMinimumSet || milliseconds(metric.MinDuration) < report.Summary.MinimumDurationMS) {
			report.Summary.MinimumDurationMS = milliseconds(metric.MinDuration)
			summaryMinimumSet = true
		}
		if milliseconds(metric.MaxDuration) > report.Summary.MaxDurationMS {
			report.Summary.MaxDurationMS = milliseconds(metric.MaxDuration)
		}
	}
	report.Summary.Successes = report.Summary.Executions - report.Summary.Errors
	report.Summary.ErrorRate = errorRate(report.Summary.Errors, report.Summary.Executions)
	if report.Summary.Executions > 0 {
		report.Summary.AverageDurationMS = report.Summary.TotalDurationMS / float64(report.Summary.Executions)
	}
	for _, stat := range m.sqlStats {
		average := averageDuration(stat.TotalDuration, stat.Executions)
		statementErrorRate := 0.0
		if stat.Executions > 0 {
			statementErrorRate = errorRate(stat.Errors, stat.Executions)
		}
		report.Statements = append(report.Statements, SQLStat{
			Datasource: stat.Datasource, Fingerprint: stat.Fingerprint,
			Operation: stat.Operation, Table: stat.Table, Statement: stat.Statement,
			Executions: stat.Executions, Successes: stat.Executions - stat.Errors,
			Errors: stat.Errors, ErrorRate: statementErrorRate, TotalRows: stat.TotalRows, MaxRows: stat.MaxRows,
			MinimumDurationMS: milliseconds(stat.MinDuration), AverageDurationMS: milliseconds(average),
			MaxDurationMS: milliseconds(stat.MaxDuration), TotalDurationMS: milliseconds(stat.TotalDuration),
			DurationBuckets: stat.DurationBuckets, FirstExecutedAt: stat.FirstExecutedAt,
			LastExecutedAt: stat.LastExecutedAt, LastErrorClass: stat.LastErrorClass,
			LastErrorAt: timePointer(stat.LastErrorAt),
		})
	}
	sort.Slice(report.Operations, func(i, j int) bool {
		if report.Operations[i].TotalDurationMS == report.Operations[j].TotalDurationMS {
			if report.Operations[i].Datasource == report.Operations[j].Datasource {
				return report.Operations[i].Operation < report.Operations[j].Operation
			}
			return report.Operations[i].Datasource < report.Operations[j].Datasource
		}
		return report.Operations[i].TotalDurationMS > report.Operations[j].TotalDurationMS
	})
	sort.Slice(report.Statements, func(i, j int) bool {
		if report.Statements[i].TotalDurationMS == report.Statements[j].TotalDurationMS {
			return report.Statements[i].Fingerprint < report.Statements[j].Fingerprint
		}
		return report.Statements[i].TotalDurationMS > report.Statements[j].TotalDurationMS
	})
	sort.Slice(report.SlowQueries, func(i, j int) bool {
		return report.SlowQueries[i].ExecutedAt.After(report.SlowQueries[j].ExecutedAt)
	})
	return report
}

func errorRate(errors, executions uint64) float64 {
	if executions == 0 {
		return 0
	}
	return float64(errors) / float64(executions)
}

func averageDuration(total time.Duration, count uint64) time.Duration {
	if count == 0 {
		return 0
	}
	return total / time.Duration(count)
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	clone := value
	return &clone
}

func (m *Module) QueryMetrics() []QueryMetric {
	m.sqlMu.RLock()
	defer m.sqlMu.RUnlock()
	result := make([]QueryMetric, 0, len(m.queryTotals))
	for _, metric := range m.queryTotals {
		result = append(result, *metric)
	}
	return result
}

func sqlFingerprint(statement string) string {
	hash := sha256.Sum256([]byte(statement))
	return hex.EncodeToString(hash[:8])
}

func truncateSQL(statement string, limit int) string {
	if len(statement) <= limit {
		return statement
	}
	for limit > 0 && !utf8.RuneStart(statement[limit]) {
		limit--
	}
	return statement[:limit]
}
