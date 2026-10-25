package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Media providers use the same signed HTTPS JSON contract as channel hooks.
// They are absent until an owner explicitly configures a channel capability.
type TranscriptionResult struct {
	Text string `json:"text" maxLength:"8000"`
}
type SearchSource struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Summary string `json:"summary"`
}
type SearchConclusion struct {
	Text    string `json:"text"`
	Sources []int  `json:"sources"`
}
type SearchCard struct {
	MessageID   guid.ID            `json:"message_id,omitempty"`
	Query       string             `json:"query"`
	Sources     []SearchSource     `json:"sources"`
	Conclusions []SearchConclusion `json:"conclusions"`
}

func (m *Module) mediaProvider(ctx context.Context, channel guid.ID, kind string) (model.ChannelConnector, guid.ID, error) {
	var out model.ChannelConnector
	project, e := m.projectForChannel(ctx, channel)
	if e != nil {
		return out, 0, e
	}
	db, e := m.database()
	if e != nil {
		return out, 0, e
	}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.connectorOwnerOn(ctx, tx, authn.TenantIDFromContext(ctx), project, channel); e != nil {
			return e
		}
		return query.BindConnectors(tx, m.names).Where(&model.ChannelConnector{TenantID: authn.TenantIDFromContext(ctx), ChannelID: channel, Kind: kind, Enabled: true}).First(&out).Error
	})
	return out, project, e
}
func (m *Module) callMediaProvider(ctx context.Context, c model.ChannelConnector, input, output any, client *http.Client) error {
	return m.callMediaProviderRequest(ctx, c, input, output, client, 0)
}

func (m *Module) callMediaProviderRequest(ctx context.Context, c model.ChannelConnector, input, output any, client *http.Client, requestID guid.ID) error {
	ring, e := m.connectorKeys()
	if e != nil {
		return e
	}
	secret, e := ring.Open("channel-connector", connectorAAD(c.TenantID, c.ChannelID, c.ID), c.SecretSealed)
	if e != nil {
		return ErrUnavailable
	}
	body, e := json.Marshal(input)
	if e != nil {
		return e
	}
	if requestID == 0 {
		id, err := m.ids.Next()
		if err != nil {
			return err
		}
		requestID = guid.ID(id)
	}
	if requestID <= 0 {
		return ErrUnavailable
	}
	req, e := connectorRequest(ctx, c.Endpoint, string(secret), requestID, body, m.now())
	if e != nil {
		return e
	}
	response, e := client.Do(req)
	if e != nil {
		return ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrUnavailable
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, 128*1024+1))
	if e != nil || len(data) > 128*1024 {
		return ErrUnavailable
	}
	if e = json.Unmarshal(data, output); e != nil {
		return ErrUnavailable
	}
	return nil
}
func (m *Module) recordMediaUse(ctx context.Context, channel, project guid.ID, version int, kind, event string) error {
	db, e := m.database()
	if e != nil {
		return e
	}
	tenant := authn.TenantIDFromContext(ctx)
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.connectorOwnerOn(ctx, tx, tenant, project, channel); e != nil {
			return e
		}
		var current model.ChannelConnector
		if e := query.BindConnectors(tx, m.names).Where(&model.ChannelConnector{TenantID: tenant, ChannelID: channel, Kind: kind, Enabled: true, Version: version}).First(&current).Error; e != nil {
			return e
		}
		// Content, confidential query text, audio and provider credentials never enter
		// the audit/outbox. The action and current Human authority remain auditable.
		return m.channelEvent(ctx, tx, tenant, project, channel, event, "CHANNEL_OWNER")
	})
}
func (m *Module) transcribeAttachment(ctx context.Context, channel, message, attachment guid.ID, client *http.Client) (TranscriptionResult, error) {
	var out TranscriptionResult
	c, project, e := m.mediaProvider(ctx, channel, "TRANSCRIPTION")
	if e != nil {
		return out, e
	}
	db, e := m.database()
	if e != nil {
		return out, e
	}
	var file files.PinnedResourceRead
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.conversionSourceOn(ctx, tx, c.TenantID, project, channel, message); e != nil {
			return e
		}
		q := query.BindAttachments(tx, m.names)
		if _, e := q.WithContext(ctx).Where(q.ID.Eq(attachment), q.TenantID.Eq(c.TenantID), q.ChannelID.Eq(channel), q.MessageID.Eq(message)).First(); e != nil {
			return e
		}
		if m.fileResources.OpenOn == nil {
			return ErrUnavailable
		}
		var e error
		file, e = m.fileResources.OpenOn(fileContext(ctx, project), tx, attachment, message)
		return e
	})
	if e != nil {
		if file.Reader != nil {
			_ = file.Reader.Close()
		}
		return out, e
	}
	defer func() { _ = file.Reader.Close() }()
	if !strings.HasPrefix(file.MIME, "audio/") || file.Size > 25*1024*1024 {
		return out, ErrInvalidArgument
	}
	audio, e := io.ReadAll(io.LimitReader(file.Reader, 25*1024*1024+1))
	if e != nil || len(audio) > 25*1024*1024 {
		return out, ErrInvalidArgument
	}
	if e = m.recordMediaUse(ctx, channel, project, c.Version, "TRANSCRIPTION", "collab.media_requested"); e != nil {
		return out, e
	}
	e = m.callMediaProvider(ctx, c, struct {
		MIME  string `json:"mime"`
		Audio string `json:"audio_base64"`
	}{file.MIME, base64.StdEncoding.EncodeToString(audio)}, &out, client)
	if e != nil {
		return out, e
	}
	if !utf8.ValidString(out.Text) || strings.TrimSpace(out.Text) == "" || utf8.RuneCountInString(out.Text) > 8000 {
		return TranscriptionResult{}, ErrUnavailable
	}
	if e = m.recordMediaUse(ctx, channel, project, c.Version, "TRANSCRIPTION", "collab.media_completed"); e != nil {
		return TranscriptionResult{}, e
	}
	return out, nil
}
func (m *Module) searchChannel(ctx context.Context, channel guid.ID, queryText string, approved bool, client *http.Client) (SearchCard, error) {
	return m.searchChannelRequest(ctx, channel, queryText, approved, client, "")
}

// The initial request receipt commits before network I/O. A retry reuses its
// provider key and immutable message; external calls never hold database locks.
func (m *Module) searchChannelRequest(ctx context.Context, channel guid.ID, queryText string, approved bool, client *http.Client, key string) (SearchCard, error) {
	var out SearchCard
	if !approved || strings.TrimSpace(queryText) == "" || !utf8.ValidString(queryText) || utf8.RuneCountInString(queryText) > 200 {
		return out, ErrInvalidArgument
	}
	project, e := m.projectForChannel(ctx, channel)
	if e != nil {
		return out, e
	}
	db, e := m.database()
	if e != nil {
		return out, e
	}
	tenant := authn.TenantIDFromContext(ctx)
	var c model.ChannelConnector
	var requestID guid.ID
	var approvedConfig struct {
		ID      guid.ID `json:"connector_id"`
		Version int     `json:"version"`
	}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		begin := func(tx *gorm.DB) (idempotency.Result, error) {
			if err := m.connectorOwnerOn(ctx, tx, tenant, project, channel); err != nil {
				return idempotency.Result{}, err
			}
			if err := query.BindConnectors(tx, m.names).Where(&model.ChannelConnector{TenantID: tenant, ChannelID: channel, Kind: "SEARCH", Enabled: true}).First(&c).Error; err != nil {
				return idempotency.Result{}, err
			}
			id, err := m.ids.Next()
			if err != nil || id <= 0 {
				return idempotency.Result{}, ErrUnavailable
			}
			if err = m.channelEvent(ctx, tx, tenant, project, channel, "collab.media_requested", "CHANNEL_OWNER"); err != nil {
				return idempotency.Result{}, err
			}
			approvedConfig.ID, approvedConfig.Version = c.ID, c.Version
			body, err := json.Marshal(approvedConfig)
			return idempotency.Result{StatusCode: http.StatusAccepted, Body: body, ResourceKind: "channel_search", ResourceID: guid.ID(id)}, err
		}
		if key == "" {
			result, err := begin(tx)
			requestID = result.ResourceID
			return err
		}
		if m.replay == nil {
			return ErrUnavailable
		}
		body, _ := json.Marshal(struct {
			Channel guid.ID `json:"channel"`
			Query   string  `json:"query"`
		}{channel, queryText})
		// Resolve idempotency before business reads establish an RR snapshot.
		// The stable request hash excludes mutable provider configuration; its
		// exact approved identity/version is pinned in the committed receipt.
		result, _, err := m.replay.Do(ctx, tx, idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "channel.media.search", Value: key, RequestHash: sha256.Sum256(body)}, begin)
		if err != nil {
			return err
		}
		if err = m.connectorOwnerOn(ctx, tx, tenant, project, channel); err != nil {
			return err
		}
		requestID = result.ResourceID
		if json.Unmarshal(result.Body, &approvedConfig) != nil || approvedConfig.ID <= 0 || approvedConfig.Version < 1 || requestID <= 0 {
			return ErrUnavailable
		}
		return nil
	})
	if e != nil {
		return out, e
	}
	// A completed retry reads only the persisted provider response. It does not
	// disclose the query again or substitute a newer response under the same key.
	found := false
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.connectorOwnerOn(ctx, tx, tenant, project, channel); e != nil {
			return e
		}
		var saved model.SystemCard
		err := query.BindSystemCards(tx, m.names).Where(&model.SystemCard{TenantID: tenant, ChannelID: channel, SourceID: requestID}).First(&saved).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// A failed attempt may only redisclose to the same approved provider.
			return query.BindConnectors(tx, m.names).Where(&model.ChannelConnector{ID: approvedConfig.ID, TenantID: tenant, ChannelID: channel, Kind: "SEARCH", Enabled: true, Version: approvedConfig.Version}).First(&c).Error
		}
		if err != nil {
			return err
		}
		if saved.Kind != "SEARCH_CARD" || json.Unmarshal([]byte(saved.Payload), &out) != nil {
			return ErrUnavailable
		}
		out.MessageID, found = saved.MessageID, true
		return nil
	})
	if e != nil || found {
		return out, e
	}
	e = m.callMediaProviderRequest(ctx, c, struct {
		Query string `json:"query"`
	}{queryText}, &out, client, requestID)
	if e != nil {
		return SearchCard{}, e
	}
	out.Query, out.MessageID = queryText, 0 // Provider data cannot choose storage identity.
	if out.Conclusions == nil {
		out.Conclusions = []SearchConclusion{}
	}
	if e = validateSearchCard(out); e != nil {
		return SearchCard{}, e
	}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.connectorOwnerOn(ctx, tx, tenant, project, channel); e != nil {
			return e
		}
		var current model.ChannelConnector
		if e := query.BindConnectors(tx, m.names).Where(&model.ChannelConnector{ID: c.ID, TenantID: tenant, ChannelID: channel, Kind: "SEARCH", Enabled: true, Version: c.Version}).First(&current).Error; e != nil {
			return e
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			return err
		}
		var payload map[string]any
		if err = json.Unmarshal(encoded, &payload); err != nil {
			return err
		}
		message, err := m.postSystemCardOn(ctx, tx, SystemCardInput{TenantID: tenant, ProjectID: project, ChannelID: channel, SourceID: requestID, Kind: "SEARCH_CARD", Body: queryText, Payload: payload})
		if err != nil {
			return err
		}
		// Concurrent retries return the first committed card's immutable payload.
		encoded, err = json.Marshal(message.Payload)
		if err != nil || json.Unmarshal(encoded, &out) != nil {
			return ErrUnavailable
		}
		out.MessageID = message.ID
		return m.channelEvent(ctx, tx, tenant, project, channel, "collab.media_completed", "CHANNEL_OWNER")
	})
	if e != nil {
		return SearchCard{}, e
	}
	return out, nil
}
func validateSearchCard(card SearchCard) error {
	if len(card.Sources) < 1 || len(card.Sources) > 5 || len(card.Conclusions) > 10 {
		return ErrUnavailable
	}
	for _, s := range card.Sources {
		u, e := url.Parse(s.URL)
		if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Hostname() == "" || strings.TrimSpace(s.Title) == "" || len(s.Title) > 1000 || len(s.Summary) > 8000 {
			return ErrUnavailable
		}
	}
	for _, c := range card.Conclusions {
		if strings.TrimSpace(c.Text) == "" || len(c.Text) > 8000 || len(c.Sources) == 0 || len(c.Sources) > 5 {
			return ErrUnavailable
		}
		for _, source := range c.Sources {
			if source < 1 || source > len(card.Sources) {
				return ErrUnavailable
			}
		}
	}
	return nil
}
