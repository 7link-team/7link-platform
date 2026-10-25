package templates

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// VideoSource is bound by Execution through its archive and calibration capability. Every
// read proves current project authority and returns source-owned immutable facts.
type VideoSource func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (VideoExecution, error)
type VideoSourceConnector struct{ Bind func(VideoSource) error }
type VideoArtifact struct {
	EvidenceID guid.ID `json:"evidence_id"`
	BindingID  guid.ID `json:"binding_id"`
	FileID     guid.ID `json:"file_id"`
	VersionID  guid.ID `json:"version_id"`
	SHA256     string  `json:"sha256"`
}
type VideoQC struct {
	TaskID        guid.ID `json:"task_id"`
	ResultID      guid.ID `json:"result_id"`
	ResultVersion int     `json:"result_version"`
	Verifier      string  `json:"verifier"`
	Method        string  `json:"method"`
	Stage         string  `json:"stage"`
	Verdict       string  `json:"verdict"`
	Reason        string  `json:"reason"`
	ReceiptSHA256 string  `json:"receipt_sha256"`
	HeadCommit    string  `json:"head_commit"`
}
type VideoExecution struct {
	ExecutionID guid.ID         `json:"execution_id"`
	WorkItemID  guid.ID         `json:"work_item_id"`
	State       string          `json:"state"`
	Artifacts   []VideoArtifact `json:"artifacts"`
	QC          []VideoQC       `json:"qc"`
}
type VideoData struct {
	Role         string                  `json:"role" enum:"SHOT,REFERENCE,MASTER,CANDIDATE"`
	Character    string                  `json:"character" maxLength:"200"`
	Framing      string                  `json:"framing" maxLength:"500"`
	ExecutionID  guid.ID                 `json:"execution_id"`
	GenerationID guid.ID                 `json:"generation_id"`
	EvidenceID   guid.ID                 `json:"evidence_id"`
	OutputIndex  int                     `json:"output_index" minimum:"0" maximum:"19"`
	Artifact     *VideoArtifact          `json:"artifact,omitempty"`
	Generation   *ProviderGenerationView `json:"generation,omitempty"`
}
type VideoEdit struct {
	Action   string        `json:"action" enum:"SAVE,GENERATE,IMPORT,SELECT"`
	Version  int           `json:"version" minimum:"0"`
	ParentID guid.ID       `json:"parent_id"`
	Reason   string        `json:"reason" minLength:"1" maxLength:"2000"`
	Content  StudioContent `json:"content"`
}
type VideoWorkbench struct {
	Items        []StudioRecordView       `json:"items"`
	Executions   []VideoExecution         `json:"executions"`
	Generations  []ProviderGenerationView `json:"generations"`
	Providers    []ProviderView           `json:"providers"`
	Bindings     ProviderBindingsView     `json:"bindings"`
	Availability string                   `json:"availability"`
}

func (m *Module) provideVideo(s *module.Services) error {
	return module.Provide[VideoSourceConnector](s, VideoSourceConnector{Bind: func(source VideoSource) error {
		if source == nil || m.videoSource != nil || m.calibrationSealed {
			return ErrInvalid
		}
		m.videoSource = source
		return nil
	}})
}
func (m *Module) videoSourceOn(ctx context.Context, tx *gorm.DB, project, id guid.ID) (VideoExecution, error) {
	if m.videoSource == nil {
		return VideoExecution{}, ErrUnavailable
	}
	return m.videoSource(ctx, tx, authn.TenantIDFromContext(ctx), project, id)
}
func (m *Module) videoWorkbenchOn(ctx context.Context, tx *gorm.DB, project guid.ID) (VideoWorkbench, error) {
	out := VideoWorkbench{Items: []StudioRecordView{}, Executions: []VideoExecution{}, Generations: []ProviderGenerationView{}, Providers: []ProviderView{}, Availability: "UNAVAILABLE"}
	if err := m.authorizeOn(ctx, tx, "template_read", 0); err != nil {
		return out, err
	}
	b, err := m.instanceOn(ctx, tx, project)
	if err != nil {
		return out, err
	}
	v := query.BindVersions(tx, m.names)
	version, err := v.WithContext(ctx).Where(v.ID.Eq(b.TemplateVersionID)).Take()
	if err != nil {
		return out, err
	}
	definition, err := versionView(version)
	if err != nil {
		return out, err
	}
	if definition.Definition.Domain != "VIDEO" {
		return out, ErrConflict
	}
	q := query.BindStudioRecords(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.BindingID.Eq(b.ID), q.Domain.Eq("VIDEO")).Order(q.ID).Find()
	if err != nil {
		return out, err
	}
	execs := map[guid.ID]bool{}
	for _, row := range rows {
		view, e := studioView(row)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, view)
		if view.Content.Video != nil && view.Content.Video.ExecutionID > 0 {
			execs[view.Content.Video.ExecutionID] = true
		}
	}
	ids := []guid.ID{}
	for id := range execs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		fact, e := m.videoSourceOn(ctx, tx, project, id)
		if e != nil {
			return out, e
		}
		out.Executions = append(out.Executions, fact)
	}
	g := query.BindProviderGenerations(tx, m.names)
	generations, err := g.WithContext(ctx).Where(g.TenantID.Eq(authn.TenantIDFromContext(ctx)), g.BindingID.Eq(b.ID), g.Kind.Eq("VIDEO_GEN")).Order(g.ID).Find()
	if err != nil {
		return out, err
	}
	for _, generation := range generations {
		view, e := m.readProviderGenerationOn(ctx, tx, generation.ID)
		if e != nil {
			return out, e
		}
		out.Generations = append(out.Generations, view)
	}
	p := query.BindProviders(tx, m.names)
	providers, err := p.WithContext(ctx).Where(p.TenantID.Eq(authn.TenantIDFromContext(ctx)), p.Kind.Eq("VIDEO_GEN")).Order(p.ID).Find()
	if err != nil {
		return out, err
	}
	for _, provider := range providers {
		if err = m.authorizeOn(ctx, tx, "template_read", provider.CreatedBy); err != nil {
			continue
		}
		view := m.providerView(provider)
		out.Providers = append(out.Providers, view)
		if view.State == "ENABLED" && m.providerExecution != nil {
			out.Availability = "CONFIGURED"
		}
	}
	binding := query.BindBindings(tx, m.names)
	row, err := binding.WithContext(ctx).Where(binding.ID.Eq(b.ID)).Take()
	if err != nil {
		return out, err
	}
	out.Bindings, err = m.providerBindingsOn(ctx, tx, row)
	return out, err
}
func (m *Module) videoEditOn(ctx context.Context, tx *gorm.DB, project, id guid.ID, in VideoEdit) (StudioRecordView, error) {
	var empty StudioRecordView
	video := in.Content.Video
	if video != nil {
		snapshot := *video
		video = &snapshot
		in.Content.Video = video
	}
	if !novelShort(in.Reason, 2000) || in.Version < 0 {
		return empty, ErrInvalid
	}
	// Source principal locks precede project/binding locks on both command and replay.
	if video != nil && video.ExecutionID > 0 {
		if _, err := m.videoSourceOn(ctx, tx, project, video.ExecutionID); err != nil {
			return empty, err
		}
	}
	b, err := m.studioAuthorizeOn(ctx, tx, project)
	if err != nil {
		return empty, err
	}
	board, err := m.videoWorkbenchOn(ctx, tx, project)
	if err != nil {
		return empty, err
	}
	if in.Action == "SELECT" {
		row, e := m.studioRecordOn(ctx, tx, b.ID, id)
		if e != nil {
			return empty, e
		}
		view, e := studioView(row)
		if e != nil {
			return empty, e
		}
		if view.Content.Video != nil {
			if _, e = m.videoArtifactOn(ctx, tx, project, *view.Content.Video); e != nil {
				return empty, e
			}
		}
		return m.decideStudioOn(ctx, tx, project, id, StudioDecision{Version: in.Version, Action: "SELECT", Reason: in.Reason})
	}
	if video == nil || in.Content.Team != nil || in.Content.Novel != nil || !novelShort(in.Content.Title, 200) || len(in.Content.Text) > 20000 || len(video.Character) > 200 || len(video.Framing) > 500 {
		return empty, ErrInvalid
	}
	if video.Generation != nil || video.Artifact != nil {
		return empty, ErrInvalid
	}
	if in.Action == "GENERATE" {
		if id != 0 || video.ExecutionID <= 0 || in.ParentID <= 0 {
			return empty, ErrInvalid
		}
		parent, e := m.studioRecordOn(ctx, tx, b.ID, in.ParentID)
		if e != nil {
			return empty, e
		}
		if parent.Domain != "VIDEO" || parent.Kind != "SHOT" {
			return empty, ErrInvalid
		}
		parentView, e := studioView(parent)
		if e != nil {
			return empty, e
		}
		input, _ := json.Marshal(map[string]any{"shot_id": parent.ID, "shot_version": parent.Version, "shot_sha256": novelDigest(parentView.Content), "prompt": in.Content.Text, "duration_ms": in.Content.DurationMS, "seed": in.Content.Seed})
		generation, e := m.requestProviderGenerationOn(ctx, tx, ProviderGenerationRequest{ProjectID: project, ExecutionID: video.ExecutionID, Kind: "VIDEO_GEN", Input: input})
		if e != nil {
			return empty, e
		}
		// The generation row is the durable job; candidate import happens only
		// after completion and attachment of actual, hash-matching artifacts.
		return StudioRecordView{StudioRecord: *parent, Content: StudioContent{Title: in.Content.Title, Video: &VideoData{Role: "SHOT", GenerationID: generation.ID, Generation: &generation}}}, nil
	}
	kind := "SHOT"
	if in.Action == "IMPORT" {
		if id != 0 || in.ParentID <= 0 || video.OutputIndex < 0 || video.OutputIndex > 19 {
			return empty, ErrInvalid
		}
		parent, e := m.studioRecordOn(ctx, tx, b.ID, in.ParentID)
		if e != nil {
			return empty, e
		}
		if parent.Domain != "VIDEO" || parent.Kind != "SHOT" {
			return empty, ErrInvalid
		}
		generation, e := m.readProviderGenerationOn(ctx, tx, video.GenerationID)
		if e != nil {
			return empty, e
		}
		if generation.ProjectID != project || generation.ExecutionID != video.ExecutionID || generation.Kind != "VIDEO_GEN" || generation.State != "COMPLETED" || video.OutputIndex >= len(generation.Output.Outputs) {
			return empty, ErrConflict
		}
		// Only the exact shot request may import the output. A generation cannot
		// be relabelled to another shot after it has run.
		g := query.BindProviderGenerations(tx, m.names)
		source, e := g.WithContext(ctx).Where(g.ID.Eq(generation.ID)).Take()
		if e != nil {
			return empty, e
		}
		var request struct {
			ShotID      guid.ID `json:"shot_id"`
			ShotVersion int     `json:"shot_version"`
			ShotSHA256  string  `json:"shot_sha256"`
		}
		parentView, e := studioView(parent)
		if e != nil {
			return empty, e
		}
		if json.Unmarshal([]byte(source.Request), &request) != nil || request.ShotID != parent.ID || request.ShotSHA256 != novelDigest(parentView.Content) {
			return empty, ErrConflict
		}
		output := generation.Output.Outputs[video.OutputIndex]
		facts, e := m.videoSourceOn(ctx, tx, project, video.ExecutionID)
		if e != nil {
			return empty, e
		}
		var artifact *VideoArtifact
		for _, a := range facts.Artifacts {
			if a.SHA256 == output.SHA256 {
				snapshot := a
				artifact = &snapshot
				break
			}
		}
		if artifact == nil {
			return empty, ErrConflict
		}
		for _, r := range board.Items {
			if r.ParentID != nil && *r.ParentID == in.ParentID && r.Content.Video != nil && r.Content.Video.GenerationID == generation.ID && r.Content.Video.OutputIndex == video.OutputIndex {
				return r, nil
			}
		}
		video.Role = "CANDIDATE"
		video.Generation = &generation
		video.Artifact = artifact
		kind = "CANDIDATE"
		in.Content.Source = output.FileRef
		in.Content.Provider = generation.AdapterKey
		in.Content.Model = output.Model + "/" + output.ModelVersion
		in.Content.Seed = output.Seed
	} else if in.Action == "SAVE" {
		if !slices.Contains([]string{"SHOT", "REFERENCE", "MASTER"}, video.Role) || in.Content.DurationMS < 0 || in.Content.DurationMS > 86400000 || len(in.Content.Source) > 2000 {
			return empty, ErrInvalid
		}
		if video.Role != "SHOT" {
			if video.ExecutionID <= 0 {
				return empty, ErrInvalid
			}
			artifact, e := m.videoArtifactOn(ctx, tx, project, *video)
			if e != nil {
				return empty, e
			}
			video.Artifact = &artifact
		}
	} else {
		return empty, ErrInvalid
	}
	raw, err := json.Marshal(in.Content)
	if err != nil {
		return empty, err
	}
	q := query.BindStudioRecords(tx, m.names)
	var row *model.StudioRecord
	if id > 0 {
		row, err = m.studioRecordOn(ctx, tx, b.ID, id)
		if err != nil {
			return empty, err
		}
		if row.Kind != "SHOT" || row.Domain != "VIDEO" || row.Version != in.Version {
			return empty, ErrConflict
		}
		result, e := q.WithContext(ctx).Where(q.ID.Eq(id), q.Version.Eq(in.Version)).Updates(map[string]any{"payload": string(raw), "version": in.Version + 1})
		if e != nil {
			return empty, e
		}
		if result.RowsAffected != 1 {
			return empty, ErrConflict
		}
		row.Version++
		row.Payload = string(raw)
	} else {
		if in.Version != 0 {
			return empty, ErrInvalid
		}
		next, e := m.ids.Next()
		if e != nil {
			return empty, e
		}
		row = &model.StudioRecord{ID: guid.ID(next), TenantID: b.TenantID, BindingID: b.ID, Domain: "VIDEO", Kind: kind, State: "OPEN", Payload: string(raw), Version: 1, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: time.Now().UTC()}
		if kind == "CANDIDATE" {
			row.ParentID = &in.ParentID
		}
		if err = q.WithContext(ctx).Create(row); err != nil {
			return empty, err
		}
	}
	if err = m.studioEventOn(ctx, tx, project, row, in.Action+":"+in.Reason); err != nil {
		return empty, err
	}
	return studioView(row)
}
func (m *Module) videoArtifactOn(ctx context.Context, tx *gorm.DB, project guid.ID, video VideoData) (VideoArtifact, error) {
	facts, err := m.videoSourceOn(ctx, tx, project, video.ExecutionID)
	if err != nil {
		return VideoArtifact{}, err
	}
	for _, a := range facts.Artifacts {
		if video.Artifact != nil && *video.Artifact == a || video.Artifact == nil && a.EvidenceID == video.EvidenceID {
			return a, nil
		}
	}
	return VideoArtifact{}, ErrNotFound
}
func (m *Module) videoOpenOn(ctx context.Context, tx *gorm.DB, project, id guid.ID) (files.PinnedResourceRead, error) {
	if m.evalFiles == nil {
		return files.PinnedResourceRead{}, ErrUnavailable
	}
	board, err := m.videoWorkbenchOn(ctx, tx, project)
	if err != nil {
		return files.PinnedResourceRead{}, err
	}
	for _, row := range board.Items {
		if row.ID != id || row.Content.Video == nil || row.Content.Video.Artifact == nil {
			continue
		}
		video := row.Content.Video
		artifact, e := m.videoArtifactOn(ctx, tx, project, *video)
		if e != nil {
			return files.PinnedResourceRead{}, e
		}
		resource, e := m.evalFiles.OpenOn(ctx, tx, artifact.BindingID)
		if e != nil {
			return files.PinnedResourceRead{}, e
		}
		if resource.BindingID != artifact.BindingID || resource.FileID != artifact.FileID || resource.VersionID != artifact.VersionID || resource.SHA256 != artifact.SHA256 || (!strings.HasPrefix(resource.MIME, "video/") && !strings.HasPrefix(resource.MIME, "image/") && !strings.HasPrefix(resource.MIME, "audio/")) {
			_ = resource.Reader.Close()
			return files.PinnedResourceRead{}, ErrConflict
		}
		return resource, nil
	}
	return files.PinnedResourceRead{}, ErrNotFound
}
