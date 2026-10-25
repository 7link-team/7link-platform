package templates

import (
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"strings"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNovelHTTPReplayCurrentAuthorityAndIsolation(t *testing.T) {
	m, _, ctx := bindStudio(t, "NOVEL")
	count := 0
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { count++; return nil }
	in := &novelCreateInput{ProjectID: "99", IdempotencyKey: "bible-once", Body: novelBible()}
	first, e := m.novelCreateHTTP(ctx, in)
	require.NoError(t, e)
	replay, e := m.novelCreateHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data.ID, replay.Body.Data.ID)
	require.Equal(t, 1, count)
	in.Body.Content.Title = "Different request"
	_, e = m.novelCreateHTTP(ctx, in)
	assertStatus(t, e, 409)
	in.Body = novelBible()
	nonhuman := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 11, SubjectType: authn.SubjectTypeServiceAccount})
	_, e = m.novelCreateHTTP(nonhuman, in)
	assertStatus(t, e, 403)
	foreign := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	_, e = m.novelReadHTTP(foreign, &novelInput{ProjectID: "99"})
	assertStatus(t, e, 404)
	_, e = m.novelReadHTTP(ctx, &novelInput{ProjectID: "bad"})
	assertStatus(t, e, 422)
	_, e = m.novelEditHTTP(ctx, &novelEditInput{ProjectID: "99", ID: first.Body.Data.ID.String(), Body: NovelEdit{Action: "RUN"}})
	assertStatus(t, e, 422)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = m.novelCreateHTTP(ctx, in)
	assertStatus(t, e, 403)
	_, e = m.novelReadHTTP(ctx, &novelInput{ProjectID: "99"})
	assertStatus(t, e, 403)
}
func TestNovelHTTPRunReplayAndCAS(t *testing.T) {
	m, _, ctx := bindStudio(t, "NOVEL")
	bible, e := m.novelCreateHTTP(ctx, &novelCreateInput{ProjectID: "99", IdempotencyKey: "bible", Body: novelBible()})
	require.NoError(t, e)
	_, e = m.novelCreateHTTP(ctx, &novelCreateInput{ProjectID: "99", IdempotencyKey: "chapter", Body: novelChapter(1, "full", 1)})
	require.NoError(t, e)
	run := &novelCreateInput{ProjectID: "99", IdempotencyKey: "run", Body: NovelEdit{Action: "RUN", Reason: "Every chapter"}}
	first, e := m.novelCreateHTTP(ctx, run)
	require.NoError(t, e)
	second, e := m.novelCreateHTTP(ctx, run)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data.ID, second.Body.Data.ID)
	input := novelBible()
	input.Version = 1
	input.Content.Text = "Updated outline"
	edit := &novelEditInput{ProjectID: "99", ID: bible.Body.Data.ID.String(), IdempotencyKey: "edit", Body: input}
	updated, e := m.novelEditHTTP(ctx, edit)
	require.NoError(t, e)
	require.Equal(t, 2, updated.Body.Data.Version)
	edit.IdempotencyKey = "stale"
	_, e = m.novelEditHTTP(ctx, edit)
	assertStatus(t, e, 409)
	board, e := m.novelReadHTTP(ctx, &novelInput{ProjectID: "99"})
	require.NoError(t, e)
	require.True(t, board.Body.Data.Stale)
	require.Len(t, board.Body.Data.Items, 2)
}

func TestNovelHTTPNullableChapterReferencesWire(t *testing.T) {
	m, _, ctx := bindStudio(t, "NOVEL")
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "novel-wire", Method: "POST", Path: "/projects/{project_id}/novel-workbench", DefaultStatus: 201}, func(_ context.Context, in *novelCreateInput) (*respx.Body[StudioRecordView], error) {
		return m.novelCreateHTTP(ctx, in)
	})
	input := novelBible()
	input.Content.Novel.Beats = []NovelBeat{{Name: "Midpoint", TargetPercent: 50}}
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"chapter_id":null`)
	url := "/projects/99/novel-workbench"
	for _, invalid := range []string{`"0"`, `""`} {
		bad := strings.Replace(string(raw), `"chapter_id":null`, `"chapter_id":`+invalid, 1)
		require.Equal(t, 422, api.Post(url, "Idempotency-Key: bad", strings.NewReader(bad)).Code)
	}
	// An outline beat must reference a recorded chapter, even though the ID wire type is nullable.
	response := api.Post(url, "Idempotency-Key: unlinked-bible", strings.NewReader(string(raw)))
	require.Equal(t, 422, response.Code, response.Body.String())
	chapter, err := m.novelCreateHTTP(ctx, &novelCreateInput{ProjectID: "99", IdempotencyKey: "chapter", Body: novelChapter(1, "full", 1)})
	require.NoError(t, err)
	input.Content.Novel.Beats[0].ChapterID = chapter.Body.Data.ID
	raw, err = json.Marshal(input)
	require.NoError(t, err)
	response = api.Post(url, "Idempotency-Key: actual-bible", strings.NewReader(string(raw)))
	require.Equal(t, 201, response.Code, response.Body.String())
	input = NovelEdit{Action: "SAVE", Kind: "FORESHADOW", Reason: "Record open thread", Content: StudioContent{Title: "Gate", Novel: &NovelData{Entries: []NovelEntry{}, Characters: []NovelCharacter{}, Beats: []NovelBeat{}, Foreshadow: &NovelForeshadow{Planted: chapter.Body.Data.ID, Planned: chapter.Body.Data.ID}}}}
	raw, err = json.Marshal(input)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"resolved":null`)
	response = api.Post(url, "Idempotency-Key: actual-thread", strings.NewReader(string(raw)))
	require.Equal(t, 201, response.Code, response.Body.String())
	bad := strings.Replace(string(raw), `"resolved":null`, `"resolved":"0"`, 1)
	require.Equal(t, 422, api.Post(url, "Idempotency-Key: bad-thread", strings.NewReader(bad)).Code)
}
