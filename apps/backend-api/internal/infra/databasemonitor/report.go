package databasemonitor

import (
	"errors"
	"time"
)

type Report struct {
	Status          string             `json:"status"`
	CheckedAt       time.Time          `json:"checked_at"`
	ProbeDurationMS float64            `json:"probe_duration_ms"`
	Datasources     []DatasourceReport `json:"datasources"`
}

type DatasourceReport struct {
	Name                string     `json:"name"`
	Readable            bool       `json:"readable"`
	Writable            bool       `json:"writable"`
	Status              string     `json:"status"`
	CheckedAt           time.Time  `json:"checked_at"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	ProbeDurationMS     float64    `json:"probe_duration_ms"`
	ConsecutiveFailures uint64     `json:"consecutive_failures"`
	Pool                PoolReport `json:"pool"`
}

type PoolReport struct {
	MaxOpenConnections int     `json:"max_open_connections"`
	OpenConnections    int     `json:"open_connections"`
	InUse              int     `json:"in_use"`
	Idle               int     `json:"idle"`
	WaitCount          int64   `json:"wait_count"`
	WaitDurationMS     float64 `json:"wait_duration_ms"`
	MaxIdleClosed      int64   `json:"max_idle_closed"`
	MaxIdleTimeClosed  int64   `json:"max_idle_time_closed"`
	MaxLifetimeClosed  int64   `json:"max_lifetime_closed"`
}

func (m *Module) Report() Report {
	snapshot := m.Snapshot()
	report := Report{
		Status: statusUnknown, CheckedAt: snapshot.CheckedAt,
		ProbeDurationMS: milliseconds(snapshot.ProbeDuration),
		Datasources:     make([]DatasourceReport, 0, len(snapshot.Datasources)),
	}
	stale := errors.Is(m.snapshotError(snapshot), ErrSnapshotStale)
	anyDown := false
	for _, item := range snapshot.Datasources {
		status := statusServing
		if stale {
			status = statusStale
		} else if !item.Up {
			status = statusNotServing
			anyDown = true
		}
		var lastSuccess *time.Time
		if !item.LastSuccessAt.IsZero() {
			value := item.LastSuccessAt
			lastSuccess = &value
		}
		pool := item.Pool.Pool
		report.Datasources = append(report.Datasources, DatasourceReport{
			Name: item.Name, Readable: item.Readable, Writable: item.Writable, Status: status,
			CheckedAt: item.CheckedAt, LastSuccessAt: lastSuccess,
			ProbeDurationMS:     milliseconds(item.ProbeDuration),
			ConsecutiveFailures: item.ConsecutiveFailures,
			Pool: PoolReport{
				MaxOpenConnections: pool.MaxOpenConnections,
				OpenConnections:    pool.OpenConnections,
				InUse:              pool.InUse, Idle: pool.Idle, WaitCount: pool.WaitCount,
				WaitDurationMS: milliseconds(pool.WaitDuration),
				MaxIdleClosed:  pool.MaxIdleClosed, MaxIdleTimeClosed: pool.MaxIdleTimeClosed,
				MaxLifetimeClosed: pool.MaxLifetimeClosed,
			},
		})
	}
	switch {
	case snapshot.CheckedAt.IsZero():
		report.Status = statusUnknown
	case stale:
		report.Status = statusStale
	case snapshot.RoutingError() != nil:
		report.Status = statusNotServing
	case anyDown:
		report.Status = statusDegraded
	default:
		report.Status = statusServing
	}
	return report
}

const (
	statusUnknown    = "UNKNOWN"
	statusServing    = "SERVING"
	statusDegraded   = "DEGRADED"
	statusNotServing = "NOT_SERVING"
	statusStale      = "STALE"
)

func milliseconds(duration time.Duration) float64 {
	return float64(duration.Nanoseconds()) / 1e6
}
