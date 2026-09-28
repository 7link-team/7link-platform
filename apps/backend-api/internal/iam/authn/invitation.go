package authn

import (
	"fmt"
	"strings"

	"7link.com/platform/internal/infra/guid"
)

const invitationTokenPrefix = "inv1_"

func (s *Service) IssueInvitationToken(id guid.ID) (string, string, error) {
	if s == nil || s.mfaKeys == nil || id.Zero() {
		return "", "", fmt.Errorf("invalid invitation credential request")
	}
	secret, err := randomToken(32)
	if err != nil {
		return "", "", fmt.Errorf("generate invitation credential: %w", err)
	}
	token := invitationTokenPrefix + id.String() + "." + secret
	digest, err := s.InvitationTokenDigest(token)
	return token, digest, err
}

// InvitationTokenDigest derives the digest to store for a freshly issued
// token, under the key that is currently sealing.
func (s *Service) InvitationTokenDigest(token string) (string, error) {
	message, err := invitationTokenMessage(s, token)
	if err != nil {
		return "", err
	}
	return s.mfaKeys.MAC("invitation:v1", message), nil
}

// InvitationTokenMatches reports whether a presented token produces the stored
// digest under any key the ring holds. Acceptance asks this rather than
// recomputing one digest, so an invitation issued before a key rotation is
// still accepted instead of silently reading as forged.
func (s *Service) InvitationTokenMatches(token, digest string) bool {
	message, err := invitationTokenMessage(s, token)
	if err != nil {
		return false
	}
	return s.mfaKeys.MACMatches("invitation:v1", message, digest)
}

func invitationTokenMessage(s *Service, token string) ([]byte, error) {
	if s == nil || s.mfaKeys == nil || !strings.HasPrefix(token, invitationTokenPrefix) || len(token) > 256 {
		return nil, fmt.Errorf("invalid invitation credential")
	}
	parts := strings.SplitN(strings.TrimPrefix(token, invitationTokenPrefix), ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid invitation credential")
	}
	return []byte("platform:invitation:v1\x00" + token), nil
}
