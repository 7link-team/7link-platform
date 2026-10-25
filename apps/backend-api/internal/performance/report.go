package performance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/performance/model"
	"7link.com/platform/internal/performance/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Assessment struct {
	Value        int64     `json:"value"`
	EvidenceRefs []string  `json:"evidence_refs"`
	Reason       string    `json:"reason"`
	ActorID      guid.ID   `json:"actor_id"`
	AssessedAt   time.Time `json:"assessed_at"`
}

type RuleScore struct {
	Key          string   `json:"key"`
	Metric       string   `json:"metric"`
	Value        *int64   `json:"value"`
	Matched      *bool    `json:"matched"`
	Delta        *int64   `json:"delta"`
	EvidenceRefs []string `json:"evidence_refs"`
	Source       string   `json:"source"`
}

type DimensionScore struct {
	Key      string      `json:"key"`
	ScoreBPS *int64      `json:"score_bps" doc:"Weighted points in units of 1/10000 point, without rounding."`
	Rules    []RuleScore `json:"rules"`
}

type Confirmation struct {
	Revision    int       `json:"revision"`
	SHA256      string    `json:"sha256"`
	ActorID     guid.ID   `json:"actor_id"`
	ConfirmedAt time.Time `json:"confirmed_at"`
	Reason      string    `json:"reason"`
}

type ReportSnapshot struct {
	Scheme        RevisionView          `json:"scheme"`
	SubjectKind   string                `json:"subject_kind"`
	SubjectID     guid.ID               `json:"subject_id"`
	Period        PeriodWindow          `json:"period"`
	Evidence      EvidenceSet           `json:"evidence"`
	Assessments   map[string]Assessment `json:"assessments"`
	Dimensions    []DimensionScore      `json:"dimensions"`
	TotalScoreBPS *int64                `json:"total_score_bps" doc:"Total weighted points in units of 1/10000 point; null means unassessed or a work summary."`
	Status        string                `json:"status" enum:"PENDING,CONFIRMED,SUMMARY"`
	Confirmation  *Confirmation         `json:"confirmation"`
}

type ReportView struct {
	ID        guid.ID        `json:"id"`
	ReportID  guid.ID        `json:"report_id"`
	Version   int            `json:"version"`
	SHA256    string         `json:"sha256"`
	Snapshot  ReportSnapshot `json:"snapshot"`
	Reason    string         `json:"reason"`
	CreatedBy guid.ID        `json:"created_by"`
	CreatedAt time.Time      `json:"created_at"`
}

type reportPrincipalLocks struct {
	Kind    string
	Subject guid.ID
}

// Principal locks precede scheme/report locks, for both Human commands and
// SYSTEM generation. Summary subject IDs are not principal identities.
func (m *Module) lockReportPrincipalsOn(ctx context.Context, tx *gorm.DB, kind string, subject guid.ID) error {
	if tx == nil || tx.Statement == nil || m.principals.Lock == nil {
		return ErrUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	ids := []guid.ID{}
	if actor := authn.PrincipalIDFromContext(ctx); actor > 0 {
		ids = append(ids, actor)
	}
	if kind == "HUMAN" || kind == "AGENT" {
		if subject <= 0 {
			return ErrInvalid
		}
		ids = append(ids, subject)
	}
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if err := m.principals.Lock(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) generationSubjectKindOn(ctx context.Context, tx *gorm.DB, schemeID guid.ID) (string, error) {
	q := query.BindSchemes(tx, m.names)
	head, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Eq(schemeID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	r := query.BindRevisions(tx, m.names)
	row, err := r.WithContext(ctx).Where(r.TenantID.Eq(head.TenantID), r.SchemeID.Eq(schemeID), r.Version.Eq(head.Version)).First()
	if err != nil {
		return "", err
	}
	view, err := revisionView(row)
	return view.Definition.SubjectKind, err
}

func assessmentKey(dimension, rule string) string {
	raw, _ := json.Marshal([2]string{dimension, rule})
	return string(raw)
}

func reportRevisionView(row *model.ReportRevision) (ReportView, error) {
	sum := sha256.Sum256([]byte(row.Snapshot))
	if hex.EncodeToString(sum[:]) != row.SHA256 {
		return ReportView{}, ErrUnavailable
	}
	var snapshot ReportSnapshot
	if err := json.Unmarshal([]byte(row.Snapshot), &snapshot); err != nil {
		return ReportView{}, ErrUnavailable
	}
	return ReportView{ID: row.ID, ReportID: row.ReportID, Version: row.Version, SHA256: row.SHA256, Snapshot: snapshot, Reason: row.Reason, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt}, nil
}

func (m *Module) reportOn(ctx context.Context, tx *gorm.DB, id guid.ID, version int) (*model.Report, ReportView, error) {
	q := query.BindReports(tx, m.names)
	head, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ReportView{}, ErrNotFound
	}
	if err != nil {
		return nil, ReportView{}, err
	}
	if version == 0 {
		version = head.Version
	}
	r := query.BindReportRevisions(tx, m.names)
	row, err := r.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(r.TenantID.Eq(head.TenantID), r.ReportID.Eq(head.ID), r.Version.Eq(version)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ReportView{}, ErrNotFound
	}
	if err != nil {
		return nil, ReportView{}, err
	}
	view, err := reportRevisionView(row)
	return head, view, err
}

func (m *Module) reportSchemeOn(ctx context.Context, tx *gorm.DB, id guid.ID, version int) (RevisionView, error) {
	head, err := m.schemeOn(ctx, tx, id)
	if err != nil {
		return RevisionView{}, err
	}
	if version == 0 {
		version = head.Version
	}
	r := query.BindRevisions(tx, m.names)
	row, err := r.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(r.TenantID.Eq(head.TenantID), r.SchemeID.Eq(id), r.Version.Eq(version)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RevisionView{}, ErrNotFound
	}
	if err != nil {
		return RevisionView{}, err
	}
	return revisionView(row)
}

func (m *Module) reportEvidenceOn(ctx context.Context, tx *gorm.DB, scheme RevisionView, subject guid.ID, window PeriodWindow) (EvidenceSet, error) {
	if subject <= 0 || !slices.Contains(scheme.Definition.SubjectIDs, subject) {
		// An empty selection never parses prose scope into a cohort.
		return EvidenceSet{}, ErrInvalid
	}
	if m.evidence.SubjectOn == nil || m.evidence.ReadOn == nil {
		return EvidenceSet{}, ErrUnavailable
	}
	tenant := authn.TenantIDFromContext(ctx)
	if err := m.evidence.SubjectOn(ctx, tx, tenant, scheme.Definition.SubjectKind, subject); err != nil {
		return EvidenceSet{}, err
	}
	request := EvidenceRequest{TenantID: tenant, SubjectID: subject, SubjectKind: scheme.Definition.SubjectKind, Period: window}
	evidence, err := m.evidence.ReadOn(ctx, tx, request)
	if err != nil {
		return EvidenceSet{}, err
	}
	if err = validateEvidence(evidence, request); err != nil {
		return EvidenceSet{}, err
	}
	// Stable non-null collections are part of the report/UI contract.
	if evidence.Facts == nil {
		evidence.Facts = []PerformanceEvidenceFact{}
	}
	if evidence.Metrics == nil {
		evidence.Metrics = []MetricValue{}
	}
	if evidence.Gaps == nil {
		evidence.Gaps = []string{}
	}
	if evidence.Work == nil {
		evidence.Work = []WorkSummaryItem{}
	}
	return evidence, nil
}

func scoreReport(snapshot *ReportSnapshot) {
	snapshot.Dimensions = []DimensionScore{}
	snapshot.TotalScoreBPS = nil
	if snapshot.SubjectKind == "PROJECT" || snapshot.SubjectKind == "TEAM" {
		snapshot.Status = "SUMMARY"
		return
	}
	metrics := map[string]MetricValue{}
	facts := map[string]PerformanceEvidenceFact{}
	for _, fact := range snapshot.Evidence.Facts {
		facts[fact.ID] = fact
	}
	for _, metric := range snapshot.Evidence.Metrics {
		metrics[metric.Metric] = metric
	}
	total, complete := int64(0), true
	for _, dimension := range snapshot.Scheme.Definition.Dimensions {
		out := DimensionScore{Key: dimension.Key, Rules: []RuleScore{}}
		points, known := int64(0), true
		for _, rule := range dimension.Rules {
			line := RuleScore{Key: rule.Key, Metric: rule.Condition.Metric, Source: "AUTOMATIC", EvidenceRefs: []string{}}
			metric, found := metrics[rule.Condition.Metric]
			if rule.Condition.Metric == "REVIEW_SCORE" {
				line.Source = "HUMAN_CALIBRATION"
				assessment, assessed := snapshot.Assessments[assessmentKey(dimension.Key, rule.Key)]
				found = assessed
				metric = MetricValue{Value: assessment.Value, EvidenceRefs: assessment.EvidenceRefs}
			} else {
				for _, ref := range metric.EvidenceRefs {
					if !slices.Contains(rule.EvidenceKinds, facts[ref].Kind) {
						found = false
					}
				}
			}
			if found {
				value := metric.Value
				matched := (rule.Condition.Operator == "GTE" && value >= rule.Condition.Threshold) || (rule.Condition.Operator == "LTE" && value <= rule.Condition.Threshold) || (rule.Condition.Operator == "EQ" && value == rule.Condition.Threshold)
				delta := int64(0)
				if matched {
					delta = rule.Points
					if rule.Effect == "SUBTRACT" {
						delta = -delta
					}
				}
				line.Value, line.Matched, line.Delta, line.EvidenceRefs = &value, &matched, &delta, metric.EvidenceRefs
				points += delta
			} else {
				known = false
			}
			out.Rules = append(out.Rules, line)
		}
		if known {
			points = max(int64(0), min(points, dimension.MaximumPoints))
			weighted := points * int64(dimension.WeightBPS)
			out.ScoreBPS = &weighted
			total += weighted
		} else {
			complete = false
		}
		snapshot.Dimensions = append(snapshot.Dimensions, out)
	}
	if complete {
		snapshot.TotalScoreBPS = &total
	}
}

func (m *Module) appendReportOn(ctx context.Context, tx *gorm.DB, head *model.Report, snapshot ReportSnapshot, reason, action string) (ReportView, error) {
	if !textOK(reason, 2000) || head.Version >= 2147483647 {
		return ReportView{}, ErrInvalid
	}
	if m.ids == nil || m.audit == nil || m.events == nil {
		return ReportView{}, ErrUnavailable
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	actor := authn.PrincipalIDFromContext(ctx)
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return ReportView{}, err
	}
	sum := sha256.Sum256(raw)
	id, err := m.ids.Next()
	if err != nil {
		return ReportView{}, err
	}
	row := model.ReportRevision{ID: guid.ID(id), TenantID: head.TenantID, ReportID: head.ID, Version: head.Version + 1, Snapshot: string(raw), SHA256: hex.EncodeToString(sum[:]), Reason: reason, CreatedBy: actor, CreatedAt: now}
	if err = query.BindReportRevisions(tx, m.names).WithContext(ctx).Create(&row); err != nil {
		return ReportView{}, err
	}
	q := query.BindReports(tx, m.names)
	info, err := q.WithContext(ctx).Where(q.TenantID.Eq(head.TenantID), q.ID.Eq(head.ID), q.Version.Eq(head.Version)).UpdateSimple(q.Version.Value(row.Version), q.UpdatedAt.Value(now))
	if err != nil {
		return ReportView{}, err
	}
	if info.RowsAffected != 1 {
		return ReportView{}, ErrConflict
	}
	actorKind, authority := "HUMAN", "ROLE:tenant_administrator"
	if actor == 0 {
		actorKind, authority = "SYSTEM", "SYSTEM:performance_schedule"
	}
	detail := map[string]any{"revision_id": row.ID.String(), "version": row.Version, "sha256": row.SHA256, "reason": reason, "scheme_revision_id": snapshot.Scheme.ID.String(), "actor_kind": actorKind, "authority_source": authority}
	ev := audit.NewDomainEvent(ctx, head.TenantID, "performance.report_"+action, "performance_report", head.ID)
	for k, v := range detail {
		ev.Detail[k] = v
	}
	if err = m.audit(ctx, tx, ev); err != nil {
		return ReportView{}, err
	}
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: head.TenantID, Type: "performance.report_" + action, Actor: events.Actor{Kind: actorKind, ID: actor, AuthoritySource: authority}, Subject: events.Subject{Kind: "performance_report", ID: head.ID}, SubjectVersion: int64(row.Version), Data: detail})
	if err != nil {
		return ReportView{}, err
	}
	return reportRevisionView(&row)
}

func (m *Module) generateReportOn(ctx context.Context, tx *gorm.DB, schemeID, subject guid.ID, window PeriodWindow, reason string) (ReportView, error) {
	if m == nil || tx == nil || tx.Statement == nil {
		return ReportView{}, ErrUnavailable
	}
	if _, prepared := tx.Statement.Context.Value(reportPrincipalLocks{}).(reportPrincipalLocks); !prepared {
		kind, err := m.generationSubjectKindOn(ctx, tx, schemeID)
		if err != nil {
			return ReportView{}, err
		}
		if err = m.lockReportPrincipalsOn(ctx, tx, kind, subject); err != nil {
			return ReportView{}, err
		}
		tx = tx.WithContext(context.WithValue(ctx, reportPrincipalLocks{}, reportPrincipalLocks{Kind: kind, Subject: subject}))
	}
	if err := m.authorizeOn(ctx, tx); err != nil {
		return ReportView{}, err
	}
	return m.generateReportCoreOn(ctx, tx, schemeID, subject, window, reason)
}

// generateReportCoreOn is shared only by the authorized Human command and the
// scheduler's persisted-schedule authorization. It never creates Human claims.
func (m *Module) generateReportCoreOn(ctx context.Context, tx *gorm.DB, schemeID, subject guid.ID, window PeriodWindow, reason string) (ReportView, error) {
	if m == nil || tx == nil || tx.Statement == nil || authn.TenantIDFromContext(ctx) <= 0 || m.ids == nil {
		return ReportView{}, ErrUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ReportView{}, ErrInvalid
	}
	expected, err := periodWindow(window.Cadence, window.Timezone, window.StartAt)
	if err != nil || expected != window {
		return ReportView{}, ErrInvalid
	}
	if !textOK(reason, 2000) || window.CutoffAt.After(time.Now()) {
		return ReportView{}, ErrInvalid
	}
	scheme, err := m.reportSchemeOn(ctx, tx, schemeID, 0)
	if err != nil {
		return ReportView{}, err
	}
	if !scheme.Definition.Enabled {
		return ReportView{}, ErrInvalid
	}
	if subject <= 0 || !slices.Contains(scheme.Definition.SubjectIDs, subject) {
		return ReportView{}, ErrInvalid
	}
	if locked, ok := tx.Statement.Context.Value(reportPrincipalLocks{}).(reportPrincipalLocks); ok && (locked.Kind != scheme.Definition.SubjectKind || locked.Subject != subject) {
		return ReportView{}, ErrConflict
	}
	if m.evidence.SubjectOn == nil {
		return ReportView{}, ErrUnavailable
	}
	if err = m.evidence.SubjectOn(ctx, tx, authn.TenantIDFromContext(ctx), scheme.Definition.SubjectKind, subject); err != nil {
		return ReportView{}, err
	}
	q := query.BindReports(tx, m.names)
	head, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.SchemeID.Eq(schemeID), q.SubjectKind.Eq(scheme.Definition.SubjectKind), q.SubjectID.Eq(subject), q.Cadence.Eq(window.Cadence), q.PeriodStart.Eq(window.PeriodStart), q.PeriodEnd.Eq(window.PeriodEnd)).First()
	if err == nil {
		_, view, e := m.reportOn(ctx, tx, head.ID, 0)
		return view, e
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return ReportView{}, err
	}
	evidence, err := m.reportEvidenceOn(ctx, tx, scheme, subject, window)
	if err != nil {
		return ReportView{}, err
	}
	id, err := m.ids.Next()
	if err != nil {
		return ReportView{}, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	head = &model.Report{ID: guid.ID(id), TenantID: authn.TenantIDFromContext(ctx), SchemeID: schemeID, SubjectKind: scheme.Definition.SubjectKind, SubjectID: subject, Cadence: window.Cadence, PeriodStart: window.PeriodStart, PeriodEnd: window.PeriodEnd, Version: 0, CreatedAt: now, UpdatedAt: now}
	if err = q.WithContext(ctx).Create(head); err != nil {
		return ReportView{}, err
	}
	snapshot := ReportSnapshot{Scheme: scheme, SubjectKind: head.SubjectKind, SubjectID: subject, Period: window, Evidence: evidence, Assessments: map[string]Assessment{}, Status: "PENDING"}
	scoreReport(&snapshot)
	return m.appendReportOn(ctx, tx, head, snapshot, reason, "generated")
}

func (m *Module) recalculateReportOn(ctx context.Context, tx *gorm.DB, id guid.ID, base, schemeVersion int, reason string, includeUncovered bool) (ReportView, error) {
	if base < 1 || schemeVersion < 1 || !textOK(reason, 2000) {
		return ReportView{}, ErrInvalid
	}
	// Scheme lock precedes report lock, matching generation and avoiding inversion.
	q := query.BindReports(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ReportView{}, ErrNotFound
	}
	if err != nil {
		return ReportView{}, err
	}
	if _, prepared := tx.Statement.Context.Value(reportPrincipalLocks{}).(reportPrincipalLocks); !prepared {
		if err = m.lockReportPrincipalsOn(ctx, tx, r.SubjectKind, r.SubjectID); err != nil {
			return ReportView{}, err
		}
	}
	if err = m.authorizeOn(ctx, tx); err != nil {
		return ReportView{}, err
	}
	scheme, err := m.reportSchemeOn(ctx, tx, r.SchemeID, schemeVersion)
	if err != nil {
		return ReportView{}, err
	}
	head, current, err := m.reportOn(ctx, tx, id, 0)
	if err != nil {
		return ReportView{}, err
	}
	if head.Version != base {
		return ReportView{}, ErrConflict
	}
	if scheme.Definition.SubjectKind != head.SubjectKind {
		return ReportView{}, ErrInvalid
	}
	window, err := reportRecalculationWindow(current.Snapshot.Period, includeUncovered, time.Now())
	if err != nil {
		return ReportView{}, err
	}
	evidence, err := m.reportEvidenceOn(ctx, tx, scheme, head.SubjectID, window)
	if err != nil {
		return ReportView{}, err
	}
	snapshot := ReportSnapshot{Scheme: scheme, SubjectKind: head.SubjectKind, SubjectID: head.SubjectID, Period: window, Evidence: evidence, Assessments: map[string]Assessment{}, Status: "PENDING"}
	scoreReport(&snapshot)
	return m.appendReportOn(ctx, tx, head, snapshot, reason, "recalculated")
}

func reportRecalculationWindow(window PeriodWindow, includeUncovered bool, now time.Time) (PeriodWindow, error) {
	if includeUncovered {
		if window.EndAt.After(now) {
			return PeriodWindow{}, ErrInvalid
		}
		window.CutoffAt = window.EndAt
		window.UncoveredStartAt = window.EndAt
	}
	return window, nil
}

func (m *Module) calibrateReportOn(ctx context.Context, tx *gorm.DB, id guid.ID, body CalibrationBody) (ReportView, error) {
	if err := m.authorizeOn(ctx, tx); err != nil {
		return ReportView{}, err
	}
	if body.BaseVersion < 1 || !textOK(body.Reason, 2000) || body.Value < 0 || body.Value > 10000 || len(body.EvidenceRefs) == 0 || len(body.EvidenceRefs) > 50 {
		return ReportView{}, ErrInvalid
	}
	head, current, err := m.reportOn(ctx, tx, id, 0)
	if err != nil {
		return ReportView{}, err
	}
	if head.Version != body.BaseVersion {
		return ReportView{}, ErrConflict
	}
	if current.Snapshot.Status == "SUMMARY" {
		return ReportView{}, ErrInvalid
	}
	found := false
	for _, dim := range current.Snapshot.Scheme.Definition.Dimensions {
		for _, rule := range dim.Rules {
			if dim.Key == body.DimensionKey && rule.Key == body.RuleKey && rule.Condition.Metric == "REVIEW_SCORE" && slices.Contains(rule.EvidenceKinds, "HUMAN_REVIEW") {
				found = true
			}
		}
	}
	if !found {
		return ReportView{}, ErrInvalid
	}
	refs := map[string]bool{}
	for _, fact := range current.Snapshot.Evidence.Facts {
		refs[fact.ID] = true
	}
	seen := map[string]bool{}
	for _, ref := range body.EvidenceRefs {
		if !textOK(ref, 2000) || seen[ref] {
			return ReportView{}, ErrInvalid
		}
		seen[ref] = true
		if !refs[ref] {
			u, err := url.Parse(ref)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
				return ReportView{}, ErrInvalid
			}
		}
	}
	if current.Snapshot.Assessments == nil {
		current.Snapshot.Assessments = map[string]Assessment{}
	}
	current.Snapshot.Assessments[assessmentKey(body.DimensionKey, body.RuleKey)] = Assessment{Value: body.Value, EvidenceRefs: body.EvidenceRefs, Reason: body.Reason, ActorID: authn.PrincipalIDFromContext(ctx), AssessedAt: time.Now().UTC().Truncate(time.Millisecond)}
	current.Snapshot.Status, current.Snapshot.Confirmation = "PENDING", nil
	scoreReport(&current.Snapshot)
	return m.appendReportOn(ctx, tx, head, current.Snapshot, body.Reason, "calibrated")
}

func (m *Module) confirmReportOn(ctx context.Context, tx *gorm.DB, id guid.ID, body ConfirmationBody) (ReportView, error) {
	if err := m.authorizeOn(ctx, tx); err != nil {
		return ReportView{}, err
	}
	if body.BaseVersion < 1 || !reportDigestPattern.MatchString(body.SHA256) || !textOK(body.Reason, 2000) {
		return ReportView{}, ErrInvalid
	}
	head, current, err := m.reportOn(ctx, tx, id, 0)
	if err != nil {
		return ReportView{}, err
	}
	if head.Version != body.BaseVersion || current.SHA256 != body.SHA256 || current.Snapshot.Status != "PENDING" {
		return ReportView{}, ErrConflict
	}
	if current.Snapshot.TotalScoreBPS == nil {
		return ReportView{}, ErrInvalid
	}
	current.Snapshot.Status = "CONFIRMED"
	current.Snapshot.Confirmation = &Confirmation{Revision: current.Version, SHA256: current.SHA256, ActorID: authn.PrincipalIDFromContext(ctx), ConfirmedAt: time.Now().UTC().Truncate(time.Millisecond), Reason: body.Reason}
	return m.appendReportOn(ctx, tx, head, current.Snapshot, body.Reason, "confirmed")
}
