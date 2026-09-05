package oauth

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"os"
	"sync"

	oauthi18n "7link.com/platform/internal/iam/oauth/i18n"
	platformi18n "7link.com/platform/pkg/i18n"
)

// defaultConsentPage is the built-in consent screen. It is deliberately a
// single self-contained document with no external references: the screen is
// shown mid-redirect on the authorization server's own origin, so anything it
// fetched from elsewhere would be a third party sitting inside the consent
// decision.
//
//go:embed consent.html
var defaultConsentPage string

// consentRenderer parses the screen once and remembers the outcome. It hangs
// off the service rather than being a package-level cache because the template
// is configuration, and two services in one process may be configured
// differently.
type consentRenderer struct {
	once sync.Once
	tpl  *template.Template
	err  error
}

// consentScopeLine is one requested scope as the screen shows it.
type consentScopeLine struct {
	// Name is the raw scope token.
	Name string
	// Description is the translated explanation, or the raw token again when
	// the scope has no translation. Detail says which of the two happened, so
	// the template does not print the token twice.
	Description string
	Detail      bool
}

// consentPageData is the whole contract between the service and whatever
// template renders the screen. Operators overriding TemplateFile write against
// these fields.
type consentPageData struct {
	Lang       string
	Title      string
	Prompt     string
	Action     string
	Challenge  string
	ClientName string
	Scopes     []consentScopeLine
	AllowLabel string
	DenyLabel  string
}

// consentTemplate parses the screen once. A TemplateFile that cannot be read
// or parsed is an error rather than a silent fallback to the built-in page,
// because falling back would quietly show the wrong branding forever.
func (s *Service) consentTemplate() (*template.Template, error) {
	s.consentPage.once.Do(func() {
		text := defaultConsentPage
		if path := s.consent.TemplateFile; path != "" {
			raw, err := os.ReadFile(path) //nolint:gosec // Operator-configured template path.
			if err != nil {
				s.consentPage.err = fmt.Errorf("read OAuth consent template: %w", err)
				return
			}
			text = string(raw)
		}
		parsed, err := template.New("oauth-consent").Parse(text)
		if err != nil {
			s.consentPage.err = fmt.Errorf("parse OAuth consent template: %w", err)
			return
		}
		s.consentPage.tpl = parsed
	})
	return s.consentPage.tpl, s.consentPage.err
}

// RenderConsent produces the consent screen for one parked request. It returns
// the rendered HTML so the HTTP layer decides status codes and headers.
func (s *Service) RenderConsent(ctx context.Context, cookieHeader, challenge string) ([]byte, error) {
	tpl, err := s.consentTemplate()
	if err != nil {
		return nil, err
	}
	request, err := s.PendingConsent(ctx, cookieHeader, challenge)
	if err != nil {
		return nil, err
	}
	data := consentPageData{
		Lang:       consentLang(ctx),
		Title:      platformi18n.TContext(ctx, oauthi18n.ConsentTitle, request.ClientName),
		Prompt:     platformi18n.TContext(ctx, oauthi18n.ConsentPrompt),
		Action:     s.consent.Path,
		Challenge:  request.Challenge,
		ClientName: request.ClientName,
		Scopes:     consentScopeLines(ctx, request.Scopes),
		AllowLabel: platformi18n.TContext(ctx, oauthi18n.ConsentAllow),
		DenyLabel:  platformi18n.TContext(ctx, oauthi18n.ConsentDeny),
	}
	var buffer bytes.Buffer
	if err := tpl.Execute(&buffer, data); err != nil {
		return nil, fmt.Errorf("render OAuth consent template: %w", err)
	}
	return buffer.Bytes(), nil
}

func consentLang(ctx context.Context) string {
	if locale := platformi18n.LocaleFromContext(ctx); locale != "" {
		return locale
	}
	return platformi18n.Locale()
}

// consentScopeLines translates each requested scope. An unknown scope still
// gets a line — showing the raw token is far better than hiding a permission
// the subject is about to grant.
func consentScopeLines(ctx context.Context, scopes []string) []consentScopeLine {
	lines := make([]consentScopeLine, 0, len(scopes))
	for _, scope := range scopes {
		key := oauthi18n.ScopeKey(scope)
		description := platformi18n.TContext(ctx, key)
		if description == key {
			lines = append(lines, consentScopeLine{Name: scope, Description: scope})
			continue
		}
		lines = append(lines, consentScopeLine{Name: scope, Description: description, Detail: true})
	}
	return lines
}
