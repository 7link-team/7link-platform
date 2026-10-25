package agents

import (
	"context"
	"errors"
	"math"
	"net/http"
	"slices"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

var PerformanceWorkTypes = []string{"FEATURE", "BUG", "CHORE", "DOC", "RESEARCH", "DESIGN", "TEST", "TASK", "RISK", "INCIDENT", "CONTENT"}

type AgentPerformanceMeasures struct {
	WorkItemType         string   `json:"work_item_type"`
	FirstDecisions       int64    `json:"fpa_n"`
	FirstPassAccepts     int64    `json:"first_pass_accepts"`
	FPA                  *float64 `json:"fpa"`
	Claims               int64    `json:"claims"`
	ReworkDecisions      int64    `json:"rework_decisions"`
	ReworkRate           *float64 `json:"rework_rate"`
	AcceptedItems        int64    `json:"accepted_items"`
	CostOnAcceptedMicros *int64   `json:"cost_on_accepted_micros"`
	CPAMicros            *int64   `json:"cpa_micros"`
}

type AgentPerformance struct {
	AgentPerformanceMeasures
	WindowDays int                        `json:"window_days"`
	ComputedAt time.Time                  `json:"computed_at"`
	Scope      string                     `json:"scope"`
	ByType     []AgentPerformanceMeasures `json:"by_type"`
	Load       struct {
		ActiveItems       int64 `json:"active_items"`
		MaxParallelItems  int   `json:"max_parallel_items"`
		RunningExecutions int64 `json:"running_executions"`
	} `json:"load"`
}

type performanceSources struct {
	read    func(context.Context, *gorm.DB, guid.ID, guid.ID, int, string, time.Time) ([]AgentPerformanceMeasures, error)
	running func(context.Context, *gorm.DB, guid.ID, guid.ID) (int64, error)
	work    delivery.AgentPerformanceLoad
}

// Providers retain their own authorization and storage boundaries. Missing
// providers fail explicitly; zero is reserved for a genuinely empty scope.
type AgentPerformanceConnector struct {
	BindHistory func(func(context.Context, *gorm.DB, guid.ID, guid.ID, int, string, time.Time) ([]AgentPerformanceMeasures, error)) error
	BindRunning func(func(context.Context, *gorm.DB, guid.ID, guid.ID) (int64, error)) error
}

func (m *Module) providePerformance(s *module.Services) error {
	return module.Provide(s, AgentPerformanceConnector{
		BindHistory: func(f func(context.Context, *gorm.DB, guid.ID, guid.ID, int, string, time.Time) ([]AgentPerformanceMeasures, error)) error {
			if f == nil || m.performance.read != nil {
				return ErrConflict
			}
			m.performance.read = f
			return nil
		},
		BindRunning: func(f func(context.Context, *gorm.DB, guid.ID, guid.ID) (int64, error)) error {
			if f == nil || m.performance.running != nil {
				return ErrConflict
			}
			m.performance.running = f
			return nil
		},
	})
}

func (m *Module) injectPerformance(s *module.Services) error {
	v, err := module.Resolve[delivery.AgentPerformanceLoad](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	m.performance.work = v
	return err
}

func performanceRatios(v *AgentPerformanceMeasures) {
	if v.FirstDecisions >= 5 {
		f := float64(v.FirstPassAccepts) / float64(v.FirstDecisions)
		v.FPA = &f
	}
	if v.Claims > 0 {
		r := float64(v.ReworkDecisions) / float64(v.Claims)
		v.ReworkRate = &r
	}
	if v.AcceptedItems > 0 && v.CostOnAcceptedMicros != nil {
		c := *v.CostOnAcceptedMicros / v.AcceptedItems
		v.CPAMicros = &c
	}
}

func (m *Module) performanceOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, days int, kind string) (AgentPerformance, error) {
	var out AgentPerformance
	if days != 30 && days != 90 || kind != "" && !slices.Contains(PerformanceWorkTypes, kind) {
		return out, ErrInvalid
	}
	if _, err := m.authorize(ctx, tx, tenant, agent, "agent_read", true); err != nil {
		return out, err
	}
	if m.performance.read == nil || m.performance.running == nil || m.performance.work.CountOn == nil || m.profiles.ReadOn == nil {
		return out, ErrUnavailable
	}
	out.WindowDays, out.ComputedAt, out.Scope = days, m.now(), "CURRENT_READER_VISIBLE_WORK_AND_EXECUTIONS"
	out.WorkItemType = kind
	rows, err := m.performance.read(ctx, tx, tenant, agent, days, kind, out.ComputedAt)
	if err != nil {
		return out, err
	}
	out.ByType = rows
	out.CostOnAcceptedMicros = new(int64)
	for i := range out.ByType {
		v := &out.ByType[i]
		performanceRatios(v)
		out.FirstDecisions += v.FirstDecisions
		out.FirstPassAccepts += v.FirstPassAccepts
		out.Claims += v.Claims
		out.ReworkDecisions += v.ReworkDecisions
		out.AcceptedItems += v.AcceptedItems
		if v.AcceptedItems > 0 {
			if v.CostOnAcceptedMicros == nil {
				out.CostOnAcceptedMicros = nil
			} else if out.CostOnAcceptedMicros != nil {
				if *v.CostOnAcceptedMicros < 0 || *out.CostOnAcceptedMicros > math.MaxInt64-*v.CostOnAcceptedMicros {
					return AgentPerformance{}, ErrUnavailable
				}
				*out.CostOnAcceptedMicros += *v.CostOnAcceptedMicros
			}
		}
	}
	performanceRatios(&out.AgentPerformanceMeasures)
	out.Load.ActiveItems, err = m.performance.work.CountOn(ctx, tx, tenant, agent)
	if err != nil {
		return out, err
	}
	out.Load.RunningExecutions, err = m.performance.running(ctx, tx, tenant, agent)
	if err != nil {
		return out, err
	}
	profile, err := m.profiles.ReadOn(ctx, tx, tenant, agent)
	if err != nil {
		return out, profileError(err)
	}
	out.Load.MaxParallelItems = profile.MaxParallelItems
	return out, nil
}

type AgentPerformanceInput struct {
	AgentPath
	WindowDays   int    `query:"window_days" default:"30" enum:"30,90"`
	WorkItemType string `query:"work_item_type" enum:"FEATURE,BUG,CHORE,DOC,RESEARCH,DESIGN,TEST,TASK,RISK,INCIDENT,CONTENT"`
}

func (m *Module) registerPerformanceHTTP(api huma.API) {
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "agent_stats.read", Method: http.MethodGet, Path: agentPath + "/stats", Summary: "Read current authorized Agent performance and live load", Tags: []string{"tenants"}, Errors: []int{401, 403, 404, 422, 500, 503}}, func(ctx context.Context, in *AgentPerformanceInput) (*ProtocolBody[AgentPerformance], error) {
		t, a, err := pathIDs(ctx, in.AgentPath)
		if err != nil {
			return nil, agentHTTPError(ctx, err)
		}
		return readHTTP(ctx, m, func(tx *gorm.DB) (AgentPerformance, error) {
			return m.performanceOn(ctx, tx, t, a, in.WindowDays, in.WorkItemType)
		})
	})
}
