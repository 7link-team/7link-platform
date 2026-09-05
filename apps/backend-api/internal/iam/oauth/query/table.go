package query

import (
	"7link.com/platform/internal/iam/oauth/model"
	"gorm.io/gorm"
)

type (
	Clients            = client
	AuthorizationCodes = authorizationCode
	Consents           = consent
	ConsentRequests    = consentRequest
	RefreshTokens      = refreshToken
)

type Tables struct {
	Clients            *Clients
	AuthorizationCodes *AuthorizationCodes
	Consents           *Consents
	ConsentRequests    *ConsentRequests
	RefreshTokens      *RefreshTokens
}

// Bind applies prefix to every table owned by the OAuth module.
func Bind(db *gorm.DB, prefix string) *Tables {
	q := Use(db)
	return &Tables{
		Clients:            q.Client.Table(prefix + model.Client{}.TableName()),
		AuthorizationCodes: q.AuthorizationCode.Table(prefix + model.AuthorizationCode{}.TableName()),
		Consents:           q.Consent.Table(prefix + model.Consent{}.TableName()),
		ConsentRequests:    q.ConsentRequest.Table(prefix + model.ConsentRequest{}.TableName()),
		RefreshTokens:      q.RefreshToken.Table(prefix + model.RefreshToken{}.TableName()),
	}
}
