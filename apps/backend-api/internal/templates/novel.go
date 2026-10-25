package templates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// NovelData holds authored structured facts. Only NovelReport contains server
// observations. An annotated fact is a human assertion, not semantic extraction.
type NovelData struct {
	Entries    []NovelEntry     `json:"entries" maxItems:"200"`
	Characters []NovelCharacter `json:"characters" maxItems:"200"`
	Beats      []NovelBeat      `json:"beats" maxItems:"100"`
	Chapter    *NovelChapter    `json:"chapter,omitempty"`
	Foreshadow *NovelForeshadow `json:"foreshadow,omitempty"`
	Report     *NovelReport     `json:"report,omitempty"`
}
type NovelEntry struct {
	Key       string `json:"key" maxLength:"100"`
	Category  string `json:"category" enum:"WORLD,LOCATION,TERM"`
	Entity    string `json:"entity" maxLength:"100"`
	Attribute string `json:"attribute" maxLength:"100"`
	Value     string `json:"value" maxLength:"500"`
	Text      string `json:"text" maxLength:"2000"`
	Invariant bool   `json:"invariant"`
}
type NovelCharacter struct {
	Name       string `json:"name" maxLength:"100"`
	Motivation string `json:"motivation" maxLength:"2000"`
	Arc        string `json:"arc" maxLength:"2000"`
}
type NovelBeat struct {
	Name          string  `json:"name" maxLength:"200"`
	TargetPercent int     `json:"target_percent" minimum:"0" maximum:"100"`
	ChapterID     guid.ID `json:"chapter_id"`
}
type NovelCitation struct {
	EntryKey string  `json:"entry_key,omitempty"`
	RecordID guid.ID `json:"record_id"`
	Version  int     `json:"version"`
	Start    int     `json:"start" minimum:"0"`
	Quote    string  `json:"quote" maxLength:"2000"`
	SHA256   string  `json:"sha256"`
}
type NovelFact struct {
	Entity    string `json:"entity" maxLength:"100"`
	Attribute string `json:"attribute" maxLength:"100"`
	Value     string `json:"value" maxLength:"500"`
	Tick      int    `json:"tick" minimum:"0"`
	Start     int    `json:"start" minimum:"0"`
	Quote     string `json:"quote" maxLength:"2000"`
}
type NovelChapter struct {
	Number      int         `json:"number" minimum:"1" maximum:"100000"`
	TargetWords int         `json:"target_words" minimum:"1" maximum:"1000000"`
	POV         string      `json:"pov" maxLength:"100"`
	Plan        string      `json:"plan" maxLength:"2000"`
	Facts       []NovelFact `json:"facts" maxItems:"200"`
}
type NovelForeshadow struct {
	Planted  guid.ID `json:"planted"`
	Planned  guid.ID `json:"planned"`
	Resolved guid.ID `json:"resolved"`
	Retained bool    `json:"retained"`
	Reason   string  `json:"reason" maxLength:"2000"`
}
type NovelIssue struct {
	Code     string        `json:"code"`
	Category string        `json:"category"`
	A        NovelCitation `json:"a"`
	B        NovelCitation `json:"b"`
}
type NovelCoverage struct {
	ID      guid.ID `json:"id"`
	Version int     `json:"version"`
	SHA256  string  `json:"sha256"`
	Words   int     `json:"words"`
}
type NovelReport struct {
	Engine               string          `json:"engine"`
	At                   time.Time       `json:"at"`
	InputSHA256          string          `json:"input_sha256"`
	Coverage             []NovelCoverage `json:"coverage"`
	Words                int             `json:"words"`
	Facts                int             `json:"facts"`
	Issues               []NovelIssue    `json:"issues"`
	Incomplete           []guid.ID       `json:"incomplete"`
	OpenForeshadows      []guid.ID       `json:"open_foreshadows"`
	CED                  string          `json:"ced"`
	SemanticAvailability string          `json:"semantic_availability"`
}
type NovelEdit struct {
	Version int           `json:"version" minimum:"0"`
	Action  string        `json:"action" enum:"SAVE,RUN"`
	Kind    string        `json:"kind" enum:"STORY_BIBLE,CHAPTER,FORESHADOW"`
	Reason  string        `json:"reason" minLength:"1" maxLength:"2000"`
	Content StudioContent `json:"content"`
}
type NovelWorkbench struct {
	Items       []StudioRecordView `json:"items"`
	Report      *StudioRecordView  `json:"report,omitempty"`
	InputSHA256 string             `json:"input_sha256"`
	Stale       bool               `json:"stale"`
}

func novelDigest(value any) string {
	b, _ := json.Marshal(value)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func novelWords(text string) int {
	// Han characters count individually; other Unicode letters/numbers form words.
	n, word := 0, false
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			n++
			word = false
		} else if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if !word {
				n++
			}
			word = true
		} else {
			word = false
		}
	}
	return n
}
func novelQuote(text string, start int, quote string) bool {
	r, q := []rune(text), []rune(quote)
	return start >= 0 && len(q) > 0 && len(quote) <= 2000 && start <= len(r) && len(q) <= len(r)-start && string(r[start:start+len(q)]) == quote
}
func novelCitation(row StudioRecordView, start int, quote string) NovelCitation {
	return NovelCitation{RecordID: row.ID, Version: row.Version, Start: start, Quote: quote, SHA256: novelDigest(row.Content)}
}
func novelEntryCitation(row StudioRecordView, e NovelEntry) NovelCitation {
	c := novelCitation(row, 0, e.Text)
	c.EntryKey = e.Key
	return c
}
func novelShort(s string, limit int) bool { return strings.TrimSpace(s) != "" && len(s) <= limit }

func validateNovel(in NovelEdit) error {
	c, n := in.Content, in.Content.Novel
	if in.Version < 0 || !novelShort(in.Reason, 2000) || !novelShort(c.Title, 200) || len(c.Text) > 20000 || len(c.Source) > 2000 || c.Team != nil || c.Video != nil || n == nil || n.Report != nil || len(n.Entries) > 200 || len(n.Characters) > 200 || len(n.Beats) > 100 {
		return ErrInvalid
	}
	switch in.Kind {
	case "STORY_BIBLE":
		if n.Chapter != nil || n.Foreshadow != nil {
			return ErrInvalid
		}
		keys, invariants := map[string]bool{}, map[string]bool{}
		for _, e := range n.Entries {
			key := e.Entity + "\x00" + e.Attribute
			if !novelShort(e.Key, 100) || keys[e.Key] || !slices.Contains([]string{"WORLD", "LOCATION", "TERM"}, e.Category) || !novelShort(e.Text, 2000) || !novelShort(e.Entity, 100) || !novelShort(e.Attribute, 100) || !novelShort(e.Value, 500) || e.Invariant && invariants[key] {
				return ErrInvalid
			}
			keys[e.Key] = true
			if e.Invariant {
				invariants[key] = true
			}
		}
		names := map[string]bool{}
		for _, ch := range n.Characters {
			if !novelShort(ch.Name, 100) || names[ch.Name] || !novelShort(ch.Motivation, 2000) || !novelShort(ch.Arc, 2000) {
				return ErrInvalid
			}
			names[ch.Name] = true
		}
		for _, b := range n.Beats {
			if !novelShort(b.Name, 200) || b.TargetPercent < 0 || b.TargetPercent > 100 || b.ChapterID <= 0 {
				return ErrInvalid
			}
		}
	case "CHAPTER":
		ch := n.Chapter
		if ch == nil || n.Foreshadow != nil || len(n.Entries)+len(n.Characters)+len(n.Beats) != 0 || ch.Number < 1 || ch.Number > 100000 || ch.TargetWords < 1 || ch.TargetWords > 1000000 || !novelShort(c.Text, 20000) || !novelShort(ch.POV, 100) || !novelShort(ch.Plan, 2000) || len(ch.Facts) > 200 {
			return ErrInvalid
		}
		for _, f := range ch.Facts {
			if !novelShort(f.Entity, 100) || !novelShort(f.Attribute, 100) || !novelShort(f.Value, 500) || f.Tick < 0 || !novelQuote(c.Text, f.Start, f.Quote) {
				return ErrInvalid
			}
		}
	case "FORESHADOW":
		f := n.Foreshadow
		if f == nil || n.Chapter != nil || len(n.Entries)+len(n.Characters)+len(n.Beats) != 0 || f.Planted <= 0 || f.Planned <= 0 || f.Resolved < 0 || f.Retained && (!novelShort(f.Reason, 2000) || f.Resolved > 0) || len(f.Reason) > 2000 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func (m *Module) novelWorkbenchOn(ctx context.Context, tx *gorm.DB, project guid.ID) (NovelWorkbench, error) {
	out := NovelWorkbench{Items: []StudioRecordView{}}
	b, err := m.instanceOn(ctx, tx, project)
	if err != nil {
		return out, err
	}
	if err = m.authorizeOn(ctx, tx, "template_read", 0); err != nil {
		return out, err
	}
	v := query.BindVersions(tx, m.names)
	version, err := v.WithContext(ctx).Where(v.ID.Eq(b.TemplateVersionID), v.TenantID.Eq(authn.TenantIDFromContext(ctx))).Take()
	if err != nil {
		return out, err
	}
	definition, err := versionView(version)
	if err != nil {
		return out, err
	}
	if definition.Definition.Domain != "NOVEL" {
		return out, ErrConflict
	}
	q := query.BindStudioRecords(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.BindingID.Eq(b.ID), q.Domain.Eq("NOVEL")).Order(q.ID).Find()
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		view, e := studioView(r)
		if e != nil {
			return out, e
		}
		if view.Content.Novel != nil && view.Content.Novel.Report != nil {
			out.Report = &view
		} else {
			out.Items = append(out.Items, view)
		}
	}
	out.InputSHA256 = novelDigest(out.Items)
	out.Stale = out.Report != nil && out.Report.Content.Novel.Report.InputSHA256 != out.InputSHA256
	return out, nil
}

func (m *Module) novelEditOn(ctx context.Context, tx *gorm.DB, project, id guid.ID, in NovelEdit) (StudioRecordView, error) {
	var empty StudioRecordView
	b, err := m.studioAuthorizeOn(ctx, tx, project)
	if err != nil {
		return empty, err
	}
	board, err := m.novelWorkbenchOn(ctx, tx, project)
	if err != nil {
		return empty, err
	}
	if !novelShort(in.Reason, 2000) {
		return empty, ErrInvalid
	}
	q := query.BindStudioRecords(tx, m.names)
	var row *model.StudioRecord
	if in.Action == "RUN" {
		if id != 0 || in.Version != 0 {
			return empty, ErrInvalid
		}
		var bible *StudioRecordView
		for i := range board.Items {
			if board.Items[i].Kind == "STORY_BIBLE" {
				bible = &board.Items[i]
			}
		}
		if bible == nil {
			return empty, ErrConflict
		}
		report := checkNovel(board)
		in.Content = StudioContent{Title: "Whole-book deterministic continuity", Novel: &NovelData{Report: &report}}
		in.Kind = "CONTINUITY"
		row = &model.StudioRecord{ParentID: &bible.ID}
	} else if in.Action == "SAVE" {
		if err = validateNovel(in); err != nil {
			return empty, err
		}
		if id > 0 {
			row, err = m.studioRecordOn(ctx, tx, b.ID, id)
			if err != nil {
				return empty, err
			}
			view, e := studioView(row)
			if e != nil {
				return empty, e
			}
			if row.Domain != "NOVEL" || row.Kind != in.Kind || row.Version != in.Version || view.Content.Novel != nil && view.Content.Novel.Report != nil {
				return empty, ErrConflict
			}
		} else if in.Version != 0 {
			return empty, ErrInvalid
		}
		for _, other := range board.Items {
			if other.ID == id {
				continue
			}
			if in.Kind == "STORY_BIBLE" && other.Kind == in.Kind {
				return empty, ErrConflict
			}
			if in.Kind == "CHAPTER" && other.Content.Novel != nil && other.Content.Novel.Chapter != nil && other.Content.Novel.Chapter.Number == in.Content.Novel.Chapter.Number {
				return empty, ErrConflict
			}
		}
		chapter := func(ref guid.ID) bool {
			return slices.ContainsFunc(board.Items, func(r StudioRecordView) bool { return r.ID == ref && r.Kind == "CHAPTER" })
		}
		for _, beat := range in.Content.Novel.Beats {
			if !chapter(beat.ChapterID) {
				return empty, ErrInvalid
			}
		}
		if f := in.Content.Novel.Foreshadow; f != nil {
			if !chapter(f.Planted) || !chapter(f.Planned) || f.Resolved > 0 && !chapter(f.Resolved) {
				return empty, ErrInvalid
			}
		}
	} else {
		return empty, ErrInvalid
	}
	raw, err := json.Marshal(in.Content)
	if err != nil {
		return empty, err
	}
	// A portable bound covers SQLite/Postgres and MySQL MEDIUMTEXT alike.
	if len(raw) > 8000000 {
		return empty, ErrInvalid
	}
	if id == 0 {
		if row == nil {
			row = &model.StudioRecord{}
		}
		next, e := m.ids.Next()
		if e != nil {
			return empty, e
		}
		row.ID = guid.ID(next)
		row.TenantID = b.TenantID
		row.BindingID = b.ID
		row.Domain = "NOVEL"
		row.Kind = in.Kind
		row.State = "OPEN"
		row.Version = 1
		row.CreatedBy = authn.PrincipalIDFromContext(ctx)
		row.CreatedAt = time.Now().UTC()
		row.Payload = string(raw)
		if err = q.WithContext(ctx).Create(row); err != nil {
			return empty, err
		}
	} else {
		result, e := q.WithContext(ctx).Where(q.ID.Eq(id), q.BindingID.Eq(b.ID), q.TenantID.Eq(b.TenantID), q.Version.Eq(in.Version)).Updates(map[string]any{"payload": string(raw), "version": in.Version + 1})
		if e != nil {
			return empty, e
		}
		if result.RowsAffected != 1 {
			return empty, ErrConflict
		}
		row.Payload = string(raw)
		row.Version++
	}
	if err = m.studioEventOn(ctx, tx, project, row, in.Action+":"+in.Reason); err != nil {
		return empty, err
	}
	return studioView(row)
}

// checkNovel exhaustively visits recorded chapters, never a sample. It detects
// contradictions in authored facts only and never emits a model or gate verdict.
func checkNovel(board NovelWorkbench) NovelReport {
	r := NovelReport{Engine: "structured-facts-v1", At: time.Now().UTC(), InputSHA256: board.InputSHA256, Coverage: []NovelCoverage{}, Issues: []NovelIssue{}, Incomplete: []guid.ID{}, OpenForeshadows: []guid.ID{}, SemanticAvailability: "UNAVAILABLE"}
	chapters := []StudioRecordView{}
	var bible StudioRecordView
	for _, row := range board.Items {
		if row.Kind == "CHAPTER" {
			chapters = append(chapters, row)
		}
		if row.Kind == "STORY_BIBLE" {
			bible = row
		}
		if row.Kind == "FORESHADOW" && (row.Content.Novel == nil || row.Content.Novel.Foreshadow == nil || row.Content.Novel.Foreshadow.Resolved == 0 && !row.Content.Novel.Foreshadow.Retained) {
			r.OpenForeshadows = append(r.OpenForeshadows, row.ID)
		}
	}
	slices.SortStableFunc(chapters, func(a, b StudioRecordView) int {
		number := func(c StudioRecordView) int {
			if c.Content.Novel != nil && c.Content.Novel.Chapter != nil {
				return c.Content.Novel.Chapter.Number
			}
			return 0
		}
		return number(a) - number(b)
	})
	type observed struct {
		fact NovelFact
		cite NovelCitation
	}
	seen, last := map[string]observed{}, map[string]observed{}
	invariants := map[string]NovelEntry{}
	if bible.Content.Novel != nil {
		for _, e := range bible.Content.Novel.Entries {
			if e.Invariant {
				invariants[e.Entity+"\x00"+e.Attribute] = e
			}
		}
	}
	for _, row := range chapters {
		words := novelWords(row.Content.Text)
		r.Words += words
		r.Coverage = append(r.Coverage, NovelCoverage{ID: row.ID, Version: row.Version, SHA256: novelDigest(row.Content), Words: words})
		if row.Content.Novel == nil || row.Content.Novel.Chapter == nil || len(row.Content.Novel.Chapter.Facts) == 0 {
			r.Incomplete = append(r.Incomplete, row.ID)
			continue
		}
		for _, f := range row.Content.Novel.Chapter.Facts {
			r.Facts++
			cite := novelCitation(row, f.Start, f.Quote)
			if !novelQuote(row.Content.Text, f.Start, f.Quote) {
				r.Incomplete = append(r.Incomplete, row.ID)
				continue
			}
			key := f.Entity + "\x00" + f.Attribute
			if e, ok := invariants[key]; ok && e.Value != f.Value {
				r.Issues = append(r.Issues, NovelIssue{Code: "BIBLE_INVARIANT", Category: "WORLD", A: novelEntryCitation(bible, e), B: cite})
			}
			at := key + "\x00" + strconv.Itoa(f.Tick)
			if prev, ok := seen[at]; ok && prev.fact.Value != f.Value {
				r.Issues = append(r.Issues, NovelIssue{Code: "SAME_TIME_CONTRADICTION", Category: "FACT", A: prev.cite, B: cite})
			}
			if prev, ok := last[f.Entity]; ok && f.Tick < prev.fact.Tick {
				r.Issues = append(r.Issues, NovelIssue{Code: "CHRONOLOGY_REVERSAL", Category: "TIMELINE", A: prev.cite, B: cite})
			}
			seen[at] = observed{f, cite}
			last[f.Entity] = observed{f, cite}
		}
	}
	r.CED = "0.0000"
	if r.Words > 0 {
		r.CED = strconv.FormatFloat(float64(len(r.Issues))*10000/float64(r.Words), 'f', 4, 64)
	}
	return r
}
