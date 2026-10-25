package agents

import (
	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"reflect"
	"strings"
	"time"
)

type ManifestPublisher struct{ Publish func(context.Context) error }
type ManifestDocument struct {
	ManifestVersion string      `json:"manifest_version"`
	SHA256          string      `json:"sha256"`
	PublishedAt     time.Time   `json:"published_at"`
	Operations      []Operation `json:"operations"`
}
type ManifestVersionView struct {
	ManifestVersion   string    `json:"manifest_version"`
	SHA256            string    `json:"sha256"`
	OperationCount    int       `json:"operation_count"`
	PublishedAt       time.Time `json:"published_at"`
	SupersedesVersion string    `json:"supersedes_version,omitempty"`
}

func manifestVersionView(r *model.ManifestVersion) ManifestVersionView {
	return ManifestVersionView{r.ManifestVersion, r.SHA256, r.OperationCount, r.PublishedAt, r.SupersedesVersion}
}

// The hash covers sorted, canonical operation contracts only; timestamps and
// version labels must not cause a fresh publication on an identical deployment.
func manifestBytes(ops []Operation) ([]byte, string, error) {
	raw, err := json.Marshal(ops)
	if err != nil {
		return nil, "", err
	}
	var canonical any
	if err = json.Unmarshal(raw, &canonical); err != nil {
		return nil, "", err
	}
	raw, err = json.Marshal(canonical)
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), err
}
func nextManifestVersion(previous string, old, current []Operation) string {
	major, minor, patch := 1, 0, 0
	if previous == "" {
		return "1.0.0"
	}
	if _, err := fmt.Sscanf(previous, "%d.%d.%d", &major, &minor, &patch); err != nil {
		return ""
	}
	before := map[string]Operation{}
	after := map[string]Operation{}
	for _, op := range old {
		before[op.Name] = op
	}
	for _, op := range current {
		after[op.Name] = op
	}
	breaking, addition := false, len(current) > len(old)
	for name, op := range before {
		next, exists := after[name]
		if !exists {
			breaking = true
			continue
		}
		op.Summary = ""
		next.Summary = ""
		inputSame := sameJSON(op.InputSchema, next.InputSchema)
		outputSame := sameJSON(op.OutputSchema, next.OutputSchema)
		if !inputSame {
			if optionalSchemaExtension(op.InputSchema, next.InputSchema) {
				addition = true
			} else {
				breaking = true
			}
		}
		if !outputSame {
			if optionalSchemaExtension(op.OutputSchema, next.OutputSchema) {
				addition = true
			} else {
				breaking = true
			}
		}
		op.InputSchema = nil
		next.InputSchema = nil
		op.OutputSchema = nil
		next.OutputSchema = nil
		if !reflect.DeepEqual(op, next) {
			breaking = true
		}
	}
	if breaking {
		major++
		minor = 0
		patch = 0
	} else if addition {
		minor++
		patch = 0
	} else {
		patch++
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}
func (m *Module) publishManifest(ctx context.Context) error {
	db, err := m.database()
	if err != nil {
		return err
	}
	if m.ids == nil || m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	ops := m.registry.List()
	raw, hash, err := manifestBytes(ops)
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindManifestVersion(tx, m.names)
		// Published rows are immutable. Unique version constraints also fence
		// concurrent deployment publishers; a losing publisher must retry startup.
		latest, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Order(q.ID.Desc()).First()
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if latest != nil && latest.SHA256 == hash {
			return nil
		}
		previous := ""
		var old []Operation
		if latest != nil {
			previous = latest.ManifestVersion
			if json.Unmarshal([]byte(latest.Document), &old) != nil {
				return ErrInvalid
			}
		}
		version := nextManifestVersion(previous, old, ops)
		if version == "" {
			return ErrInvalid
		}
		id, err := m.ids.Next()
		if err != nil {
			return err
		}
		if id <= 0 {
			return ErrUnavailable
		}
		row := model.ManifestVersion{ID: guid.ID(id), TenantID: 0, ManifestVersion: version, SHA256: hash, Document: string(raw), OperationCount: len(ops), SupersedesVersion: previous, PublishedAt: m.now()}
		if err = q.WithContext(ctx).Create(&row); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, 0, "agents.manifest_published", "operation_manifest", row.ID)
		event.Detail["action"] = "operation_manifest_published"
		event.Detail["authority_source"] = "SYSTEM:agents.manifest_publish"
		event.Detail["manifest_version"] = version
		event.Detail["sha256"] = hash
		event.Detail["operation_count"] = len(ops)
		if err = m.audit(ctx, tx, event); err != nil {
			return err
		}
		_, err = m.events.Publish(ctx, tx, events.Event{TenantID: 0, Type: "agents.manifest_published", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:agents.manifest_publish"}, Subject: events.Subject{Kind: "operation_manifest", ID: row.ID}, SubjectVersion: 1, Data: map[string]any{"action": "operation_manifest_published", "manifest_version": version, "sha256": hash}})
		return err
	})
}
func (m *Module) manifestOn(ctx context.Context, tx *gorm.DB, version string) (ManifestDocument, error) {
	q := query.BindManifestVersion(tx, m.names)
	read := q.WithContext(ctx)
	if version != "" {
		read = read.Where(q.ManifestVersion.Eq(version))
	}
	row, err := read.Order(q.ID.Desc()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ManifestDocument{}, ErrNotFound
	}
	if err != nil {
		return ManifestDocument{}, err
	}
	var ops []Operation
	if json.Unmarshal([]byte(row.Document), &ops) != nil {
		return ManifestDocument{}, ErrInvalid
	}
	_, hash, err := manifestBytes(ops)
	if err != nil || hash != row.SHA256 {
		return ManifestDocument{}, ErrInvalid
	}
	return ManifestDocument{row.ManifestVersion, row.SHA256, row.PublishedAt, ops}, nil
}
func etagMatches(header, hash string) bool {
	for _, part := range strings.Split(header, ",") {
		value := strings.TrimSpace(part)
		if value == "*" || strings.TrimPrefix(value, "W/") == `"`+hash+`"` {
			return true
		}
	}
	return false
}

func sameJSON(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

// Only additive optional object properties qualify for a minor release. Any
// unrecognized constraint change remains a conservative breaking change.
func optionalSchemaExtension(a, b json.RawMessage) bool {
	var before, after map[string]any
	if json.Unmarshal(a, &before) != nil || json.Unmarshal(b, &after) != nil {
		return false
	}
	if before["type"] != "object" || after["type"] != "object" {
		return false
	}
	oldProps, _ := before["properties"].(map[string]any)
	newProps, _ := after["properties"].(map[string]any)
	if len(newProps) <= len(oldProps) {
		return false
	}
	for name, old := range oldProps {
		if !reflect.DeepEqual(old, newProps[name]) {
			return false
		}
	}
	// Newly added properties must not appear in required.
	if required, ok := after["required"].([]any); ok {
		for _, name := range required {
			key, ok := name.(string)
			if !ok {
				return false
			}
			if _, exists := oldProps[key]; !exists {
				return false
			}
		}
	}
	delete(before, "properties")
	delete(after, "properties")
	return reflect.DeepEqual(before, after)
}

// FinalizeMountedOperations seals the mounted catalog and publishes its manifest.
func FinalizeMountedOperations(services *module.Services, api huma.API) error {
	catalog, err := module.Resolve[MountedOperationCatalog](services)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if catalog.Finalize == nil {
		return ErrUnavailable
	}
	if err := catalog.Finalize(api); err != nil {
		return err
	}
	publisher, err := module.Resolve[ManifestPublisher](services)
	if err != nil {
		return err
	}
	if publisher.Publish == nil {
		return ErrUnavailable
	}
	return publisher.Publish(context.Background())
}
