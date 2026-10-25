package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestChannelSearchCardsPersistImmutableCitationsAndReplayWithoutRedisclosure(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	configureTestConnector(t, m, db, ch.ID, "SEARCH")
	ctx := conversationContext(t, 2)
	calls := 0
	client := &http.Client{Transport: connectorTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		require.NotEmpty(t, req.Header.Get("Idempotency-Key"))
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"message_id":"999","sources":[{"title":"Source","url":"https://example.com/source","summary":"original"}],"conclusions":[{"text":"Cited finding","sources":[1]}]}`))}, nil
	})}
	first, err := m.searchChannelRequest(ctx, ch.ID, "approved query", true, client, "same-search")
	require.NoError(t, err)
	require.Positive(t, first.MessageID)
	require.NotEqual(t, guid.ID(999), first.MessageID)
	replayed, err := m.searchChannelRequest(ctx, ch.ID, "approved query", true, client, "same-search")
	require.NoError(t, err)
	require.Equal(t, first, replayed)
	require.Equal(t, 1, calls, "successful retry must not disclose the query again")
	_, err = m.searchChannelRequest(ctx, ch.ID, "different query", true, client, "same-search")
	require.ErrorIs(t, err, idempotency.ErrKeyReused)
	require.Equal(t, 1, calls)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, err := m.listMessagesOn(ctx, tx, 1, 3, ch.ID, MessageFilter{Limit: 50, Activity: "all"})
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		require.Equal(t, "SEARCH_CARD", page.Items[0].Kind)
		require.Equal(t, "INTERNAL", page.Items[0].Visibility)
		require.Equal(t, first.MessageID, page.Items[0].ID)
		require.Equal(t, "approved query", page.Items[0].Payload["query"])
		for _, retract := range []bool{false, true} {
			_, err = m.mutateMessageOn(ctx, tx, 1, first.MessageID, 1, EditMessageInput{Body: "altered"}, retract)
			require.ErrorIs(t, err, ErrMessageNotEditable)
		}
		return nil
	}))
	_, err = m.searchChannelRequest(conversationContext(t, 4), ch.ID, "approved query", true, client, "same-search")
	require.Error(t, err)
	require.Equal(t, 1, calls)
}

func TestMediaProviderRejectsCorruptSecretsRequestsAndResponses(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	c := configureTestConnector(t, m, db, ch.ID, "SEARCH")
	var row model.ChannelConnector
	require.NoError(t, query.BindConnectors(db, m.names).Where(&model.ChannelConnector{ID: c.ID}).First(&row).Error)
	for _, scenario := range []string{"key", "ciphertext", "json_input", "negative_id", "request_url", "network", "oversize", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			config := m.config
			defer func() { m.config = config }()
			provider, input, id := row, any(map[string]string{"query": "approved"}), guid.ID(12)
			client := &http.Client{Transport: connectorTransport(func(*http.Request) (*http.Response, error) {
				if scenario == "network" {
					return nil, errors.New("network private error")
				}
				body := "not json"
				if scenario == "oversize" {
					body = strings.Repeat("x", 128*1024+1)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			switch scenario {
			case "key":
				m.config.ConnectorEncryptionKey = ""
			case "ciphertext":
				provider.SecretSealed = "corrupt"
			case "json_input":
				input = make(chan int)
			case "negative_id":
				id = -1
			case "request_url":
				provider.Endpoint = "://bad"
			}
			var out SearchCard
			require.Error(t, m.callMediaProviderRequest(conversationContext(t, 2), provider, input, &out, client, id))
			require.Zero(t, out.MessageID)
			require.Empty(t, out.Sources)
		})
	}
}

func TestTranscriptionFailuresDoNotPublishUnverifiedText(t *testing.T) {
	for _, scenario := range []string{"missing_open", "open_error", "not_audio", "oversize", "empty_text", "provider_error", "audit_request", "audit_complete", "source_missing"} {
		t.Run(scenario, func(t *testing.T) {
			m, db := conversationHarness(t)
			ch := createConversation(t, m, db, "INTERNAL")
			configureTestConnector(t, m, db, ch.ID, "TRANSCRIPTION")
			message := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "audio", Visibility: "CHANNEL"})
			a := model.MessageAttachment{ID: 71, TenantID: 1, ChannelID: ch.ID, MessageID: message.ID, FileID: 12, VersionID: 13}
			require.NoError(t, query.BindAttachments(db, m.names).WithContext(t.Context()).Create(&a))
			fault := errors.New("media dependency failure")
			m.fileResources.OpenOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (files.PinnedResourceRead, error) {
				file := files.PinnedResourceRead{PinnedResource: files.PinnedResource{MIME: "audio/webm", Size: 5}, Reader: io.NopCloser(strings.NewReader("audio"))}
				switch scenario {
				case "open_error":
					return file, fault
				case "not_audio":
					file.MIME = "text/plain"
				case "oversize":
					file.Size = 25*1024*1024 + 1
				}
				return file, nil
			}
			if scenario == "missing_open" {
				m.fileResources.OpenOn = nil
			}
			if scenario == "source_missing" {
				message.ID = 999
			}
			original := m.audit
			m.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
				if (scenario == "audit_request" && event.EventType == "collab.media_requested") || (scenario == "audit_complete" && event.EventType == "collab.media_completed") {
					return fault
				}
				return original(ctx, tx, event)
			}
			calls := 0
			client := &http.Client{Transport: connectorTransport(func(*http.Request) (*http.Response, error) {
				calls++
				status, body := 200, `{"text":""}`
				if scenario == "provider_error" {
					status = 502
				}
				if scenario == "audit_complete" {
					body = `{"text":"actual text"}`
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			out, err := m.transcribeAttachment(conversationContext(t, 2), ch.ID, message.ID, a.ID, client)
			require.Error(t, err)
			require.Empty(t, out.Text)
			if scenario != "empty_text" && scenario != "provider_error" && scenario != "audit_complete" {
				require.Zero(t, calls)
			}
			var n int64
			require.NoError(t, db.Table("audit_probe").Where("event_type = ?", "collab.media_completed").Count(&n).Error)
			require.Zero(t, n)
		})
	}
}

func TestChannelSearchCardAuditFailureRollsBackAndRetriesSameProviderIdentity(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	configureTestConnector(t, m, db, ch.ID, "SEARCH")
	original := m.audit
	fault := errors.New("audit unavailable")
	var providerKeys []string
	client := &http.Client{Transport: connectorTransport(func(req *http.Request) (*http.Response, error) {
		providerKeys = append(providerKeys, req.Header.Get("Idempotency-Key"))
		if len(providerKeys) == 1 {
			m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"sources":[{"title":"Source","url":"https://example.com/source","summary":"original"}],"conclusions":[]}`))}, nil
	})}
	_, err := m.searchChannelRequest(conversationContext(t, 2), ch.ID, "query", true, client, "retry-search")
	require.ErrorIs(t, err, fault)
	m.audit = original
	var cards int64
	require.NoError(t, query.BindSystemCards(db, m.names).Count(&cards).Error)
	require.Zero(t, cards)
	out, err := m.searchChannelRequest(conversationContext(t, 2), ch.ID, "query", true, client, "retry-search")
	require.NoError(t, err)
	require.Positive(t, out.MessageID)
	require.Len(t, providerKeys, 2)
	require.Equal(t, providerKeys[0], providerKeys[1])
}

func TestChannelMediaSearchRequiresHumanApprovalConfiguredGrantAndCitations(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	ctx := conversationContext(t, 2)
	calls := 0
	client := &http.Client{Transport: connectorTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		data, e := io.ReadAll(req.Body)
		require.NoError(t, e)
		require.JSONEq(t, `{"query":"public standards"}`, string(data))
		require.NotEmpty(t, req.Header.Get("X-Channel-Signature"))
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"sources":[{"title":"Standard","url":"https://example.com/search?q=csv","summary":"actual source"}],"conclusions":[{"text":"cited","sources":[1]}]}`))}, nil
	})}
	_, e := m.searchChannel(ctx, ch.ID, "public standards", false, client)
	require.ErrorIs(t, e, ErrInvalidArgument)
	require.Zero(t, calls)
	_, e = m.searchChannel(ctx, ch.ID, "public standards", true, client)
	require.Error(t, e)
	require.Zero(t, calls)
	configureTestConnector(t, m, db, ch.ID, "SEARCH")
	out, e := m.searchChannel(ctx, ch.ID, "public standards", true, client)
	require.NoError(t, e)
	require.Equal(t, "public standards", out.Query)
	require.Equal(t, []int{1}, out.Conclusions[0].Sources)
	require.Equal(t, 1, calls)
	var audits int64
	require.NoError(t, db.Table("audit_probe").Where("event_type IN ?", []string{"collab.media_requested", "collab.media_completed"}).Count(&audits).Error)
	require.EqualValues(t, 2, audits)
	_, e = m.searchChannel(conversationContext(t, 4), ch.ID, "public standards", true, client)
	require.Error(t, e)
	require.Equal(t, 1, calls)
	for _, card := range []SearchCard{{Sources: []SearchSource{{Title: "bad", URL: "javascript:alert(1)"}}}, {Sources: []SearchSource{{Title: "good", URL: "https://example.com"}}, Conclusions: []SearchConclusion{{Text: "uncited"}}}, {Sources: []SearchSource{{Title: "good", URL: "https://example.com"}}, Conclusions: []SearchConclusion{{Text: "wrong", Sources: []int{2}}}}} {
		require.ErrorIs(t, validateSearchCard(card), ErrUnavailable)
	}
}
func TestChannelMediaTranscriptionReadsOnlyPinnedAuthorizedAudio(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	configureTestConnector(t, m, db, ch.ID, "TRANSCRIPTION")
	message := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "voice", Visibility: "CHANNEL"})
	attachment := model.MessageAttachment{ID: 71, TenantID: 1, ChannelID: ch.ID, MessageID: message.ID, FileID: 12, VersionID: 13}
	require.NoError(t, query.BindAttachments(db, m.names).WithContext(t.Context()).Create(&attachment))
	opens := 0
	m.fileResources.OpenOn = func(_ context.Context, _ *gorm.DB, id, msg guid.ID) (files.PinnedResourceRead, error) {
		opens++
		require.Equal(t, attachment.ID, id)
		require.Equal(t, message.ID, msg)
		return files.PinnedResourceRead{PinnedResource: files.PinnedResource{MIME: "audio/webm", Size: 5}, Reader: io.NopCloser(strings.NewReader("audio"))}, nil
	}
	client := &http.Client{Transport: connectorTransport(func(req *http.Request) (*http.Response, error) {
		var payload struct {
			MIME  string `json:"mime"`
			Audio string `json:"audio_base64"`
		}
		require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
		require.Equal(t, "audio/webm", payload.MIME)
		require.Equal(t, "YXVkaW8=", payload.Audio)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"text":"actual transcription"}`))}, nil
	})}
	out, e := m.transcribeAttachment(conversationContext(t, 2), ch.ID, message.ID, attachment.ID, client)
	require.NoError(t, e)
	require.Equal(t, "actual transcription", out.Text)
	require.Equal(t, 1, opens)
	_, e = m.transcribeAttachment(conversationContext(t, 2), ch.ID, message.ID, 999, client)
	require.Error(t, e)
	require.Equal(t, 1, opens)
}
func TestChannelMediaRejectsProviderErrorsAndRevocation(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	configureTestConnector(t, m, db, ch.ID, "SEARCH")
	client := &http.Client{Transport: connectorTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("upstream secret error"))}, nil
	})}
	_, e := m.searchChannel(conversationContext(t, 2), ch.ID, "query", true, client)
	require.ErrorIs(t, e, ErrUnavailable)
	client.Transport = connectorTransport(func(*http.Request) (*http.Response, error) {
		require.NoError(t, query.BindConnectors(db, m.names).Where(&model.ChannelConnector{ChannelID: ch.ID, Kind: "SEARCH"}).Update("enabled", false).Error)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"sources":[{"title":"source","url":"https://example.com"}],"conclusions":[]}`))}, nil
	})
	_, e = m.searchChannel(conversationContext(t, 2), ch.ID, "query", true, client)
	require.Error(t, e)
}

// The wrapper changes configuration in the committed approval transaction,
// deterministically exercising the gap before the outbound call.
type searchReceiptStore struct {
	idempotency.Store
	after func(*gorm.DB) error
}

func (s searchReceiptStore) Do(ctx context.Context, tx *gorm.DB, key idempotency.Key, fn func(*gorm.DB) (idempotency.Result, error)) (idempotency.Result, bool, error) {
	result, replayed, err := s.Store.Do(ctx, tx, key, fn)
	if err == nil && s.after != nil {
		err = s.after(tx)
	}
	return result, replayed, err
}

func TestChannelSearchRechecksApprovedConfigBeforeDisclosure(t *testing.T) {
	for _, change := range []string{"disabled", "rotated"} {
		t.Run(change, func(t *testing.T) {
			m, db := conversationHarness(t)
			ch := createConversation(t, m, db, "INTERNAL")
			configureTestConnector(t, m, db, ch.ID, "SEARCH")
			m.replay = searchReceiptStore{Store: m.replay, after: func(tx *gorm.DB) error {
				updates := map[string]any{"enabled": false}
				if change == "rotated" {
					updates = map[string]any{"version": 2, "endpoint": "https://replacement.example/search"}
				}
				return query.BindConnectors(tx, m.names).Where(&model.ChannelConnector{TenantID: 1, ChannelID: ch.ID, Kind: "SEARCH"}).Updates(updates).Error
			}}
			calls := 0
			client := &http.Client{Transport: connectorTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("must not disclose")
			})}
			_, err := m.searchChannelRequest(conversationContext(t, 2), ch.ID, "confidential query", true, client, "config-fence")
			require.Error(t, err)
			require.Zero(t, calls)
			var cards int64
			require.NoError(t, query.BindSystemCards(db, m.names).Count(&cards).Error)
			require.Zero(t, cards)
		})
	}
}

func TestChannelSearchReplaySurvivesConfigChangesAndNormalizesEmptyConclusions(t *testing.T) {
	for _, response := range []string{
		`{"sources":[{"title":"Source","url":"https://example.com","summary":"actual"}]}`,
		`{"sources":[{"title":"Source","url":"https://example.com","summary":"actual"}],"conclusions":null}`,
		`{"sources":[{"title":"Source","url":"https://example.com","summary":"actual"}],"conclusions":[]}`,
	} {
		t.Run(response, func(t *testing.T) {
			m, db := conversationHarness(t)
			ch := createConversation(t, m, db, "INTERNAL")
			configureTestConnector(t, m, db, ch.ID, "SEARCH")
			calls := 0
			client := &http.Client{Transport: connectorTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			ctx := conversationContext(t, 2)
			first, err := m.searchChannelRequest(ctx, ch.ID, "query", true, client, "completed")
			require.NoError(t, err)
			require.NotNil(t, first.Conclusions)
			require.Empty(t, first.Conclusions)
			for _, enabled := range []bool{true, false} {
				require.NoError(t, query.BindConnectors(db, m.names).Where(&model.ChannelConnector{TenantID: 1, ChannelID: ch.ID, Kind: "SEARCH"}).Updates(map[string]any{"enabled": enabled, "version": 2}).Error)
				replayed, err := m.searchChannelRequest(ctx, ch.ID, "query", true, client, "completed")
				require.NoError(t, err)
				require.Equal(t, first, replayed)
				require.Equal(t, 1, calls)
			}
			var saved model.SystemCard
			require.NoError(t, query.BindSystemCards(db, m.names).First(&saved).Error)
			require.Contains(t, saved.Payload, `"conclusions":[]`)
			_, err = m.searchChannelRequest(conversationContext(t, 4), ch.ID, "query", true, client, "completed")
			require.Error(t, err)
			require.Equal(t, 1, calls)
		})
	}
}
