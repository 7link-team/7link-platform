package webhook

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/webhook/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/httpx"
	"7link.com/platform/pkg/extension/keyringx"
	"7link.com/platform/pkg/extension/secretx"
	"7link.com/platform/pkg/module"
)

// Config controls following the chain and sending what it finds. The encryption
// key protects subscription signing secrets; resolve it from a secret manager via
// encryption_key_file in production.
type Config struct {
	module.Config
	EncryptionKey     string `mapstructure:"encryption_key" description:"base64 32-byte key protecting subscription signing secrets; prefer encryption_key_file" default:"" secret:"true"`
	EncryptionKeyFile string `mapstructure:"encryption_key_file" description:"file containing the base64 32-byte webhook encryption key" default:""`
	// Retired keys still open stored secrets and never seal anything, so one
	// rotation does not have to finish before the next can start.
	PreviousEncryptionKeys     []string `mapstructure:"previous_encryption_keys" description:"retired base64 32-byte webhook keys that still open stored secrets" secret:"true"`
	PreviousEncryptionKeyFiles []string `mapstructure:"previous_encryption_key_files" description:"files containing retired base64 32-byte webhook keys"`

	// A subscription's URL is tenant-controlled, so these two are the only way to
	// widen what a delivery reaches. They are per-module on purpose: permitting an
	// internal receiver here should not also permit outbound SCIM to that range.
	OutboundAllowPlaintext bool     `mapstructure:"outbound_allow_plaintext" description:"permit http:// receivers; leave false to require https" default:"false"`
	OutboundAllowNetworks  []string `mapstructure:"outbound_allow_networks" description:"CIDR blocks a receiver may resolve to despite being private or reserved"`

	HTTPTimeout   time.Duration `mapstructure:"http_timeout" description:"receiver request timeout" default:"10s"`
	PollInterval  time.Duration `mapstructure:"poll_interval" description:"how often the chain is followed and due deliveries are sent" default:"5s"`
	RetryBase     time.Duration `mapstructure:"retry_base" description:"first retry delay; each further attempt doubles it" default:"5s"`
	MaxAttempts   int           `mapstructure:"max_attempts" description:"attempts before a delivery is abandoned" default:"8"`
	DeliveryBatch int           `mapstructure:"delivery_batch" description:"deliveries attempted in one pass" default:"50"`
	FanOutBatch   int           `mapstructure:"fan_out_batch" description:"events read from one tenant's chain in one pass" default:"200"`
	// FanOutSubscriptions bounds how many subscriptions one pass catches up, so a
	// deployment with many of them makes progress on all of them in turn rather
	// than holding one statement open across every tenant.
	FanOutSubscriptions int `mapstructure:"fan_out_subscriptions" description:"subscriptions caught up in one pass" default:"200"`
}

const webhookKeyDomain = "webhook:secret:"

type Service struct {
	repo       *Repository
	readStream audit.EventStreamReader
	keys       *keyringx.Keyring
	client     *http.Client
	outbound   httpx.Policy
	userAgent  string
	cfg        Config
	nextID     func() (guid.ID, error)
	now        func() time.Time

	delivered atomic.Int64
	failures  atomic.Int64
}

func ResolveKeyring(cfg Config) (*keyringx.Keyring, error) {
	active, err := secretx.Resolve("webhook.encryption_key", cfg.EncryptionKey, cfg.EncryptionKeyFile, 4096)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(active) == "" {
		return nil, errors.New("webhook encryption key is required when webhook is enabled")
	}
	previous, err := secretx.ResolveList(
		"webhook.previous_encryption_keys", cfg.PreviousEncryptionKeys, cfg.PreviousEncryptionKeyFiles, 4096,
	)
	if err != nil {
		return nil, err
	}
	keys, err := keyringx.New(webhookKeyDomain, active, previous...)
	if err != nil {
		return nil, fmt.Errorf("webhook encryption key: %w", err)
	}
	return keys, nil
}

func ResolveOutboundPolicy(cfg Config) (httpx.Policy, error) {
	policy, err := httpx.ParsePolicy(cfg.OutboundAllowPlaintext, cfg.OutboundAllowNetworks)
	if err != nil {
		return httpx.Policy{}, fmt.Errorf("webhook outbound policy: %w", err)
	}
	return policy, nil
}

func NewService(
	runtime *appenv.Runtime, reader audit.EventStreamReader, cfg Config,
	keys *keyringx.Keyring, ids guid.Source, outbound httpx.Policy,
) *Service {
	if runtime == nil || reader == nil || ids == nil {
		panic("webhook service requires a runtime, an audit event stream reader, and an id generator")
	}
	cfg = cfg.withDefaults()
	return &Service{
		repo: NewRepository(runtime), readStream: reader, keys: keys,
		client:    httpx.NewClient(cfg.HTTPTimeout, outbound),
		outbound:  outbound,
		userAgent: outboundUserAgent(runtime),
		cfg:       cfg,
		nextID: func() (guid.ID, error) {
			id, err := ids.Next()
			return guid.ID(id), err
		},
		now: func() time.Time { return time.Now().UTC() },
	}
}

// withDefaults fills what a zero config leaves out, so a deployment that sets
// nothing still polls, retries and gives up rather than spinning.
func (c Config) withDefaults() Config {
	if c.HTTPTimeout <= 0 {
		c.HTTPTimeout = 10 * time.Second
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 5 * time.Second
	}
	if c.RetryBase <= 0 {
		c.RetryBase = 5 * time.Second
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 8
	}
	if c.DeliveryBatch <= 0 {
		c.DeliveryBatch = 50
	}
	if c.FanOutBatch <= 0 {
		c.FanOutBatch = 200
	}
	if c.FanOutSubscriptions <= 0 {
		c.FanOutSubscriptions = 200
	}
	return c
}

// outboundUserAgent names this deployment to a receiver. It comes from the
// runtime identity, so one build serves any deployment and no product name is
// written into the source.
func outboundUserAgent(runtime *appenv.Runtime) string {
	if runtime == nil {
		return ""
	}
	identity := runtime.Identity()
	name := strings.TrimSpace(identity.Name())
	if name == "" {
		return ""
	}
	if version := strings.TrimSpace(identity.Version()); version != "" {
		return name + "/" + version
	}
	return name
}

func (s *Service) openSecret(row subscriptionRow) ([]byte, error) {
	if row.SecretCiphertext == "" {
		return nil, nil
	}
	if s.keys == nil {
		return nil, errors.New("webhook signing secrets are unavailable")
	}
	secret, err := s.keys.Open("webhook-secret", []byte(row.ID.String()), row.SecretCiphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt webhook signing secret: %w", err)
	}
	return secret, nil
}

func (s *Service) sealSecret(id guid.ID, secret string) (string, error) {
	if secret == "" {
		return "", nil
	}
	if s.keys == nil {
		return "", errors.New("webhook signing secrets are unavailable")
	}
	return s.keys.Seal("webhook-secret", []byte(id.String()), []byte(secret))
}

// CreateSubscription registers a receiver. A new subscription starts at the
// tenant's current head rather than at zero: registering an endpoint is a request
// to hear what happens next, not to be sent the entire history.
func (s *Service) CreateSubscription(ctx context.Context, tenantID guid.ID, input SubscriptionInput) (Subscription, error) {
	row, secret, err := s.normalize(tenantID, input)
	if err != nil {
		return Subscription{}, err
	}
	if taken, err := s.repo.nameTaken(ctx, tenantID, row.Name, 0); err != nil {
		return Subscription{}, err
	} else if taken {
		return Subscription{}, ErrSubscriptionNameTaken
	}
	id, err := s.nextID()
	if err != nil {
		return Subscription{}, err
	}
	row.ID = id
	if row.SecretCiphertext, err = s.sealSecret(id, secret); err != nil {
		return Subscription{}, err
	}
	head, err := s.tenantHead(ctx, tenantID)
	if err != nil {
		return Subscription{}, err
	}
	row.AfterSequence = head
	now := s.now().UTC()
	row.CreatedAt, row.UpdatedAt = now, now
	if err := s.repo.insertSubscription(ctx, &row); err != nil {
		return Subscription{}, err
	}
	return projectSubscription(row), nil
}

// tenantHead is the sequence a new subscription starts after. Reading one event
// past the end is how the chain's head is found without a count.
func (s *Service) tenantHead(ctx context.Context, tenantID guid.ID) (int64, error) {
	head := int64(0)
	for {
		events, err := s.readStream(ctx, tenantID, head, 500)
		if err != nil {
			return 0, err
		}
		if len(events) == 0 {
			return head, nil
		}
		head = events[len(events)-1].Sequence
	}
}

func (s *Service) UpdateSubscription(ctx context.Context, tenantID, id guid.ID, input SubscriptionInput) (Subscription, error) {
	existing, err := s.repo.getSubscription(ctx, tenantID, id)
	if err != nil {
		return Subscription{}, err
	}
	row, secret, err := s.normalize(tenantID, input)
	if err != nil {
		return Subscription{}, err
	}
	if taken, err := s.repo.nameTaken(ctx, tenantID, row.Name, id); err != nil {
		return Subscription{}, err
	} else if taken {
		return Subscription{}, ErrSubscriptionNameTaken
	}
	row.ID, row.AfterSequence, row.CreatedAt = id, existing.AfterSequence, existing.CreatedAt
	// An empty secret keeps the stored one, so a caller editing the URL does not
	// have to resend a secret it was never shown.
	row.SecretCiphertext = existing.SecretCiphertext
	if secret != "" {
		if row.SecretCiphertext, err = s.sealSecret(id, secret); err != nil {
			return Subscription{}, err
		}
	}
	row.UpdatedAt = s.now().UTC()
	if err := s.repo.updateSubscription(ctx, &row); err != nil {
		return Subscription{}, err
	}
	return projectSubscription(row), nil
}

func (s *Service) DeleteSubscription(ctx context.Context, tenantID, id guid.ID) error {
	return s.repo.deleteSubscription(ctx, tenantID, id)
}

func (s *Service) GetSubscription(ctx context.Context, tenantID, id guid.ID) (Subscription, error) {
	row, err := s.repo.getSubscription(ctx, tenantID, id)
	if err != nil {
		return Subscription{}, err
	}
	return projectSubscription(row), nil
}

func (s *Service) ListSubscriptions(ctx context.Context, tenantID guid.ID, offset, limit int) ([]Subscription, int64, error) {
	rows, total, err := s.repo.listSubscriptions(ctx, tenantID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	items := make([]Subscription, 0, len(rows))
	for _, row := range rows {
		items = append(items, projectSubscription(row))
	}
	return items, total, nil
}

// Delivery is one attempt history entry a tenant can read back, so an operator
// can see why a receiver is not getting events.
type Delivery struct {
	ID             guid.ID    `json:"id"`
	SubscriptionID guid.ID    `json:"subscription_id"`
	EventID        guid.ID    `json:"event_id"`
	EventSequence  int64      `json:"event_sequence"`
	EventType      string     `json:"event_type"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	AvailableAt    time.Time  `json:"available_at"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (s *Service) ListDeliveries(ctx context.Context, tenantID, subscriptionID guid.ID, offset, limit int) ([]Delivery, int64, error) {
	if _, err := s.repo.getSubscription(ctx, tenantID, subscriptionID); err != nil {
		return nil, 0, err
	}
	rows, total, err := s.repo.listDeliveries(ctx, tenantID, subscriptionID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	items := make([]Delivery, 0, len(rows))
	for _, row := range rows {
		items = append(items, Delivery{
			ID: row.ID, SubscriptionID: row.SubscriptionID, EventID: row.EventID,
			EventSequence: row.EventSequence, EventType: row.EventType, Status: row.Status,
			Attempts: row.Attempts, AvailableAt: row.AvailableAt, DeliveredAt: row.DeliveredAt,
			LastError: row.LastError, CreatedAt: row.CreatedAt,
		})
	}
	return items, total, nil
}

func (s *Service) normalize(tenantID guid.ID, input SubscriptionInput) (subscriptionRow, string, error) {
	name := strings.TrimSpace(input.Name)
	receiver, urlErr := s.normalizeURL(input.URL)
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = SubscriptionActive
	}
	types, typesErr := normalizeEventTypes(input.EventTypes)
	if tenantID.Zero() || name == "" || len(name) > 128 || urlErr != nil || typesErr != nil ||
		(status != SubscriptionActive && status != SubscriptionDisabled) || len(input.Secret) > 512 {
		return subscriptionRow{}, "", ErrInvalidSubscription
	}
	return subscriptionRow{
		TenantID: tenantID, Name: name, URL: receiver, EventTypes: types, Status: status,
	}, strings.TrimSpace(input.Secret), nil
}

// normalizeURL judges the receiver while the operator is still looking at the
// form. It is not the enforcement point: a name that resolves somewhere else
// later is caught on the dial by the same policy.
func (s *Service) normalizeURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("invalid receiver URL")
	}
	normalized := parsed.String()
	if len(normalized) > 2048 {
		return "", errors.New("receiver URL is too long")
	}
	if err := s.outbound.ValidateURL(normalized); err != nil {
		return "", fmt.Errorf("invalid receiver URL: %w", err)
	}
	return normalized, nil
}

// normalizeEventTypes sorts and de-duplicates the selection so two subscriptions
// asking for the same thing store the same value.
func normalizeEventTypes(values []string) (string, error) {
	seen := map[string]struct{}{}
	kept := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > 64 || strings.ContainsAny(value, " \t\n") {
			return "", errors.New("invalid event type")
		}
		if _, repeated := seen[value]; repeated {
			continue
		}
		seen[value] = struct{}{}
		kept = append(kept, value)
	}
	sort.Strings(kept)
	joined := strings.Join(kept, " ")
	if len(joined) > 1024 {
		return "", errors.New("too many event types")
	}
	return joined, nil
}

func projectSubscription(row model.Subscription) Subscription {
	types := []string{}
	if trimmed := strings.TrimSpace(row.EventTypes); trimmed != "" {
		types = strings.Fields(trimmed)
	}
	return Subscription{
		ID: row.ID, TenantID: row.TenantID, Name: row.Name, URL: row.URL,
		SecretSet: row.SecretCiphertext != "", EventTypes: types, Status: row.Status,
		AfterSequence: row.AfterSequence, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
