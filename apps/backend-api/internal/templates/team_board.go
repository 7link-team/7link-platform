package templates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/httpx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrTeamEvidence   = errors.New("templates: evidence URL or version could not be verified")
	ErrTeamIncomplete = errors.New("templates: checklist or risks are incomplete")
	ErrTeamStatus     = errors.New("templates: reported status differs from current delivery state")
)

type TeamChecklistItem struct {
	Text          string `json:"text" minLength:"1" maxLength:"500"`
	Done          bool   `json:"done"`
	EvidenceIndex int    `json:"evidence_index" minimum:"0" maximum:"9"`
}
type TeamEvidence struct {
	URL        string    `json:"url" maxLength:"2000"`
	Version    string    `json:"version" maxLength:"200"`
	ModifiedAt time.Time `json:"modified_at"`
}
type TeamRisk struct {
	Title       string    `json:"title" minLength:"1" maxLength:"500"`
	OwnerID     guid.ID   `json:"owner_id"`
	DueAt       time.Time `json:"due_at"`
	Disposition string    `json:"disposition" maxLength:"2000"`
	Resolved    bool      `json:"resolved"`
}
type TeamSignOff struct {
	ActorID     guid.ID   `json:"actor_id"`
	At          time.Time `json:"at"`
	Scope       string    `json:"scope"`
	ContentHash string    `json:"content_hash"`
	WorkVersion int       `json:"work_version"`
}

// TeamTaskData extends the existing cross-dialect JSON studio document. Signoffs
// are append-only server observations; editable input cannot supply them.
type TeamTaskData struct {
	WorkItemID guid.ID                 `json:"work_item_id"`
	SignerID   guid.ID                 `json:"signer_id"`
	Checklist  []TeamChecklistItem     `json:"checklist" maxItems:"50"`
	Evidence   []TeamEvidence          `json:"evidence" maxItems:"10"`
	Risks      []TeamRisk              `json:"risks" maxItems:"50"`
	SignOffs   []TeamSignOff           `json:"sign_offs"`
	Validation *TeamEvidenceValidation `json:"validation,omitempty"`
}
type TeamEvidenceValidation struct {
	At          time.Time `json:"at"`
	ContentHash string    `json:"content_hash"`
	WorkVersion int       `json:"work_version"`
}
type TeamEdit struct {
	Version int           `json:"version" minimum:"0"`
	Action  string        `json:"action" enum:"SAVE,SIGN_OFF,CHECK_EVIDENCE,EDIT_DRAFT,ADOPT_DRAFT"`
	Reason  string        `json:"reason" minLength:"1" maxLength:"2000"`
	Content StudioContent `json:"content"`
}
type TeamTaskView struct {
	Record   StudioRecordView      `json:"record"`
	Work     delivery.TeamWorkFact `json:"work"`
	Complete int                   `json:"complete"`
	Signed   bool                  `json:"signed"`
}

func (m *Module) teamReplayOn(ctx context.Context, tx *gorm.DB, receipt TeamTaskView) (TeamTaskView, error) {
	q := query.BindBindings(tx, m.names)
	binding, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Eq(receipt.Record.BindingID)).Take()
	if err != nil {
		return TeamTaskView{}, err
	}
	if err = m.lockTeamParticipantsOn(ctx, tx, binding.ProjectID, receipt.Record.ID, StudioContent{}); err != nil {
		return TeamTaskView{}, err
	}
	current, err := m.studioAuthorizeOn(ctx, tx, binding.ProjectID)
	if err != nil {
		return TeamTaskView{}, err
	}
	if current.ID != binding.ID {
		return TeamTaskView{}, ErrConflict
	}
	row, err := m.studioRecordOn(ctx, tx, binding.ID, receipt.Record.ID)
	if err != nil {
		return TeamTaskView{}, err
	}
	view, err := studioView(row)
	if err != nil {
		return TeamTaskView{}, err
	}
	var fact delivery.TeamWorkFact
	if view.Content.Team != nil {
		fact, err = m.teamWorkFactOn(ctx, tx, binding.ProjectID, view.Content.Team.WorkItemID)
	}
	if err != nil {
		return TeamTaskView{}, err
	}
	return teamTaskView(view, fact), nil
}

func (m *Module) teamWorkFactOn(ctx context.Context, tx *gorm.DB, project, work guid.ID) (delivery.TeamWorkFact, error) {
	if m.teamWork == nil {
		return delivery.TeamWorkFact{}, ErrUnavailable
	}
	fact, err := m.teamWork(ctx, tx, authn.TenantIDFromContext(ctx), project, work)
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, delivery.ErrProjectNotFound) {
		err = ErrNotFound
	}
	return fact, err
}
func teamHash(c StudioContent) string {
	snapshot := c
	team := *c.Team
	team.SignOffs = nil
	team.Validation = nil
	snapshot.Team = &team
	raw, _ := json.Marshal(snapshot)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func teamTaskView(row StudioRecordView, work delivery.TeamWorkFact) TeamTaskView {
	out := TeamTaskView{Record: row, Work: work}
	if row.Content.Team == nil {
		return out
	}
	for _, item := range row.Content.Team.Checklist {
		if item.Done {
			out.Complete++
		}
	}
	signs := row.Content.Team.SignOffs
	if len(signs) > 0 {
		last := signs[len(signs)-1]
		out.Signed = row.State == "CONFIRMED" && last.ContentHash == teamHash(row.Content) && last.WorkVersion == work.Version && last.ActorID == row.Content.Team.SignerID && last.ActorID != work.OwnerID && last.ActorID != row.CreatedBy && (work.AuthorID == nil || *work.AuthorID != last.ActorID)
	}
	return out
}
func (m *Module) validateTeamContentOn(ctx context.Context, tx *gorm.DB, project guid.ID, c StudioContent) (delivery.TeamWorkFact, error) {
	var empty delivery.TeamWorkFact
	team := c.Team
	if strings.TrimSpace(c.Title) == "" || len(c.Title) > 200 || len(c.Text) > 20000 || len(c.Source) > 2000 || c.OwnerID == nil || *c.OwnerID <= 0 || c.DueAt == nil || c.DueAt.IsZero() || team == nil || team.WorkItemID <= 0 || team.SignerID <= 0 || len(team.Checklist) < 1 || len(team.Checklist) > 50 || len(team.Evidence) > 10 || len(team.Risks) > 50 {
		return empty, ErrInvalid
	}
	work, err := m.teamWorkFactOn(ctx, tx, project, team.WorkItemID)
	if err != nil {
		return empty, err
	}
	if work.OwnerID != *c.OwnerID {
		return empty, ErrConflict
	}
	humanIDs := []guid.ID{team.SignerID, *c.OwnerID}
	for _, risk := range team.Risks {
		if strings.TrimSpace(risk.Title) == "" || len(risk.Title) > 500 || risk.OwnerID <= 0 || risk.DueAt.IsZero() || len(risk.Disposition) > 2000 || (risk.Resolved && strings.TrimSpace(risk.Disposition) == "") {
			return empty, ErrInvalid
		}
		humanIDs = append(humanIDs, risk.OwnerID)
	}
	for _, id := range humanIDs {
		if m.teamHuman == nil {
			return empty, ErrUnavailable
		}
		if e := m.teamHuman(ctx, tx, authn.TenantIDFromContext(ctx), id); e != nil {
			return empty, e
		}
	}
	for _, item := range team.Checklist {
		if strings.TrimSpace(item.Text) == "" || len(item.Text) > 500 || item.EvidenceIndex < 0 || item.EvidenceIndex > 9 || (item.Done && item.EvidenceIndex >= len(team.Evidence)) {
			return empty, ErrInvalid
		}
	}
	for _, evidence := range team.Evidence {
		u, e := url.Parse(evidence.URL)
		if e != nil || len(evidence.URL) > 2000 || u.User != nil || u.Fragment != "" || (httpx.Policy{}).ValidateURL(evidence.URL) != nil || strings.TrimSpace(evidence.Version) == "" || len(evidence.Version) > 200 || evidence.ModifiedAt.IsZero() {
			return empty, ErrInvalid
		}
	}
	return work, nil
}

// checkTeamEvidence reads actual remote headers. No user-provided "accessible"
// or "passed" flag can establish evidence. The guarded client blocks SSRF on
// every connection/redirect and sends no ambient credentials.
func checkTeamEvidence(ctx context.Context, client *http.Client, evidence TeamEvidence, started time.Time) error {
	if !evidence.ModifiedAt.After(started) || evidence.ModifiedAt.After(time.Now().UTC().Add(time.Minute)) {
		return ErrTeamEvidence
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, evidence.URL, nil)
	if err != nil {
		return ErrInvalid
	}
	response, err := client.Do(request)
	if err != nil {
		return ErrTeamEvidence
	}
	defer func() { _ = response.Body.Close() }()
	modified, err := http.ParseTime(response.Header.Get("Last-Modified"))
	if response.StatusCode < 200 || response.StatusCode >= 300 || err != nil || !modified.Equal(evidence.ModifiedAt) || strings.Trim(response.Header.Get("ETag"), "\"") != strings.Trim(evidence.Version, "\"") {
		return ErrTeamEvidence
	}
	return nil
}
func (m *Module) teamEditOn(ctx context.Context, tx *gorm.DB, project, id guid.ID, in TeamEdit) (TeamTaskView, error) {
	var empty TeamTaskView
	if strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 2000 || in.Version < 0 || !slices.Contains([]string{"SAVE", "SIGN_OFF", "CHECK_EVIDENCE", "EDIT_DRAFT", "ADOPT_DRAFT"}, in.Action) {
		return empty, ErrInvalid
	}
	if err := m.lockTeamParticipantsOn(ctx, tx, project, id, in.Content); err != nil {
		return empty, err
	}
	binding, err := m.studioAuthorizeOn(ctx, tx, project)
	if err != nil {
		return empty, err
	}
	version, err := m.versionOn(ctx, tx, binding.TemplateVersionID, "template_read")
	if err != nil {
		return empty, err
	}
	definition, err := versionView(version)
	if err != nil {
		return empty, err
	}
	if definition.Definition.Domain != "TEAM_TASKS" {
		return empty, ErrConflict
	}
	q := query.BindStudioRecords(tx, m.names)
	row := &model.StudioRecord{TenantID: binding.TenantID, BindingID: binding.ID, Domain: "TEAM_TASKS", Kind: "TASK", State: "CONFIRMED", Version: 1, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: time.Now().UTC()}
	var previous StudioContent
	if id > 0 {
		row, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(binding.TenantID), q.BindingID.Eq(binding.ID), q.ID.Eq(id)).Take()
		if err != nil {
			return empty, err
		}
		if row.Domain != "TEAM_TASKS" || row.Version != in.Version {
			return empty, ErrConflict
		}
		if err = json.Unmarshal([]byte(row.Payload), &previous); err != nil {
			return empty, err
		}
	} else if in.Version != 0 || in.Action != "SAVE" {
		return empty, ErrInvalid
	}
	c := in.Content
	var work delivery.TeamWorkFact
	switch in.Action {
	case "EDIT_DRAFT":
		if row.Kind != "ASSISTANT_DRAFT" || row.State != "OPEN" {
			return empty, ErrConflict
		}
		if err = (StudioDraft{Domain: "TEAM_TASKS", Kind: "ASSISTANT_DRAFT", Content: c}).validate(); err != nil {
			return empty, err
		}
	case "SAVE", "ADOPT_DRAFT":
		if in.Action == "ADOPT_DRAFT" {
			if id <= 0 || row.Kind != "ASSISTANT_DRAFT" || row.State != "OPEN" || previous.Source == "" {
				return empty, ErrConflict
			}
			c.Source = previous.Source
			// Adoption retains the recorded suggestion type. Omitting STATUS
			// cannot bypass reconciliation against the current canonical task.
			c.DraftType = previous.DraftType
			if c.DraftType == "STATUS" && c.ReportedState == "" {
				c.ReportedState = previous.ReportedState
			}
		} else if id > 0 && row.Kind != "TASK" {
			return empty, ErrInvalid
		}
		if c.Team != nil && c.Team.WorkItemID == 0 {
			if previous.Team != nil || c.OwnerID == nil || *c.OwnerID <= 0 || len(c.Team.Checklist) < 1 || len(c.Team.Checklist) > 50 || c.DueAt == nil || c.DueAt.IsZero() || c.Team.SignerID <= 0 {
				return empty, ErrInvalid
			}
			if _, e := m.teamWorkFactOn(ctx, tx, project, 0); e != nil {
				return empty, e
			}
			criteria := make([]delivery.TemplateCriterion, len(c.Team.Checklist))
			for index, item := range c.Team.Checklist {
				criteria[index] = delivery.TemplateCriterion{Text: item.Text, VerificationMethod: "Human review against recorded deliverable evidence"}
			}
			workID, e := m.delivery.CreateWorkOn(ctx, tx, binding.TenantID, project, delivery.TemplateWorkInput{Title: c.Title, Description: c.Text, Type: "TASK", Objective: c.Title, AccountablePrincipalID: *c.OwnerID, Criteria: criteria})
			if e != nil {
				return empty, e
			}
			team := *c.Team
			team.WorkItemID = workID
			c.Team = &team
		}
		work, err = m.validateTeamContentOn(ctx, tx, project, c)
		if err != nil {
			return empty, err
		}
		if previous.Team != nil && previous.Team.WorkItemID != c.Team.WorkItemID {
			return empty, ErrConflict
		}
		if c.DraftType == "STATUS" && c.ReportedState != work.State {
			return empty, ErrTeamStatus
		}
		team := *c.Team
		team.SignOffs = []TeamSignOff{}
		team.Validation = nil
		if team.Evidence == nil {
			team.Evidence = []TeamEvidence{}
		}
		if team.Risks == nil {
			team.Risks = []TeamRisk{}
		}
		if previous.Team != nil {
			team.SignOffs = previous.Team.SignOffs
		}
		c.Team = &team
		row.Kind = "TASK"
		row.State = "CONFIRMED"
	case "SIGN_OFF", "CHECK_EVIDENCE":
		if id <= 0 || row.Kind != "TASK" || previous.Team == nil {
			return empty, ErrInvalid
		}
		c = previous
		work, err = m.validateTeamContentOn(ctx, tx, project, c)
		if err != nil {
			return empty, err
		}
		actor := authn.PrincipalIDFromContext(ctx)
		if in.Action == "SIGN_OFF" && (actor != c.Team.SignerID || actor == row.CreatedBy || actor == work.OwnerID || (work.AuthorID != nil && actor == *work.AuthorID)) {
			return empty, ErrForbidden
		}
		if len(c.Team.SignOffs) >= 100 || len(c.Team.Evidence) == 0 {
			return empty, ErrConflict
		}
		if slices.Contains([]string{"CANCELLED", "STOPPED"}, work.State) { //nolint:misspell // Preserve the published domain or serialized contract spelling.
			return empty, ErrConflict
		}
		if in.Action == "SIGN_OFF" {
			for _, item := range c.Team.Checklist {
				if !item.Done {
					return empty, ErrTeamIncomplete
				}
			}
			for _, risk := range c.Team.Risks {
				if !risk.Resolved {
					return empty, ErrTeamIncomplete
				}
			}
		}
		client := httpx.NewClient(5*time.Second, httpx.Policy{})
		if m.teamEvidenceClient != nil {
			client = m.teamEvidenceClient
		}
		defer client.CloseIdleConnections()
		checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		for _, evidence := range c.Team.Evidence {
			if err = checkTeamEvidence(checkCtx, client, evidence, work.StartedAt); err != nil {
				return empty, err
			}
		}
		c.Team.Validation = &TeamEvidenceValidation{At: time.Now().UTC(), ContentHash: teamHash(c), WorkVersion: work.Version}
		if in.Action == "SIGN_OFF" {
			c.Team.SignOffs = append(c.Team.SignOffs, TeamSignOff{ActorID: actor, At: time.Now().UTC(), Scope: in.Reason, ContentHash: teamHash(c), WorkVersion: work.Version})
			row.State = "CONFIRMED"
		}
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return empty, err
	}
	row.Payload = string(raw)
	if id == 0 {
		next, e := m.ids.Next()
		if e != nil {
			return empty, e
		}
		row.ID = guid.ID(next)
		err = q.WithContext(ctx).Create(row)
	} else {
		result, e := q.WithContext(ctx).Where(q.TenantID.Eq(binding.TenantID), q.BindingID.Eq(binding.ID), q.ID.Eq(id), q.Version.Eq(in.Version)).Updates(map[string]any{"payload": row.Payload, "kind": row.Kind, "state": row.State, "version": row.Version + 1})
		err = e
		if err == nil && result.RowsAffected != 1 {
			return empty, ErrConflict
		}
		row.Version++
	}
	if err != nil {
		return empty, err
	}
	if err = m.studioEventOn(ctx, tx, project, row, in.Action+":"+in.Reason); err != nil {
		return empty, err
	}
	view, err := studioView(row)
	return teamTaskView(view, work), err
}

// Read hints grant no authority. All participant principal fences are acquired
// in ID order before the existing project/binding fence. Current validation
// below rejects a changed work owner or binding instead of reversing lock order.
func (m *Module) lockTeamParticipantsOn(ctx context.Context, tx *gorm.DB, project, id guid.ID, c StudioContent) error {
	q := query.BindBindings(tx, m.names)
	binding, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var runs []TemplateNodeRun
	if err = json.Unmarshal([]byte(binding.Runs), &runs); err != nil {
		return err
	}
	ids := []guid.ID{authn.PrincipalIDFromContext(ctx)}
	if len(runs) > 0 {
		ids = append(ids, runs[0].AccountableID)
	}
	collect := func(content StudioContent) error {
		if content.OwnerID != nil {
			ids = append(ids, *content.OwnerID)
		}
		if content.Team != nil {
			if len(content.Team.Risks) > 50 {
				return ErrInvalid
			}
			ids = append(ids, content.Team.SignerID)
			for _, risk := range content.Team.Risks {
				ids = append(ids, risk.OwnerID)
			}
		}
		return nil
	}
	if err = collect(c); err != nil {
		return err
	}
	if id > 0 {
		row, e := m.studioRecordOn(ctx, tx, binding.ID, id)
		if e != nil {
			return e
		}
		view, e := studioView(row)
		if e != nil {
			return e
		}
		if err = collect(view.Content); err != nil {
			return err
		}
	}
	slices.Sort(ids)
	for _, principal := range slices.Compact(ids) {
		if principal <= 0 {
			return ErrInvalid
		}
		if err = m.principals.Lock(ctx, tx, principal); err != nil {
			return err
		}
	}
	return nil
}
