package fleet

import (
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"fmt"
	"golang.org/x/net/publicsuffix"
	"gorm.io/gorm"
	"net"
	"net/url"
	"strings"
	"time"
)

type PreviewTLSConfig struct {
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
}

// PreviewConfig isolates untrusted application content from the console origin.
type PreviewConfig struct {
	Domain            string           `mapstructure:"domain"`
	ConsoleOrigin     string           `mapstructure:"console_origin"`
	Mode              string           `mapstructure:"mode" default:"SUBDOMAIN"`
	Listen            string           `mapstructure:"listen"`
	TLS               PreviewTLSConfig `mapstructure:"tls"`
	MaxSessionMinutes int              `mapstructure:"max_session_minutes" default:"480"`
	TrustedProxies    []string         `mapstructure:"trusted_proxies"`
}

func (c PreviewConfig) Validate() error {
	if len(c.TrustedProxies) != 0 {
		return fmt.Errorf("preview trusted proxies are unsupported; configure the direct TLS gateway")
	}
	if c.Domain == "" {
		return nil
	}
	if net.ParseIP(c.Domain) != nil || len(c.Domain) > 253 {
		return fmt.Errorf("preview domain must be a DNS hostname")
	}
	for _, part := range strings.Split(c.Domain, ".") {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return fmt.Errorf("preview domain must be a DNS hostname")
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return fmt.Errorf("preview domain must be a DNS hostname")
			}
		}
	}
	if strings.ContainsAny(c.Domain, "/:@ \t\r\n") || strings.ToLower(c.Domain) != c.Domain || strings.HasSuffix(c.Domain, ".") {
		return fmt.Errorf("preview domain must be a DNS hostname")
	}
	root, err := publicsuffix.EffectiveTLDPlusOne(c.Domain)
	if err != nil {
		return fmt.Errorf("preview domain: %w", err)
	}
	console, err := url.Parse(c.ConsoleOrigin)
	if err != nil || console.Scheme != "https" || console.Host == "" || console.User != nil || console.RawQuery != "" || console.Fragment != "" || (console.Path != "" && console.Path != "/") {
		return fmt.Errorf("preview requires an HTTPS console origin")
	}
	consoleRoot, err := publicsuffix.EffectiveTLDPlusOne(console.Hostname())
	if err != nil || strings.EqualFold(root, consoleRoot) {
		return fmt.Errorf("preview and console must use different registrable domains")
	}
	if c.Mode != "" && c.Mode != "SUBDOMAIN" && c.Mode != "PATH" {
		return fmt.Errorf("invalid preview mode")
	}
	if c.MaxSessionMinutes != 0 && (c.MaxSessionMinutes < 1 || c.MaxSessionMinutes > 1440) {
		return fmt.Errorf("preview max session minutes out of range")
	}
	if (c.TLS.CertFile == "") != (c.TLS.KeyFile == "") {
		return fmt.Errorf("preview TLS certificate and key must be configured together")
	}
	return nil
}
func (c PreviewConfig) URL(label, path string) string {
	if c.Domain == "" || !validPreviewLabel(label) {
		return ""
	}
	if path == "" {
		path = "/"
	}
	if c.Mode == "PATH" {
		return "https://" + c.Domain + "/p/" + label + path
	}
	return "https://" + label + "." + c.Domain + path
}
func validPreviewLabel(label string) bool {
	if len(label) != 26 {
		return false
	}
	for _, r := range label {
		if !(r >= 'a' && r <= 'z' || r >= '2' && r <= '7') {
			return false
		}
	}
	return true
}

// PreviewGrant is reconstructed by execution authority for each gateway request.
type PreviewGrant struct {
	PrincipalID, AccessID                                                   guid.ID
	Cookie                                                                  string
	TunnelMaxStreams                                                        int
	SessionID, TenantID, ProjectID, MachineID, ExecutionID, RunnerSessionID guid.ID
	ConnEpoch, DispatchEpoch                                                int64
	Port                                                                    int
	Workspace                                                               string
	ExpiresAt                                                               time.Time
}
type PreviewAccessResolver struct {
	Revalidate   func(context.Context, PreviewGrant) error
	StopMachine  func(context.Context, guid.ID, guid.ID, string) error
	Authorize    func(context.Context, string, string, string) (PreviewGrant, error)
	ExchangeCode func(context.Context, string, string) (string, string, time.Time, error)
	Touch        func(context.Context, guid.ID) error
}
type PreviewAccessResolverConnector struct {
	Bind func(PreviewAccessResolver) error
}
type PreviewMachine struct {
	TunnelMaxStreams int
	RunnerSessionID  guid.ID
	ConnEpoch        int64
	Enabled          bool
	State            string
}

// PreviewRuntime exposes fleet-owned transport and machine facts, never browser credentials.
type PreviewRuntime struct {
	Config       func() PreviewConfig
	Available    func(guid.ID, guid.ID) bool
	Ports        func(context.Context, PreviewGrant) ([]int, error)
	ResetSession func(context.Context, guid.ID, string) error
	MachineOn    func(context.Context, *gorm.DB, guid.ID, guid.ID) (PreviewMachine, error)
}

func (m *Module) providePreview(s *module.Services) error {
	if err := module.Provide[PreviewAccessResolverConnector](s, PreviewAccessResolverConnector{Bind: func(r PreviewAccessResolver) error {
		if r.Authorize == nil || r.ExchangeCode == nil || r.Touch == nil || r.Revalidate == nil || m.previewAccess.Authorize != nil {
			return ErrInvalidArgument
		}
		m.previewAccess = r
		return nil
	}}); err != nil {
		return err
	}
	return module.Provide[*PreviewRuntime](s, &PreviewRuntime{
		Config: func() PreviewConfig { return m.config.Preview },
		Available: func(t, id guid.ID) bool {
			return m.config.Preview.Domain != "" && m.previewTransport != nil && m.previewTransport.Available != nil && m.previewTransport.Available(t, id)
		},
		Ports: func(ctx context.Context, g PreviewGrant) ([]int, error) {
			if m.previewTransport == nil || m.previewTransport.Ports == nil {
				return nil, ErrUnavailable
			}
			return m.previewTransport.Ports(ctx, g)
		},
		ResetSession: func(ctx context.Context, id guid.ID, reason string) error {
			if m.previewTransport == nil || m.previewTransport.ResetSession == nil {
				return nil
			}
			return m.previewTransport.ResetSession(ctx, id, reason)
		},
		MachineOn: m.previewMachineOn,
	})
}
func (m *Module) previewMachineOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID) (PreviewMachine, error) {
	var result PreviewMachine
	row, err := m.machineOn(ctx, tx, tenant, machine, true)
	if err != nil {
		return result, err
	}
	result.State = machineState(row, time.Now().UTC())
	result.ConnEpoch = row.ConnEpoch
	if row.CurrentSessionID != nil {
		result.RunnerSessionID = *row.CurrentSessionID
	}
	q := query.BindTerminalAccess(tx, m.names)
	access, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Enabled = access.AllowRemotePreview
	result.TunnelMaxStreams = access.TunnelMaxStreams
	return result, nil
}
