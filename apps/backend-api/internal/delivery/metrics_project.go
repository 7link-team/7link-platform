package delivery

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"math/big"
	"slices"
	"strconv"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// MetricRatio retains exact source counts. A missing denominator is never 100%.
type MetricRatio struct {
	MissingDenominator int     `json:"missing_denominator"`
	Numerator          *string `json:"numerator"`
	Denominator        *string `json:"denominator"`
	Value              *string `json:"value"`
	Missing            int     `json:"missing"`
	MissingRate        *string `json:"missing_rate"`
	LowerBound         bool    `json:"lower_bound"`
	Reason             string  `json:"reason,omitempty"`
}
type MetricDistribution struct {
	Samples int     `json:"samples"`
	Sum     string  `json:"sum"`
	Mean    *string `json:"mean"`
	P50     *string `json:"p50"`
	P90     *string `json:"p90"`
	P95     *string `json:"p95"`
	Unit    string  `json:"unit"`
}
type ProjectMetricSnapshot struct {
	ProjectID                  guid.ID `json:"project_id"`
	roundSamples, cycleSamples []int64
	WorkItems                  int                    `json:"work_items"`
	ScopeChanged               int                    `json:"scope_changed"`
	KPIs                       map[string]MetricRatio `json:"kpis"`
	InternalRounds             MetricDistribution     `json:"internal_rounds"`
	ReworkCycle                MetricDistribution     `json:"rework_cycle"`
	Versions                   map[string][]string    `json:"versions"`
	MissingSources             []string               `json:"missing_sources"`
}
type MetricSelection struct {
	Model    string `json:"model"`
	Verifier string `json:"verifier"`
	Workflow string `json:"workflow"`
}

type ProjectMetrics struct {
	Selection  MetricSelection         `json:"selection"`
	From       time.Time               `json:"from"`
	To         time.Time               `json:"to"`
	ComputedAt time.Time               `json:"computed_at"`
	Version    string                  `json:"version"`
	Cohort     string                  `json:"cohort"`
	Projects   []ProjectMetricSnapshot `json:"projects"`
	Team       ProjectMetricSnapshot   `json:"team"`
}
type MetricExecution struct {
	ID, WorkItemID         guid.ID
	ParentID               *guid.ID
	CreatedAt              time.Time
	Model, WorkflowVersion string
}
type MetricClientReceipt struct {
	PackageID, WorkItemID guid.ID
	ItemKey               string
	At                    time.Time
	Accepted              bool
	Package               bool
}
type MetricClientHistory struct {
	Receipts []MetricClientReceipt
	Complete bool
}
type DeliveryMetricsSources struct {
	Executions func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) ([]MetricExecution, error)
	Client     func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) (MetricClientHistory, error)
}
type DeliveryMetricsSourcesConnector struct {
	BindExecutions func(func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) ([]MetricExecution, error)) error
	BindClient     func(func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) (MetricClientHistory, error)) error
}

func (m *Module) provideProjectMetrics(s *module.Services) error {
	return module.Provide[DeliveryMetricsSourcesConnector](s, DeliveryMetricsSourcesConnector{
		BindExecutions: func(f func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) ([]MetricExecution, error)) error {
			if f == nil || m.metricsSources.Executions != nil {
				return ErrInvalidArgument
			}
			m.metricsSources.Executions = f
			return nil
		},
		BindClient: func(f func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) (MetricClientHistory, error)) error {
			if f == nil || m.metricsSources.Client != nil {
				return ErrInvalidArgument
			}
			m.metricsSources.Client = f
			return nil
		},
	})
}
func metricRatio(n, d int64) MetricRatio { return metricBigRatio(big.NewInt(n), d) }
func metricBigRatio(n *big.Int, d int64) MetricRatio {
	ns, ds := n.String(), strconv.FormatInt(d, 10)
	out := MetricRatio{Numerator: &ns, Denominator: &ds}
	if d == 0 {
		out.Reason = "NO_SAMPLES"
		return out
	}
	v := new(big.Rat).SetFrac(n, big.NewInt(d)).FloatString(6)
	out.Value = &v
	return out
}
func metricMissing(out MetricRatio, missing, total int, reason string, lower bool) MetricRatio {
	out.MissingDenominator = total
	out.Missing = missing
	out.LowerBound = lower
	out.Reason = reason
	if total > 0 {
		v := new(big.Rat).SetFrac64(int64(missing), int64(total)).FloatString(6)
		out.MissingRate = &v
	}
	return out
}
func metricDistribution(values []int64, unit string) MetricDistribution {
	values = slices.Clone(values)
	slices.Sort(values)
	sum := new(big.Int)
	for _, v := range values {
		sum.Add(sum, big.NewInt(v))
	}
	out := MetricDistribution{Samples: len(values), Sum: sum.String(), Unit: unit}
	if len(values) == 0 {
		return out
	}
	mean := new(big.Rat).SetFrac(sum, big.NewInt(int64(len(values)))).FloatString(6)
	out.Mean = &mean
	quantile := func(p int) *string { v := strconv.FormatInt(values[(len(values)*p+99)/100-1], 10); return &v }
	out.P50 = quantile(50)
	out.P90 = quantile(90)
	out.P95 = quantile(95)
	return out
}
func metricIn(at, start, end time.Time) bool { return !at.Before(start) && at.Before(end) }

func (m *Module) projectMetricOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, start, end time.Time) (ProjectMetricSnapshot, error) {
	return m.projectMetricSelectedOn(ctx, tx, tenant, project, start, end, MetricSelection{})
}
func (m *Module) projectMetricSelectedOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, start, end time.Time, selection MetricSelection) (ProjectMetricSnapshot, error) {
	out := ProjectMetricSnapshot{ProjectID: project, KPIs: map[string]MetricRatio{}, Versions: map[string][]string{"model": {}, "verifier": {}, "workflow": {}}, MissingSources: []string{}}
	// Current principal, project and row policy precede every historical aggregate.
	// Execution sources use principal-before-project ordering as well.
	if err := m.lockStagePrincipalsOn(ctx, tx, authn.PrincipalIDFromContext(ctx), nil); err != nil {
		return out, err
	}
	if err := m.authorizeOn(ctx, tx, tenant, project, "project_read"); err != nil {
		return out, err
	}
	if err := m.authorizeOn(ctx, tx, tenant, project, "work_item_read"); err != nil {
		return out, err
	}
	policy, err := m.workPolicy(ctx, tx, tenant, project, "work_item_read", authn.PrincipalIDFromContext(ctx))
	if err != nil {
		return out, err
	}
	w := query.BindWorkItems(tx, m.service.names)
	scope, err := workScope(policy, project, w.CreatedBy)
	if err != nil {
		return out, err
	}
	stmt := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.IsContainer.Is(false), w.CreatedAt.Lt(end))
	if scope != nil {
		stmt = stmt.Where(scope)
	}
	work, err := stmt.Order(w.ID).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(work) > 10000 {
		return out, ErrUnavailable
	}

	if selection.Model != "" || selection.Workflow != "" || selection.Verifier != "" {
		candidateIDs := make([]guid.ID, len(work))
		for i, row := range work {
			candidateIDs[i] = row.ID
		}
		selected := map[guid.ID]bool{}
		if selection.Model != "" || selection.Workflow != "" {
			if m.metricsSources.Executions == nil {
				return out, ErrUnavailable
			}
			execs, e := m.metricsSources.Executions(ctx, tx, tenant, project, candidateIDs, start, end)
			if e != nil {
				return out, e
			}
			for _, execution := range execs {
				if (selection.Model == "" || execution.Model == selection.Model) && (selection.Workflow == "" || execution.WorkflowVersion == selection.Workflow) {
					selected[execution.WorkItemID] = true
				}
			}
		} else {
			for _, id := range candidateIDs {
				selected[id] = true
			}
		}
		if selection.Verifier != "" && len(candidateIDs) > 0 {
			values := make([]driver.Valuer, len(candidateIDs))
			for i, id := range candidateIDs {
				values[i] = id
			}
			v := query.BindVerificationRuns(tx, m.service.names)
			rows, e := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ProjectID.Eq(project), v.WorkItemID.In(values...), v.CreatedAt.Gte(start), v.CreatedAt.Lt(end), v.ReviewerModel.Eq(selection.Verifier)).Limit(10001).Find()
			if e != nil {
				return out, e
			}
			if len(rows) > 10000 {
				return out, ErrUnavailable
			}
			verifierIDs := map[guid.ID]bool{}
			for _, row := range rows {
				verifierIDs[row.WorkItemID] = true
			}
			for id := range selected {
				if !verifierIDs[id] {
					delete(selected, id)
				}
			}
		}
		filtered := work[:0]
		for _, row := range work {
			if selected[row.ID] {
				filtered = append(filtered, row)
			}
		}
		work = filtered
	}
	out.WorkItems = len(work)
	ids := make([]driver.Valuer, len(work))
	workIDs := make([]guid.ID, len(work))
	for i, row := range work {
		ids[i] = row.ID
		workIDs[i] = row.ID

	}
	// Every key exists even when a genuine source is missing.
	for _, key := range []string{"L2_FIRST_PASS", "L3_FIRST_ACCEPTANCE", "L4_PACKAGE_FIRST_ACCEPTANCE", "L4_ITEM_FIRST_ACCEPTANCE", "COST_PER_ACCEPTED_MICROS", "KNOWN_COST_LOWER_BOUND_MICROS", "ATTENTION_MIN_PER_ACCEPTED", "HUMAN_TIME_MISSING", "EVIDENCE_COVERAGE", "CRITERIA_COVERAGE", "L3_ESCAPE", "L4_ESCAPE", "HUMAN_OVERRIDE"} {
		out.KPIs[key] = metricRatio(0, 0)
	}
	out.InternalRounds = metricDistribution(nil, "rounds")
	out.ReworkCycle = metricDistribution(nil, "ms")
	if len(ids) == 0 {
		return out, nil
	}
	changes := query.BindChanges(tx, m.service.names)
	changed, err := changes.WithContext(ctx).Where(changes.TenantID.Eq(tenant), changes.ProjectID.Eq(project), changes.CreatedAt.Gte(start), changes.CreatedAt.Lt(end)).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(changed) > 10000 {
		return out, ErrUnavailable
	}
	if len(changed) > 0 {
		changeIDs := make([]driver.Valuer, len(changed))
		for i, row := range changed {
			changeIDs[i] = row.ID
		}
		impact := query.BindChangeImpacts(tx, m.service.names)
		impacts, e := impact.WithContext(ctx).Where(impact.TenantID.Eq(tenant), impact.ProjectID.Eq(project), impact.WorkItemID.In(ids...), impact.ChangeRecordID.In(changeIDs...)).Limit(10001).Find()
		if e != nil {
			return out, e
		}
		if len(impacts) > 10000 {
			return out, ErrUnavailable
		}
		affected := map[guid.ID]bool{}
		for _, row := range impacts {
			affected[row.WorkItemID] = true
		}
		out.ScopeChanged = len(affected)
	}
	marker := query.BindReportQualityObservations(tx, m.service.names)
	observed, err := marker.WithContext(ctx).Where(marker.Singleton.Eq(1)).First()
	if err != nil {
		return out, err
	}
	historyMissing := start.Before(observed.ObservedSince)

	r := query.BindResultVersions(tx, m.service.names)
	results, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.WorkItemID.In(ids...), r.CreatedAt.Lt(end)).Order(r.CreatedAt, r.ID).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(results) > 10000 {
		return out, ErrUnavailable
	}
	a := query.BindResultActions(tx, m.service.names)
	actions, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.WorkItemID.In(ids...), a.CreatedAt.Lt(end)).Order(a.CreatedAt, a.ID).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(actions) > 10000 {
		return out, ErrUnavailable
	}
	v := query.BindVerificationRuns(tx, m.service.names)
	runs, err := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ProjectID.Eq(project), v.WorkItemID.In(ids...), v.CreatedAt.Lt(end)).Order(v.CreatedAt, v.ID).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(runs) > 10000 {
		return out, ErrUnavailable
	}
	q := query.BindReportQualityReceipts(tx, m.service.names)
	quality, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.In(ids...), q.OccurredAt.Lt(end)).Order(q.OccurredAt, q.ID).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(quality) > 10000 {
		return out, ErrUnavailable
	}
	accepted, firstDecision := map[guid.ID]bool{}, map[guid.ID]bool{}
	var acceptedN, l3N, l3D, attentionMissing, attentionTotal int64
	attentionMS := new(big.Int)
	for _, row := range actions {
		if (row.Kind == "ACCEPT" || (row.Kind == "RETURN" && row.HandoffID == nil)) && !firstDecision[row.WorkItemID] {
			firstDecision[row.WorkItemID] = true
			if metricIn(row.CreatedAt, start, end) {
				l3D++
				if row.Kind == "ACCEPT" && row.Verdict == "PASSED" {
					l3N++
				}
			}
		}
		if row.Kind == "ACCEPT" && row.Verdict == "PASSED" && !accepted[row.WorkItemID] {
			accepted[row.WorkItemID] = true
			if metricIn(row.CreatedAt, start, end) {
				acceptedN++
			}
		}
		if metricIn(row.CreatedAt, start, end) && (row.Kind == "ACCEPT" || row.Kind == "RETURN" || row.Kind == "CHECK") {
			attentionTotal++
			if row.AttentionMS == nil {
				attentionMissing++
			}
		}
	}
	out.KPIs["L3_FIRST_ACCEPTANCE"] = metricRatio(l3N, l3D)
	// Each measured session contributes once, even when CHECK and ACCEPT actions
	// both contain the same cumulative attention receipt. Cross-window sessions
	// cannot be apportioned from a cumulative counter and remain outside this lower bound.
	sessions := query.BindAttentionSessions(tx, m.service.names)
	measured, err := sessions.WithContext(ctx).Where(sessions.TenantID.Eq(tenant), sessions.ProjectID.Eq(project), sessions.WorkItemID.In(ids...), sessions.StartedAt.Gte(start), sessions.EndedAt.Gte(start), sessions.EndedAt.Lt(end)).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(measured) > 10000 {
		return out, ErrUnavailable
	}
	for _, session := range measured {
		if session.ActiveMS < 0 {
			return out, ErrUnavailable
		}
		attentionMS.Add(attentionMS, big.NewInt(session.ActiveMS))
	}
	mins := new(big.Rat).SetFrac(attentionMS, big.NewInt(60000))
	numerator := mins.FloatString(6)
	att := metricBigRatio(attentionMS, acceptedN*60000)
	att.Numerator = &numerator
	denominator := strconv.FormatInt(acceptedN, 10)
	att.Denominator = &denominator
	out.KPIs["ATTENTION_MIN_PER_ACCEPTED"] = metricMissing(att, int(attentionMissing), int(attentionTotal), "MEASURED_REVIEW_ONLY", true)
	out.KPIs["HUMAN_TIME_MISSING"] = metricRatio(attentionMissing, attentionTotal)
	qaStart, qaPass := map[guid.ID]time.Time{}, map[guid.ID]*model.ReportQualityReceipt{}
	roundMax := map[guid.ID]int64{}
	roundKeys := map[guid.ID]map[string]bool{}
	verifiers := map[string]bool{}
	for _, row := range runs {
		if row.Stage == "AUTOMATED" || row.Stage == "AGENT_TEST" || row.Stage == "AGENT_ACCEPTANCE" {
			if _, ok := qaStart[row.WorkItemID]; !ok {
				qaStart[row.WorkItemID] = row.CreatedAt
			}
			if metricIn(row.CreatedAt, start, end) {
				if roundKeys[row.WorkItemID] == nil {
					roundKeys[row.WorkItemID] = map[string]bool{}
				}
				roundKeys[row.WorkItemID][row.ResultID.String()+":"+strconv.Itoa(row.Round)] = true
				roundMax[row.WorkItemID] = int64(len(roundKeys[row.WorkItemID]))
				if row.ReviewerModel != "" {
					verifiers[row.ReviewerModel] = true
				}
			}
		}
	}
	for _, row := range quality {
		if row.Kind == "INTERNAL_QA_PASSED" {
			if _, ok := qaPass[row.WorkItemID]; !ok {
				qaPass[row.WorkItemID] = row
			}
		}
	}
	for _, row := range work {
		if at, ok := qaStart[row.ID]; ok && metricIn(at, start, end) && row.CreatedAt.Before(observed.ObservedSince) {
			historyMissing = true
		}
	}
	var l2N, l2D int64
	for id, at := range qaStart {
		if metricIn(at, start, end) {
			l2D++
			if p := qaPass[id]; p != nil && p.Rounds == 1 && !p.OccurredAt.Before(at) && p.OccurredAt.Before(end) && metricFirstQAPassed(id, runs, results, p.OccurredAt) {
				l2N++
			}
		}
	}
	out.KPIs["L2_FIRST_PASS"] = metricRatio(l2N, l2D)
	rounds := []int64{}
	for _, n := range roundMax {
		rounds = append(rounds, n)
	}
	out.InternalRounds = metricDistribution(rounds, "rounds")
	out.roundSamples = rounds
	for name := range verifiers {
		out.Versions["verifier"] = append(out.Versions["verifier"], name)
	}
	slices.Sort(out.Versions["verifier"])
	override := query.BindVerificationOverrides(tx, m.service.names)
	overrides, err := override.WithContext(ctx).Where(override.TenantID.Eq(tenant), override.ProjectID.Eq(project), override.WorkItemID.In(ids...), override.CreatedAt.Gte(start), override.CreatedAt.Lt(end)).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(overrides) > 10000 {
		return out, ErrUnavailable
	}
	overIDs := map[guid.ID]bool{}
	for _, o := range overrides {
		overIDs[o.WorkItemID] = true
	}
	overridePopulation := map[guid.ID]bool{}
	for id := range roundMax {
		overridePopulation[id] = true
	}
	for id := range overIDs {
		overridePopulation[id] = true
	}
	out.KPIs["HUMAN_OVERRIDE"] = metricRatio(int64(len(overIDs)), int64(len(overridePopulation)))
	// Coverage uses immutable result evidence and the baseline's criterion set.
	claimed, evidenced := map[string]bool{}, map[string]bool{}
	var criteriaN, criteriaD int64
	e := query.BindResultEvidence(tx, m.service.names)
	for _, row := range results {
		if !metricIn(row.CreatedAt, start, end) {
			continue
		}
		claimDay := row.WorkItemID.String() + ":" + row.CreatedAt.UTC().Format(time.DateOnly)
		claimed[claimDay] = true
		pins, er := e.WithContext(ctx).Where(e.TenantID.Eq(tenant), e.ProjectID.Eq(project), e.ResultID.Eq(row.ID)).Limit(10001).Find()
		if er != nil {
			return out, er
		}
		if len(pins) > 10000 {
			return out, ErrUnavailable
		}
		mapped := map[guid.ID]bool{}
		for _, pin := range pins {
			if len(pin.SHA256) == 64 && pin.BindingID > 0 && pin.VersionID > 0 {
				evidenced[claimDay] = true
				if pin.CriterionID != nil && pin.Coverage == "COVERED" {
					mapped[*pin.CriterionID] = true
				}
			}
		}
		var plan []workVerificationStep
		if er := json.Unmarshal([]byte(row.VerificationPlan), &plan); er != nil {
			return out, er
		}
		for _, criterion := range plan {
			criteriaD++
			if mapped[criterion.CriterionID] {
				criteriaN++
			}
		}
	}
	out.KPIs["EVIDENCE_COVERAGE"] = metricRatio(int64(len(evidenced)), int64(len(claimed)))
	out.KPIs["CRITERIA_COVERAGE"] = metricRatio(criteriaN, criteriaD)
	// State history includes internal rework, not merely Human result actions.
	t := query.BindWorkTransitions(tx, m.service.names)
	transitions, err := t.WithContext(ctx).Where(t.TenantID.Eq(tenant), t.ProjectID.Eq(project), t.WorkItemID.In(ids...), t.CreatedAt.Lt(end), t.ToState.In("REWORK", "ACCEPTED")).Order(t.CreatedAt, t.ID).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(transitions) > 10000 {
		return out, ErrUnavailable
	}
	cycleEvents := []HomeWorkTransition{}
	for _, event := range transitions {
		if event.ReasonCode == "SCOPE_REQUEST_CHANGES" {
			continue
		}
		cycleEvents = append(cycleEvents, HomeWorkTransition{WorkItemID: event.WorkItemID, At: event.CreatedAt, Accepted: event.ToState == "ACCEPTED"})
	}

	// Separate escape levels; only explicit quality receipts establish an escape.
	escapedH, escapedC, entered := map[guid.ID]bool{}, map[guid.ID]bool{}, map[guid.ID]bool{}
	for _, row := range quality {
		if !metricIn(row.OccurredAt, start, end) {
			continue
		}
		if row.Kind == "L3_ENTERED" && qaPass[row.WorkItemID] != nil && !qaPass[row.WorkItemID].OccurredAt.After(row.OccurredAt) {
			entered[row.WorkItemID] = true
		}
		if row.Kind == "ESCAPE_HUMAN" {
			escapedH[row.WorkItemID] = true
		}
		if row.Kind == "ESCAPE_CLIENT" {
			escapedC[row.WorkItemID] = true
		}
	}
	humanEscapes := int64(0)
	for id := range escapedH {
		if entered[id] {
			humanEscapes++
		}
	}
	out.KPIs["L3_ESCAPE"] = metricRatio(humanEscapes, int64(len(entered)))
	if m.metricsSources.Client != nil {
		history, er := m.metricsSources.Client(ctx, tx, tenant, project, workIDs, end)
		if er != nil {
			return out, er
		}
		var pn, pd, in, id int64
		seenItems := map[string]bool{}
		seenPackages := map[guid.ID]bool{}
		l4QA := map[guid.ID]bool{}
		for _, receipt := range history.Receipts {
			if !receipt.Package && !receipt.Accepted {
				cycleEvents = append(cycleEvents, HomeWorkTransition{WorkItemID: receipt.WorkItemID, At: receipt.At})
			}
			if receipt.Package {
				if !seenPackages[receipt.PackageID] {
					seenPackages[receipt.PackageID] = true
					if metricIn(receipt.At, start, end) {
						pd++
						if receipt.Accepted {
							pn++
						}
					}
				}
			} else {
				if !seenItems[receipt.ItemKey] {
					seenItems[receipt.ItemKey] = true
					if metricIn(receipt.At, start, end) {
						id++
						if receipt.Accepted {
							in++
						}
						if p := qaPass[receipt.WorkItemID]; p != nil && !p.OccurredAt.After(receipt.At) {
							l4QA[receipt.WorkItemID] = true
						}
					}
				}
			}
		}
		out.KPIs["L4_PACKAGE_FIRST_ACCEPTANCE"] = metricRatio(pn, pd)
		out.KPIs["L4_ITEM_FIRST_ACCEPTANCE"] = metricRatio(in, id)
		clientEscapes := int64(0)
		for work := range escapedC {
			if l4QA[work] {
				clientEscapes++
			}
		}
		out.KPIs["L4_ESCAPE"] = metricRatio(clientEscapes, int64(len(l4QA)))
		if !history.Complete {
			out.MissingSources = append(out.MissingSources, "LEGACY_CLIENT_ITEM_IDENTITY")
			for _, key := range []string{"L4_PACKAGE_FIRST_ACCEPTANCE", "L4_ITEM_FIRST_ACCEPTANCE", "L4_ESCAPE"} {
				value := out.KPIs[key]
				value.Value = nil
				value.Reason = "INCOMPLETE_CLIENT_HISTORY"
				out.KPIs[key] = value
			}
		}
	} else {
		out.MissingSources = append(out.MissingSources, "CLIENT_RECEIPTS")
	}
	slices.SortStableFunc(cycleEvents, func(a, b HomeWorkTransition) int {
		if order := a.At.Compare(b.At); order != 0 {
			return order
		}
		if a.Accepted == b.Accepted {
			return 0
		}
		if a.Accepted {
			return 1
		}
		return -1
	})
	opened := map[guid.ID]time.Time{}
	cycles := []int64{}
	for _, event := range cycleEvents {
		if !event.Accepted {
			if _, ok := opened[event.WorkItemID]; !ok {
				opened[event.WorkItemID] = event.At
			}
			continue
		}
		if at, ok := opened[event.WorkItemID]; ok {
			if metricIn(event.At, start, end) {
				cycles = append(cycles, event.At.Sub(at).Milliseconds())
			}
			delete(opened, event.WorkItemID)
		}
	}
	out.ReworkCycle = metricDistribution(cycles, "ms")
	out.cycleSamples = cycles
	cost := new(big.Int)
	missingCost, totalCost := 0, 0
	if m.metricsSources.Executions != nil && m.acceptanceSettlements != nil {
		execs, er := m.metricsSources.Executions(ctx, tx, tenant, project, workIDs, start, end)
		if er != nil {
			return out, er
		}
		models, workflows := map[string]bool{}, map[string]bool{}
		for _, execution := range execs {
			totalCost++
			value, known, er := m.acceptanceSettlements(ctx, tx, tenant, project, []guid.ID{execution.ID})
			if er != nil {
				return out, er
			}
			if !known {
				missingCost++
			} else {
				if value < 0 {
					return out, ErrUnavailable
				}
				cost.Add(cost, big.NewInt(value))
			}
			if execution.Model != "" {
				models[execution.Model] = true
			}
			if execution.WorkflowVersion != "" {
				workflows[execution.WorkflowVersion] = true
			}
		}
		for name := range models {
			out.Versions["model"] = append(out.Versions["model"], name)
		}
		for name := range workflows {
			out.Versions["workflow"] = append(out.Versions["workflow"], name)
		}
	} else {
		missingCost = 0
		totalCost = 0
		out.MissingSources = append(out.MissingSources, "EXECUTION_SETTLEMENTS")
	}
	for name := range out.Versions {
		slices.Sort(out.Versions[name])
		if len(out.Versions[name]) == 0 {
			out.MissingSources = append(out.MissingSources, "VERSION_"+name)
		}
	}
	lower := metricMissing(metricBigRatio(cost, acceptedN), missingCost, totalCost, "EXCLUDES_UNPRICED_LABOR_MACHINE_PLATFORM", true)
	if m.metricsSources.Executions == nil || m.acceptanceSettlements == nil {
		lower.Value = nil
		lower.Numerator = nil
		lower.Reason = "EXECUTION_SETTLEMENTS"
	}
	out.KPIs["KNOWN_COST_LOWER_BOUND_MICROS"] = lower
	full := metricMissing(lower, len(work), len(work), "LABOR_MACHINE_PLATFORM_ALLOCATION_UNAVAILABLE", true)
	full.Value = nil
	full.Reason = "LABOR_MACHINE_PLATFORM_ALLOCATION_UNAVAILABLE"
	out.KPIs["COST_PER_ACCEPTED_MICROS"] = full
	out.MissingSources = append(out.MissingSources, "LABOR_PRICING", "MACHINE_PLATFORM_ALLOCATION")
	if historyMissing {
		out.MissingSources = append(out.MissingSources, "QUALITY_OBSERVATION_BOUNDARY")
		for _, key := range []string{"L2_FIRST_PASS", "L3_ESCAPE", "L4_ESCAPE"} {
			value := out.KPIs[key]
			value.Value = nil
			value.Reason = "INCOMPLETE_QUALITY_HISTORY"
			out.KPIs[key] = value
		}
	}
	return out, nil
}

// An eventual pass on a later claim/round cannot repair the first attempt.
func metricFirstQAPassed(work guid.ID, runs []*model.VerificationRun, results []*model.ResultVersion, before time.Time) bool {
	var first *model.VerificationRun
	for _, run := range runs {
		if run.WorkItemID == work && run.Method != "HUMAN" {
			first = run
			break
		}
	}
	if first == nil {
		return false
	}
	var result *model.ResultVersion
	for _, r := range results {
		if r.ID == first.ResultID {
			result = r
			break
		}
	}
	if result == nil || result.PreviousResultID != nil {
		return false
	}
	var plan []workVerificationStep
	if json.Unmarshal([]byte(result.VerificationPlan), &plan) != nil || len(plan) == 0 {
		return false
	}
	stages := map[guid.ID]map[string]bool{}
	for _, run := range runs {
		if run.ResultID != result.ID || run.Method == "HUMAN" {
			continue
		}
		if run.Round != 1 || run.State != "DONE" || run.Verdict != "PASSED" || run.CompletedAt == nil || run.CompletedAt.After(before) || len(run.ReceiptHash) != 64 {
			return false
		}
		if stages[run.CriterionID] == nil {
			stages[run.CriterionID] = map[string]bool{}
		}
		stages[run.CriterionID][run.Stage] = true
	}
	required := 0
	for _, step := range plan {
		if step.Method == "HUMAN" {
			continue
		}
		required++
		if !stages[step.CriterionID]["AGENT_TEST"] || !stages[step.CriterionID]["AGENT_ACCEPTANCE"] || (step.Method != "AGENT_JUDGE" && !stages[step.CriterionID]["AUTOMATED"]) {
			return false
		}
	}
	return required > 0
}

func metricTeam(projects []ProjectMetricSnapshot) ProjectMetricSnapshot {
	out := ProjectMetricSnapshot{KPIs: map[string]MetricRatio{}, Versions: map[string][]string{"model": {}, "verifier": {}, "workflow": {}}, MissingSources: []string{}}
	rounds, cycles := []int64{}, []int64{}
	for _, p := range projects {
		out.WorkItems += p.WorkItems
		out.ScopeChanged += p.ScopeChanged
		rounds = append(rounds, p.roundSamples...)
		cycles = append(cycles, p.cycleSamples...)
		out.MissingSources = append(out.MissingSources, p.MissingSources...)
		for name, values := range p.Versions {
			out.Versions[name] = append(out.Versions[name], values...)
		}
		for key, metric := range p.KPIs {
			current, exists := out.KPIs[key]
			if !exists {
				out.KPIs[key] = metric
				continue
			}
			n, d := new(big.Rat), new(big.Rat)
			if current.Numerator != nil {
				n.SetString(*current.Numerator)
			}
			if metric.Numerator != nil {
				v, ok := new(big.Rat).SetString(*metric.Numerator)
				if ok {
					n.Add(n, v)
				}
			}
			if current.Denominator != nil {
				d.SetString(*current.Denominator)
			}
			if metric.Denominator != nil {
				v, ok := new(big.Rat).SetString(*metric.Denominator)
				if ok {
					d.Add(d, v)
				}
			}
			ns, ds := n.FloatString(6), d.FloatString(0)
			combined := MetricRatio{Numerator: &ns, Denominator: &ds, Missing: current.Missing + metric.Missing, MissingDenominator: current.MissingDenominator + metric.MissingDenominator, LowerBound: current.LowerBound || metric.LowerBound, Reason: metric.Reason}
			if d.Sign() > 0 {
				value := new(big.Rat).Quo(n, d).FloatString(6)
				combined.Value = &value
			}
			unknown := func(value MetricRatio) bool {
				return value.Value == nil && value.Reason != "NO_SAMPLES" && value.Reason != "MEASURED_REVIEW_ONLY" && value.Reason != "EXCLUDES_UNPRICED_LABOR_MACHINE_PLATFORM"
			}
			if unknown(current) {
				combined.Value = nil
				combined.Reason = current.Reason
			} else if unknown(metric) {
				combined.Value = nil
				combined.Reason = metric.Reason
			}
			if combined.MissingDenominator > 0 {
				v := new(big.Rat).SetFrac64(int64(combined.Missing), int64(combined.MissingDenominator)).FloatString(6)
				combined.MissingRate = &v
			}
			out.KPIs[key] = combined
		}
	}
	for name, values := range out.Versions {
		slices.Sort(values)
		out.Versions[name] = slices.Compact(values)
	}
	slices.Sort(out.MissingSources)
	out.MissingSources = slices.Compact(out.MissingSources)
	out.InternalRounds = metricDistribution(rounds, "rounds")
	out.ReworkCycle = metricDistribution(cycles, "ms")
	return out
}

type projectMetricsInput struct {
	TenantID  string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	From      string `query:"from" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
	To        string `query:"to" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
	ProjectID string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Model     string `query:"model" maxLength:"160"`
	Verifier  string `query:"verifier" maxLength:"160"`
	Workflow  string `query:"workflow" maxLength:"160"`
}

func (m *Module) registerProjectMetricsHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "delivery.metrics.projects", Method: "GET", Path: "/metrics/projects", Summary: "Read authorized project and team economics and quality cohorts", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}, authz.Guard{Resource: "work_item", Verb: "read"}, func(ctx context.Context, in *projectMetricsInput) (*respx.Body[ProjectMetrics], error) {
		tenant, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, projectHTTPError(ctx, ErrInvalidArgument)
		}
		start, end := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -7), time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
		if in.From != "" {
			start, err = time.Parse(time.DateOnly, in.From)
			if err != nil {
				return nil, projectHTTPError(ctx, ErrInvalidArgument)
			}
		}
		if in.To != "" {
			end, err = time.Parse(time.DateOnly, in.To)
			if err != nil {
				return nil, projectHTTPError(ctx, ErrInvalidArgument)
			}
		}
		if !start.Before(end) || end.Sub(start) > 93*24*time.Hour {
			return nil, projectHTTPError(ctx, ErrInvalidArgument)
		}
		if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
			return nil, projectHTTPError(ctx, ErrUnavailable)
		}
		out := ProjectMetrics{From: start, To: end, ComputedAt: time.Now().UTC(), Version: "project-metrics-v1", Cohort: "VISIBLE_WORK_WINDOW_UTC", Projects: []ProjectMetricSnapshot{}}
		out.Selection = MetricSelection{Model: in.Model, Verifier: in.Verifier, Workflow: in.Workflow}
		err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := m.lockStagePrincipalsOn(ctx, tx, authn.PrincipalIDFromContext(ctx), nil); err != nil {
				return err
			}
			projects := []guid.ID{}
			if in.ProjectID != "" {
				id, e := guid.RequireID(in.ProjectID)
				if e != nil {
					return ErrInvalidArgument
				}
				projects = append(projects, id)
			} else {
				var e error
				projects, e = m.planningProjectsOn(ctx, tx, tenant)
				if e != nil {
					return e
				}
			}
			for _, project := range projects {
				snapshot, e := m.projectMetricSelectedOn(ctx, tx, tenant, project, start, end, MetricSelection{Model: in.Model, Verifier: in.Verifier, Workflow: in.Workflow})
				if e != nil {
					return e
				}
				out.Projects = append(out.Projects, snapshot)
			}
			out.Team = metricTeam(out.Projects)
			return nil
		})
		if err != nil {
			return nil, projectHTTPError(ctx, err)
		}
		return respx.OK(ctx, out), nil
	})
}
