package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CodeLinkInput struct {
	Kind         string  `json:"kind" enum:"COMMIT,MERGE_REQUEST,BRANCH"`
	RepositoryID guid.ID `json:"repository_id"`
	Ref          string  `json:"ref" minLength:"1" maxLength:"200"`
	Title        string  `json:"title,omitempty" maxLength:"300"`
	URL          string  `json:"url,omitempty" maxLength:"500"`
}

// LocalCodeLinks is the module-owned trusted integration capability. Callers
// supply operator-configured local paths and retain the current IAM identity.
type LocalCodeLinks struct {
	Import                func(context.Context, guid.ID, guid.ID, guid.ID, guid.ID, string) (CodeLinkImport, error)
	WorkEvidenceOn        func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) (CodeLinkEvidence, error)
	RequirementEvidenceOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) (CodeLinkEvidence, error)
}

func (m *Module) provideCodeLinks(services *module.Services) error {
	return module.Provide[LocalCodeLinks](services, LocalCodeLinks{Import: m.ImportLocalGit, WorkEvidenceOn: m.CodeLinkEvidenceOn, RequirementEvidenceOn: m.RequirementCodeLinkEvidenceOn})
}

type CodeReference struct {
	Token, Kind string
	Number      int
}

var codeToken = regexp.MustCompile(`#(WI|R)-([1-9][0-9]{0,8})`)

func codeWord(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}

// ParseCodeReferences recognizes project-local identifiers only; no global
// snowflake ID lookup can accidentally associate an item from another project.
func ParseCodeReferences(text string) []CodeReference {
	out := []CodeReference{}
	seen := map[string]bool{}
	for _, match := range codeToken.FindAllStringSubmatchIndex(text, -1) {
		start, end := match[0], match[1]
		if start > 0 && codeWord(text[start-1]) || end < len(text) && codeWord(text[end]) {
			continue
		}
		token := text[start:end]
		if seen[token] {
			continue
		}
		seen[token] = true
		number, _ := strconv.Atoi(text[match[4]:match[5]])
		out = append(out, CodeReference{token, text[match[2]:match[3]], number})
	}
	return out
}

var commitRef = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

func codeLinkEventType(target string) string {
	if target == "requirement" {
		return "requirement.code_link_created"
	}
	return "code_link.created"
}
func (m *Module) createRequirementCodeLinkOn(ctx context.Context, tx *gorm.DB, tenant, project, requirement, actor guid.ID, in CodeLinkInput, provenance string) (model.CodeLink, error) {
	var empty model.CodeLink
	if !validCodeLink(in) || !slices.Contains([]string{"MANUAL_REFERENCE", "LOCAL_GIT_OBSERVED"}, provenance) {
		return empty, ErrInvalidArgument
	}
	c, err := m.requirementWriterOn(ctx, tx, tenant, project, actor)
	if err != nil {
		return empty, err
	}
	q := query.BindRequirements(tx, m.service.names)
	scope, err := workScope(c, project, q.CreatedBy)
	if err != nil {
		return empty, err
	}
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(requirement), q.ArchivedAt.IsNull())
	if scope != nil {
		base = base.Where(scope)
	}
	row, err := base.Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrProjectNotFound
	}
	if err != nil {
		return empty, err
	}
	return m.recordCodeLinkOn(ctx, tx, tenant, project, requirement, actor, "requirement", int64(row.Version), in, provenance)
}
func validCodeLink(in CodeLinkInput) bool {
	if in.RepositoryID <= 0 || !slices.Contains([]string{"COMMIT", "MERGE_REQUEST", "BRANCH"}, in.Kind) || strings.TrimSpace(in.Ref) == "" || utf8.RuneCountInString(in.Ref) > 200 || utf8.RuneCountInString(in.Title) > 300 || strings.ContainsAny(in.Ref, "\x00\r\n") || len(in.URL) > 500 {
		return false
	}
	if in.Kind == "COMMIT" && !commitRef.MatchString(in.Ref) {
		return false
	}
	if in.URL != "" {
		u, e := url.Parse(in.URL)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return false
		}
	}
	return true
}
func (m *Module) codeRepositoryOn(ctx context.Context, tx *gorm.DB, tenant, project, repository guid.ID) (*model.ProjectRepository, error) {
	q := query.BindRepositories(tx, m.service.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(repository), q.ArchivedAt.IsNull()).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, ErrProjectNotFound
	}
	return row, e
}
func (m *Module) createCodeLinkOn(ctx context.Context, tx *gorm.DB, tenant, project, work, actor, owner guid.ID, in CodeLinkInput, provenance string) (model.CodeLink, error) {
	var empty model.CodeLink
	if !validCodeLink(in) || !slices.Contains([]string{"MANUAL_REFERENCE", "LOCAL_GIT_OBSERVED"}, provenance) {
		return empty, ErrInvalidArgument
	}
	_, w, _, err := m.lockedWorkOn(ctx, tx, tenant, project, work, actor, owner, "work_item_write")
	if err != nil {
		return empty, err
	}
	return m.recordCodeLinkOn(ctx, tx, tenant, project, work, actor, "work_item", int64(w.Version), in, provenance)
}
func (m *Module) recordCodeLinkOn(ctx context.Context, tx *gorm.DB, tenant, project, target, actor guid.ID, targetKind string, version int64, in CodeLinkInput, provenance string) (model.CodeLink, error) {
	var empty model.CodeLink
	repository, err := m.codeRepositoryOn(ctx, tx, tenant, project, in.RepositoryID)
	if err != nil {
		return empty, err
	}
	table := query.BindCodeLinks(tx, m.service.names).WithContext(ctx)
	var existing model.CodeLink
	err = table.Where("tenant_id = ? AND target_kind = ? AND target_id = ? AND repository_id = ? AND kind = ? AND ref = ?", tenant, targetKind, target, in.RepositoryID, in.Kind, in.Ref).First(&existing).Error
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, err
	}
	if m.ids == nil || m.service.audit == nil || m.service.events == nil {
		return empty, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	if id <= 0 {
		return empty, ErrUnavailable
	}
	source := "MANUAL"
	if provenance == "LOCAL_GIT_OBSERVED" {
		source = "LOCAL_GIT"
	}
	snapshot, err := json.Marshal(struct{ Kind, Repository, Ref, Title, URL, Source, Provenance string }{in.Kind, repository.Name, in.Ref, in.Title, in.URL, source, provenance})
	if err != nil {
		return empty, err
	}
	sum := sha256.Sum256(snapshot)
	row := model.CodeLink{ID: guid.ID(id), TenantID: tenant, ProjectID: project, TargetKind: targetKind, TargetID: target, Kind: in.Kind, RepositoryID: repository.ID, Repository: repository.Name, Ref: in.Ref, Title: in.Title, URL: in.URL, Source: source, Provenance: provenance, Snapshot: string(snapshot), ContentHash: hex.EncodeToString(sum[:]), CreatedBy: actor, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if err = table.Create(&row).Error; err != nil {
		return empty, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.code_link_created", targetKind, target)
	event.Detail["code_link_id"], event.Detail["provenance"], event.Detail["content_sha256"], event.Detail["authority_source"] = row.ID, provenance, row.ContentHash, "IAM:"+targetKind+"_write"
	if err = m.service.audit(ctx, tx, event); err != nil {
		return empty, err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: codeLinkEventType(targetKind), Subject: events.Subject{Kind: targetKind, ID: target}, SubjectVersion: version, Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "IAM:" + targetKind + "_write"}, Data: map[string]any{"code_link_id": row.ID, "kind": row.Kind, "provenance": provenance, "content_sha256": row.ContentHash}})
	return row, err
}

// CodeLinkEvidence is an immutable reference snapshot. Observed means a local
// Git object was actually read; neither value means merged, deployed or passed.
type CodeLinkEvidence struct {
	ID         guid.ID `json:"id"`
	Kind       string  `json:"kind"`
	Provenance string  `json:"provenance"`
	Snapshot   string  `json:"snapshot"`
	SHA256     string  `json:"sha256"`
	Observed   bool    `json:"observed"`
}

func (m *Module) CodeLinkEvidenceOn(ctx context.Context, tx *gorm.DB, tenant, project, work, id, actor guid.ID) (CodeLinkEvidence, error) {
	var empty CodeLinkEvidence
	if _, err := m.readWork(ctx, tx, tenant, project, work, actor); err != nil {
		return empty, err
	}
	var row model.CodeLink
	if err := query.BindCodeLinks(tx, m.service.names).WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND target_kind = ? AND target_id = ? AND id = ?", tenant, project, "work_item", work, id).First(&row).Error; err != nil {
		return empty, err
	}
	sum := sha256.Sum256([]byte(row.Snapshot))
	if hex.EncodeToString(sum[:]) != row.ContentHash {
		return empty, ErrUnavailable
	}
	return CodeLinkEvidence{row.ID, row.Kind, row.Provenance, row.Snapshot, row.ContentHash, row.Provenance == "LOCAL_GIT_OBSERVED"}, nil
}
func (m *Module) RequirementCodeLinkEvidenceOn(ctx context.Context, tx *gorm.DB, tenant, project, requirement, id, actor guid.ID) (CodeLinkEvidence, error) {
	var empty CodeLinkEvidence
	if _, err := m.readRequirement(ctx, tx, tenant, project, requirement, actor); err != nil {
		return empty, err
	}
	var row model.CodeLink
	if err := query.BindCodeLinks(tx, m.service.names).WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND target_kind = ? AND target_id = ? AND id = ?", tenant, project, "requirement", requirement, id).First(&row).Error; err != nil {
		return empty, err
	}
	sum := sha256.Sum256([]byte(row.Snapshot))
	if hex.EncodeToString(sum[:]) != row.ContentHash {
		return empty, ErrUnavailable
	}
	return CodeLinkEvidence{row.ID, row.Kind, row.Provenance, row.Snapshot, row.ContentHash, row.Provenance == "LOCAL_GIT_OBSERVED"}, nil
}

type LocalGitObject struct{ Kind, Ref, Title string }
type CodeLinkImport struct {
	Links   []model.CodeLink     `json:"links"`
	Ignored []model.CodeLinkScan `json:"ignored"`
}

// ImportLocalGit is a trusted server-side adapter entry point. The path must
// come from operator configuration, never a project repository URL or request.
// Authorization is checked before disk access and again in the write transaction.
func (m *Module) ImportLocalGit(ctx context.Context, tenant, project, repository, actor guid.ID, trustedPath string) (CodeLinkImport, error) {
	out := CodeLinkImport{Links: []model.CodeLink{}, Ignored: []model.CodeLinkScan{}}
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return out, ErrUnavailable
	}
	if actor <= 0 || authn.PrincipalIDFromContext(ctx) != actor || authn.TenantIDFromContext(ctx) != tenant {
		return out, ErrWorkForbidden
	}
	db := m.runtime.Database().Write()
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.authorizeOn(ctx, tx, tenant, project, "work_item_write"); e != nil {
			return e
		}
		_, e := m.codeRepositoryOn(ctx, tx, tenant, project, repository)
		return e
	})
	if err != nil {
		return out, err
	}
	objects, err := ReadLocalGit(ctx, trustedPath)
	if err != nil {
		return out, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		repo, e := m.codeRepositoryOn(ctx, tx, tenant, project, repository)
		if e != nil {
			return e
		}
		out, e = m.importCodeObjectsOn(ctx, tx, tenant, project, actor, repo, objects)
		return e
	})
	return out, err
}

// importCodeObjectsOn consumes only observations produced by the local adapter,
// after the configured repository belongs to this project. Remote MR state is
// deliberately absent from this contract.
func (m *Module) importCodeObjectsOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, repository *model.ProjectRepository, objects []LocalGitObject) (CodeLinkImport, error) {
	out := CodeLinkImport{Links: []model.CodeLink{}, Ignored: []model.CodeLinkScan{}}
	if len(objects) > 200 || repository == nil || repository.TenantID != tenant || repository.ProjectID != project {
		return out, ErrInvalidArgument
	}
	if m.ids == nil || m.principals.Lock == nil {
		return out, ErrUnavailable
	}
	principals := []guid.ID{actor}
	for _, object := range objects {
		for _, ref := range ParseCodeReferences(object.Title) {
			if ref.Kind != "WI" {
				continue
			}
			q := query.BindWorkItems(tx, m.service.names)
			w, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Number.Eq(ref.Number)).First()
			if errors.Is(e, gorm.ErrRecordNotFound) {
				continue
			}
			if e != nil {
				return out, e
			}
			principals = append(principals, w.AccountablePrincipalID)
			hidden, e := m.hiddenAcceptancePrincipalsOn(ctx, tx, tenant, project, w.ID)
			if e != nil {
				return out, e
			}
			principals = append(principals, hidden...)
		}
	}
	slices.Sort(principals)
	for _, id := range slices.Compact(principals) {
		if e := m.principals.Lock(ctx, tx, id); e != nil {
			return out, e
		}
	}
	if e := m.authorizeOn(ctx, tx, tenant, project, "work_item_write"); e != nil {
		return out, e
	}
	for _, object := range objects {
		if object.Kind != "COMMIT" && object.Kind != "BRANCH" {
			return out, ErrInvalidArgument
		}
		for _, ref := range ParseCodeReferences(object.Title) {
			var work *model.WorkItem
			reason := "UNKNOWN_REFERENCE"
			if ref.Kind == "WI" {
				q := query.BindWorkItems(tx, m.service.names)
				var err error
				work, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Number.Eq(ref.Number)).First()
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return out, err
				}
			} else {
				q := query.BindRequirements(tx, m.service.names)
				requirement, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Number.Eq(ref.Number), q.ArchivedAt.IsNull()).First()
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return out, err
				}
				if requirement != nil {
					row, e := m.createRequirementCodeLinkOn(ctx, tx, tenant, project, requirement.ID, actor, CodeLinkInput{Kind: object.Kind, RepositoryID: repository.ID, Ref: object.Ref, Title: truncateCodeTitle(object.Title)}, "LOCAL_GIT_OBSERVED")
					if e != nil {
						return out, e
					}
					out.Links = append(out.Links, row)
					continue
				}
			}
			if work == nil {
				id, err := m.ids.Next()
				if err != nil {
					return out, err
				}
				ignored := model.CodeLinkScan{ID: guid.ID(id), TenantID: tenant, ProjectID: project, RepositoryID: repository.ID, Ref: object.Ref, Token: ref.Token, Reason: reason, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
				if err = query.BindCodeLinkScans(tx, m.service.names).WithContext(ctx).Create(&ignored).Error; err != nil {
					return out, err
				}
				out.Ignored = append(out.Ignored, ignored)
				continue
			}
			row, err := m.createCodeLinkOn(ctx, tx, tenant, project, work.ID, actor, work.AccountablePrincipalID, CodeLinkInput{Kind: object.Kind, RepositoryID: repository.ID, Ref: object.Ref, Title: truncateCodeTitle(object.Title)}, "LOCAL_GIT_OBSERVED")
			if err != nil {
				return out, err
			}
			out.Links = append(out.Links, row)
		}
	}
	return out, nil
}
func truncateCodeTitle(title string) string {
	r := []rune(title)
	if len(r) > 300 {
		return string(r[:300])
	}
	return title
}
