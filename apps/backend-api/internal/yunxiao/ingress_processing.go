package yunxiao

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ingressCommit = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

// IngressDecimal accepts callback JSON numbers or decimal strings and emits a
// decimal string so browser readers cannot silently round signed source facts.
type IngressDecimal string

func (v *IngressDecimal) UnmarshalJSON(raw []byte) error {
	text := string(raw)
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return ErrInvalid
		}
	}
	if !ingressDecimal.MatchString(text) {
		return ErrInvalid
	}
	*v = IngressDecimal(text)
	return nil
}

type FlowRedline struct {
	Metric    string         `json:"metric" maxLength:"200"`
	Op        string         `json:"op" enum:"LT,LTE,GT,GTE,EQ,NE,<,<=,>,>=,==,!="`
	Threshold IngressDecimal `json:"threshold"`
	Value     IngressDecimal `json:"value"`
	Passed    *bool          `json:"passed"`
}
type FlowStage struct {
	Name     string        `json:"name" maxLength:"200"`
	Status   string        `json:"status"`
	Redlines []FlowRedline `json:"redlines" maxItems:"100"`
}
type FlowManualGate struct {
	Name      string   `json:"name" maxLength:"200"`
	Mode      string   `json:"mode" enum:"AND,OR"`
	Approvers []string `json:"approvers" maxItems:"100"`
	Result    string   `json:"result" enum:"PASSED,FAILED,PENDING"`
}

// IngressObservation preserves source assertions as reference evidence. A
// signed manual gate is always an external, read-only decision, never L3.
// TrustedVerification stays false until an exact frozen verifier consumes it.
type IngressObservation struct {
	EventID             guid.ID          `json:"event_id"`
	Source              string           `json:"source"`
	Repository          string           `json:"repository"`
	ExternalProjectID   string           `json:"external_project_id"`
	PipelineID          string           `json:"pipeline_id"`
	RunID               string           `json:"run_id"`
	Commit              string           `json:"commit"`
	Kind                string           `json:"kind"`
	Title               string           `json:"title"`
	References          []string         `json:"references"`
	StageResults        []FlowStage      `json:"stage_results"`
	ManualGates         []FlowManualGate `json:"manual_gates"`
	AuthoritySource     string           `json:"authority_source"`
	TrustedVerification bool             `json:"trusted_verification"`
	LocalAcceptance     bool             `json:"local_acceptance"`
}

func parseIngressObservation(event *model.InboundEvent, raw []byte) (IngressObservation, error) {
	out := IngressObservation{EventID: event.ID, Source: event.Source, Kind: event.EventKind, References: []string{}, StageResults: []FlowStage{}, ManualGates: []FlowManualGate{}}
	if event.Source == "FLOW" {
		var body struct {
			Project  string           `json:"external_project_id"`
			Pipeline string           `json:"pipeline_id"`
			Run      string           `json:"run_id"`
			Commit   string           `json:"commit"`
			Stages   []FlowStage      `json:"stage_results"`
			Gates    []FlowManualGate `json:"manual_gates"`
		}
		if json.Unmarshal(raw, &body) != nil || strings.TrimSpace(body.Project) == "" || !textValid(body.Project, 64) || len(body.Stages) > 100 || len(body.Gates) > 100 {
			return out, ErrInvalid
		}
		for i, stage := range body.Stages {
			if stage.Redlines == nil {
				body.Stages[i].Redlines = []FlowRedline{}
			}
			if strings.TrimSpace(stage.Name) == "" || !textValid(stage.Name, 200) || !slices.Contains([]string{"PASSED", "FAILED", "PENDING", "RUNNING", "CANCELLED", "SKIPPED"}, stage.Status) || len(stage.Redlines) > 100 { //nolint:misspell // Preserve the published domain or serialized contract spelling.
				return out, ErrInvalid
			}
			for _, r := range stage.Redlines {
				if strings.TrimSpace(r.Metric) == "" || !textValid(r.Metric, 200) || !slices.Contains([]string{"LT", "LTE", "GT", "GTE", "EQ", "NE", "<", "<=", ">", ">=", "==", "!="}, r.Op) || r.Passed == nil || !ingressDecimal.MatchString(string(r.Value)) || !ingressDecimal.MatchString(string(r.Threshold)) {
					return out, ErrInvalid
				}
			}
		}
		for i, gate := range body.Gates {
			if gate.Approvers == nil {
				body.Gates[i].Approvers = []string{}
			}
			if strings.TrimSpace(gate.Name) == "" || !textValid(gate.Name, 200) || !slices.Contains([]string{"AND", "OR"}, gate.Mode) || !slices.Contains([]string{"PASSED", "FAILED", "PENDING"}, gate.Result) || len(gate.Approvers) > 100 {
				return out, ErrInvalid
			}
			for _, a := range gate.Approvers {
				if strings.TrimSpace(a) == "" || !textValid(a, 200) {
					return out, ErrInvalid
				}
			}
		}
		out.ExternalProjectID, out.PipelineID, out.RunID, out.Commit = body.Project, body.Pipeline, body.Run, body.Commit
		if body.Stages != nil {
			out.StageResults = body.Stages
		}
		if body.Gates != nil {
			out.ManualGates = body.Gates
		}
		out.AuthoritySource = "EXTERNAL:YUNXIAO_FLOW:" + body.Pipeline
		return out, nil
	}
	var body struct {
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Attributes struct {
			Title      string `json:"title"`
			Note       string `json:"note"`
			LastCommit struct {
				ID string `json:"id"`
			} `json:"last_commit"`
		} `json:"object_attributes"`
		Commits []struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		} `json:"commits"`
	}
	if json.Unmarshal(raw, &body) != nil || strings.TrimSpace(body.Repository.FullName) == "" || !textValid(body.Repository.FullName, 200) || len(body.Commits) > 1000 {
		return out, ErrInvalid
	}
	out.Repository = body.Repository.FullName
	out.Title = truncateIngressTitle(body.Attributes.Title)
	text := body.Attributes.Title + "\n" + body.Attributes.Note
	for _, c := range body.Commits {
		if !ingressCommit.MatchString(c.ID) {
			return out, ErrInvalid
		}
		text += "\n" + c.Message
	}
	if body.Attributes.LastCommit.ID != "" && !ingressCommit.MatchString(body.Attributes.LastCommit.ID) {
		return out, ErrInvalid
	}
	out.Commit = body.Attributes.LastCommit.ID
	for _, ref := range delivery.ParseCodeReferences(text) {
		out.References = append(out.References, ref.Token)
	}
	out.AuthoritySource = "EXTERNAL:YUNXIAO_CODEUP"
	return out, nil
}

var ingressDecimal = regexp.MustCompile(`^-?(?:0|[1-9][0-9]{0,30})(?:\.[0-9]{1,18})?$`)

func truncateIngressTitle(v string) string {
	r := []rune(v)
	if len(r) > 300 {
		return string(r[:300])
	}
	return v
}

func (m *Module) processIngressJob(ctx context.Context, job jobs.Job) error {
	var payload struct {
		EventID string `json:"event_id"`
	}
	if json.Unmarshal(job.Payload, &payload) != nil || job.TenantID <= 0 {
		return ErrInvalid
	}
	id, err := guid.RequireID(payload.EventID)
	if err != nil {
		return ErrInvalid
	}
	db := m.database()
	if db == nil || m.ingressFiles == nil {
		return ErrUnavailable
	}
	// Discovery is followed by locked re-reads. The worker never trusts a tenant,
	// connection, project, or owner from the job payload.
	q := query.BindInboundEvents(db, m.names)
	event, err := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.ID.Eq(id)).First()
	if err != nil {
		return err
	}
	if event.State == "PROCESSED" || event.State == "IGNORED" {
		return nil
	}
	cq := query.BindConnections(db, m.names)
	conn, err := cq.WithContext(ctx).Where(cq.TenantID.Eq(job.TenantID), cq.ID.Eq(event.ConnectionID)).First()
	if err != nil {
		return err
	}
	signed := context.WithValue(ctx, ingressAuthenticationKey{}, ingressAuthentication{conn.TenantID, conn.ID, conn.CreatedBy, conn.Version})
	var raw []byte
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, e := m.ingressFiles.OpenOn(signed, tx, conn.TenantID, conn.ID, conn.CreatedBy, event.PayloadFileID)
		if e != nil {
			return e
		}
		defer func() { _ = r.Close() }()
		raw, e = io.ReadAll(io.LimitReader(r, (1<<20)+1))
		if e != nil {
			return e
		}
		if len(raw) > 1<<20 || ingressDigest(raw) != event.PayloadHash || !event.SignatureOK {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		return m.ingressJobFailure(ctx, job, event, err)
	}
	observation, err := parseIngressObservation(event, raw)
	if err != nil {
		return m.ingressJobFailure(ctx, job, event, err)
	}
	mq := query.BindProjectMappings(db, m.names)
	mappings, err := mq.WithContext(ctx).Where(mq.TenantID.Eq(job.TenantID), mq.ConnectionID.Eq(conn.ID)).Order(mq.ProjectID, mq.ID).Find()
	if err != nil {
		return err
	}
	matching := []*model.ProjectMapping{}
	for _, r := range mappings {
		v := mappingView(r)
		if (event.Source == "FLOW" && r.ExternalProjectID == observation.ExternalProjectID) || (event.Source == "CODEUP" && slices.Contains(v.CodeupRepositories, observation.Repository)) {
			matching = append(matching, r)
		}
	}
	// Each target is authorized before any connection/file locks; all effects
	// commit together, so a retry cannot expose half a multi-project observation.
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		delegated := authn.WithClaims(ctx, &authn.Claims{TenantID: conn.TenantID, PrincipalID: conn.CreatedBy, SubjectType: authn.SubjectTypePrincipal})
		if e := m.authorityOn(delegated, tx, conn.TenantID); e != nil {
			return e
		}
		for _, r := range matching {
			if m.projects.AuthorizeOn == nil {
				return ErrUnavailable
			}
			if e := m.projects.AuthorizeOn(delegated, tx, conn.TenantID, r.ProjectID, "project_manage"); e != nil {
				return e
			}
			current, e := m.mappingOn(ctx, tx, conn.TenantID, r.ID)
			if e != nil {
				return e
			}
			if current.Version != r.Version || current.Paused {
				return ErrConflict
			}
		}
		if e := m.authorizeIngressPayloadOn(signed, tx, conn.TenantID, conn.ID, conn.CreatedBy); e != nil {
			return e
		}
		locked, e := query.BindInboundEvents(tx, m.names).WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(job.TenantID), q.ID.Eq(id)).First()
		if e != nil {
			return e
		}
		if locked.State == "PROCESSED" || locked.State == "IGNORED" {
			return nil
		}
		if locked.PayloadHash != event.PayloadHash || locked.PayloadFileID != event.PayloadFileID || !locked.SignatureOK {
			return ErrConflict
		}
		detail, e := json.Marshal(observation)
		if e != nil {
			return e
		}
		for _, r := range matching {
			logID, e := m.nextID()
			if e != nil {
				return e
			}
			now := m.now()
			log := &model.IngressObservation{ID: logID, TenantID: conn.TenantID, ProjectID: r.ProjectID, MappingID: r.ID, EventID: event.ID, CreatedAt: now, Source: event.Source, Detail: string(detail), ContentHash: ingressDigest(detail)}
			if e = query.BindIngressObservations(tx, m.names).WithContext(ctx).Create(log); e != nil {
				return e
			}
		}
		locked.Version++
		locked.State = "PROCESSED"
		if len(matching) == 0 {
			locked.State = "IGNORED"
		}
		now := m.now()
		locked.ProcessedAt = &now
		locked.UpdatedAt = now
		locked.Error = nil
		i, e := query.BindInboundEvents(tx, m.names).WithContext(ctx).Where(q.ID.Eq(id), q.Version.Eq(locked.Version-1)).Select(q.Version, q.State, q.UpdatedAt, q.ProcessedAt, q.Error).Updates(locked)
		if e != nil {
			return e
		}
		if i.RowsAffected != 1 {
			return ErrConflict
		}
		return m.ingressChangedOn(ctx, tx, locked, "integration.ingress_processed")
	})
	if err != nil {
		return m.ingressJobFailure(ctx, job, event, err)
	}
	return nil
}

func (m *Module) ingressJobFailure(ctx context.Context, job jobs.Job, event *model.InboundEvent, cause error) error {
	err := m.database().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindInboundEvents(tx, m.names)
		r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(job.TenantID), q.ID.Eq(event.ID)).First()
		if e != nil {
			return e
		}
		if r.State == "PROCESSED" || r.State == "IGNORED" {
			return nil
		}
		terminal := job.MaxAttempts > 0 && job.Attempt >= job.MaxAttempts
		if terminal && r.State == "FAILED" && r.Error != nil && *r.Error == "RETRIES_EXHAUSTED" {
			return nil
		}
		r.State = "FAILED"
		r.Version++
		r.UpdatedAt = m.now()
		reason := "PROCESSING_FAILED"
		if terminal {
			reason = "RETRIES_EXHAUSTED"
		}
		r.Error = &reason
		i, e := q.WithContext(ctx).Where(q.ID.Eq(r.ID), q.Version.Eq(r.Version-1)).Select(q.State, q.Version, q.UpdatedAt, q.Error).Updates(r)
		if e != nil {
			return e
		}
		if i.RowsAffected != 1 {
			return ErrConflict
		}
		return m.ingressChangedOn(ctx, tx, r, "integration.ingress_failed")
	})
	return errors.Join(cause, err)
}
