package identity

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Avatar stores only the style and controlled file URL, never image content.
type Avatar struct {
	Style string `json:"style" enum:"robot,pixel,monster,geometric,custom"`
	URL   string `json:"url"`
}

type avatarInput struct {
	CandidateURL string `json:"candidate_url,omitempty" maxLength:"256"`
	Style        string `json:"style,omitempty" enum:"robot,pixel,monster,geometric,custom"`
	ImageData    string `json:"image_data,omitempty" maxLength:"2800000"`
}

var avatarStyles = []string{"robot", "pixel", "monster", "geometric"}

func storedAvatar(raw string) *Avatar {
	if raw == "" {
		return nil
	}
	var avatar Avatar
	if json.Unmarshal([]byte(raw), &avatar) != nil {
		return nil
	}
	return &avatar
}

func avatarPixels(in avatarInput) ([]byte, string, error) {
	if in.ImageData == "" {
		if in.Style == "" {
			var choice [1]byte
			if _, err := rand.Read(choice[:]); err != nil {
				return nil, "", err
			}
			in.Style = avatarStyles[int(choice[0])%len(avatarStyles)]
		}
		valid := false
		for _, style := range avatarStyles {
			valid = valid || in.Style == style
		}
		if !valid {
			return nil, "", ErrInvalid
		}
		pixels, err := randomAvatarPNG(in.Style)
		return pixels, in.Style, err
	}
	if in.Style != "custom" || len(in.ImageData) > 2800000 {
		return nil, "", ErrInvalid
	}
	parts := strings.SplitN(in.ImageData, ",", 2)
	if len(parts) != 2 {
		return nil, "", ErrInvalid
	}
	switch parts[0] {
	case "data:image/png;base64", "data:image/jpeg;base64", "data:image/webp;base64":
	default:
		return nil, "", ErrInvalid
	}
	raw, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil || len(raw) > 2*1024*1024 {
		return nil, "", ErrInvalid
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 2048 || config.Height > 2048 {
		return nil, "", ErrInvalid
	}
	if parts[0] != "data:image/"+format+";base64" {
		return nil, "", ErrInvalid
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", ErrInvalid
	}
	bounds := src.Bounds()
	side := min(bounds.Dx(), bounds.Dy())
	x, y := bounds.Min.X+(bounds.Dx()-side)/2, bounds.Min.Y+(bounds.Dy()-side)/2
	dst := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, image.Rect(x, y, x+side, y+side), draw.Src, nil)
	var encoded bytes.Buffer
	if png.Encode(&encoded, dst) != nil {
		return nil, "", ErrInvalid
	}
	return encoded.Bytes(), "custom", nil
}

func (r *Repository) saveAvatarFile(id guid.ID, pixels []byte, style string) (Avatar, error) {
	var key [24]byte
	if _, err := rand.Read(key[:]); err != nil {
		return Avatar{}, err
	}
	name := hex.EncodeToString(key[:]) + ".png"
	directory := filepath.Join(r.avatarDirectory, id.String())
	if err := os.MkdirAll(directory, 0700); err != nil {
		return Avatar{}, err
	}
	file, err := os.CreateTemp(directory, ".avatar-*")
	if err != nil {
		return Avatar{}, err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	_, writeErr := file.Write(pixels)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return Avatar{}, writeErr
	}
	if closeErr != nil {
		return Avatar{}, closeErr
	}
	if err := os.Rename(temporary, filepath.Join(directory, name)); err != nil {
		return Avatar{}, err
	}
	return Avatar{Style: style, URL: "/api/iam/avatars/" + id.String() + "/" + name}, nil
}

func (r *Repository) initializeAvatar(row *model.Principal) error {
	if row.Avatar != "" {
		return nil
	}
	pixels, style, err := avatarPixels(avatarInput{})
	if err != nil {
		return err
	}
	avatar, err := r.saveAvatarFile(row.ID, pixels, style)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(avatar)
	if err != nil {
		return err
	}
	row.Avatar = string(raw)
	return nil
}

// MyAvatar reads the caller's persisted global avatar reference.
func (s *Service) MyAvatar(ctx context.Context, id guid.ID) (Avatar, error) {
	row, err := s.repo.lifecyclePrincipal(ctx, id, false)
	if err != nil {
		return Avatar{}, err
	}
	if row.Status != "active" {
		return Avatar{}, ErrPrincipalInactive
	}
	if avatar := storedAvatar(row.Avatar); avatar != nil {
		return *avatar, nil
	}
	return s.SetMyAvatar(ctx, id, avatarInput{})
}

// SetMyAvatar writes a normalized PNG first, then saves its reference and audit event.
func (s *Service) SetMyAvatar(ctx context.Context, id guid.ID, in avatarInput) (Avatar, error) {
	var pixels []byte
	style := in.Style
	var err error
	if in.CandidateURL != "" {
		prefix := "/api/iam/avatars/" + id.String() + "/"
		name := strings.TrimPrefix(in.CandidateURL, prefix)
		if !strings.HasPrefix(in.CandidateURL, prefix) || len(name) != 52 || filepath.Base(name) != name || !strings.HasSuffix(name, ".png") || in.ImageData != "" {
			return Avatar{}, ErrInvalid
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(name, ".png")); err != nil {
			return Avatar{}, ErrInvalid
		}
		valid := false
		for _, value := range avatarStyles {
			valid = valid || style == value
		}
		if !valid {
			return Avatar{}, ErrInvalid
		}
		pixels, err = os.ReadFile(filepath.Join(s.repo.avatarDirectory, id.String(), name))
	} else {
		pixels, style, err = avatarPixels(in)
	}
	if err != nil {
		return Avatar{}, err
	}
	var avatar Avatar
	var previous *Avatar
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		row, err := repo.lifecyclePrincipal(ctx, id, true)
		if err != nil {
			return err
		}
		if row.Status != "active" {
			return ErrPrincipalInactive
		}
		previous = storedAvatar(row.Avatar)
		avatar, err = repo.saveAvatarFile(id, pixels, style)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(avatar)
		if err != nil {
			return err
		}
		if err := repo.updatePrincipalRow(ctx, id, map[string]any{"avatar": string(raw), "updated_at": identityTime(s.now())}); err != nil {
			return err
		}
		return repo.appendAudit(ctx, audit.NewDomainEvent(ctx, 0, "principal_avatar_updated", "principal", id))
	})
	if err != nil {
		s.repo.removeAvatarFile(id, &avatar)
	} else {
		s.repo.removeAvatarFile(id, previous)
	}
	return avatar, err
}

func (r *Repository) removeAvatarFile(id guid.ID, avatar *Avatar) {
	if avatar == nil || avatar.URL == "" {
		return
	}
	prefix := "/api/iam/avatars/" + id.String() + "/"
	if !strings.HasPrefix(avatar.URL, prefix) {
		return
	}
	name := strings.TrimPrefix(avatar.URL, prefix)
	if filepath.Base(name) != name {
		return
	}
	_ = os.Remove(filepath.Join(r.avatarDirectory, id.String(), name))
}

func registerAvatarHTTP(api huma.API, s *Service, registrar *authz.Registrar) {
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "identity-my-avatar", Method: http.MethodGet, Path: "/iam/me/avatar", Summary: "Read my avatar", Tags: []string{"identity"}}, func(ctx context.Context, in *ClosureBrowserInput) (*respx.Body[Avatar], error) {
		id, err := closurePrincipal(ctx, in.Cookie, in.Authorization)
		if err != nil {
			return nil, err
		}
		avatar, err := s.MyAvatar(ctx, id)
		if err != nil {
			return nil, identityError(err)
		}
		return respx.OK(ctx, avatar), nil
	})
	type input struct {
		ClosureBrowserInput
		Body avatarInput
	}
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "identity-set-my-avatar", Method: http.MethodPut, Path: "/iam/me/avatar", Summary: "Replace my avatar", Tags: []string{"identity"}, MaxBodyBytes: 2900000}, func(ctx context.Context, in *input) (*respx.Body[Avatar], error) {
		id, err := s.closureMutationPrincipal(ctx, in.ClosureBrowserInput, http.MethodPut)
		if err != nil {
			return nil, err
		}
		avatar, err := s.SetMyAvatar(ctx, id, in.Body)
		if err != nil {
			return nil, identityError(err)
		}
		return respx.OK(ctx, avatar), nil
	})
	type imageInput struct {
		PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
		File        string `path:"file" pattern:"^[a-f0-9]{48}\\.png$" maxLength:"52"`
		TenantID    string `query:"tenant_id" maxLength:"19"`
	}
	type imageOutput struct {
		ContentType  string `header:"Content-Type"`
		CacheControl string `header:"Cache-Control"`
		NoSniff      string `header:"X-Content-Type-Options"`
		Body         []byte
	}
	authz.RegisterAuthenticated(registrar, api, huma.Operation{OperationID: "identity-avatar-image", Method: http.MethodGet, Path: "/iam/avatars/{principal_id}/{file}", Summary: "Read an authorized account avatar image", Tags: []string{"identity"}}, func(ctx context.Context, in *imageInput) (*imageOutput, error) {
		claims, ok := authnext.FromContext(ctx)
		if !ok || claims == nil {
			return nil, huma.Error401Unauthorized("respx.unauthorized")
		}
		id, err := requireID(in.PrincipalID)
		if err != nil {
			return nil, err
		}
		if id != claims.PrincipalID {
			tenantID, err := requireID(in.TenantID)
			if err != nil || s.avatarPermissions == nil {
				return nil, huma.Error403Forbidden("respx.forbidden")
			}
			allowed, err := s.avatarPermissions.Check(ctx, tenantID, "user_view", claims.PrincipalID)
			if err != nil {
				return nil, identityError(err)
			}
			if !allowed {
				return nil, huma.Error403Forbidden("respx.forbidden")
			}
			if _, err := s.Get(ctx, tenantID, id); err != nil {
				return nil, identityError(err)
			}
			fields, err := authz.ProfileConstraint(ctx, s.fields, tenantID, claims.PrincipalID)
			if err != nil {
				return nil, identityError(err)
			}
			if fields.ReadString("display_name", "") == nil {
				return nil, huma.Error403Forbidden("respx.forbidden")
			}
		}
		row, err := s.repo.lifecyclePrincipal(ctx, id, false)
		if err != nil {
			return nil, identityError(err)
		}
		avatar := storedAvatar(row.Avatar)
		if row.Status != "active" || (id != claims.PrincipalID && (avatar == nil || avatar.URL != "/api/iam/avatars/"+id.String()+"/"+in.File)) {
			return nil, huma.Error404NotFound("respx.not_found")
		}
		pixels, err := os.ReadFile(filepath.Join(s.repo.avatarDirectory, id.String(), in.File))
		if err != nil {
			return nil, huma.Error404NotFound("respx.not_found")
		}
		return &imageOutput{ContentType: "image/png", CacheControl: "private, no-store", NoSniff: "nosniff", Body: pixels}, nil
	})
	type candidateInput struct {
		ClosureBrowserInput
		Body struct {
			Style string `json:"style,omitempty" enum:"robot,pixel,monster,geometric"`
		}
	}
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "identity-avatar-candidates", Method: http.MethodPost, Path: "/iam/me/avatar-candidates", Summary: "Generate six private server-side avatar candidates", Tags: []string{"identity"}}, func(ctx context.Context, in *candidateInput) (*respx.Body[[]Avatar], error) {
		id, err := s.closureMutationPrincipal(ctx, in.ClosureBrowserInput, http.MethodPost)
		if err != nil {
			return nil, err
		}
		row, err := s.repo.lifecyclePrincipal(ctx, id, false)
		if err != nil {
			return nil, identityError(err)
		}
		if row.Status != "active" {
			return nil, identityError(ErrPrincipalInactive)
		}
		candidates := make([]Avatar, 0, 6)
		for i := 0; i < 6; i++ {
			pixels, style, err := avatarPixels(avatarInput{Style: in.Body.Style})
			if err != nil {
				return nil, identityError(err)
			}
			avatar, err := s.repo.saveAvatarFile(id, pixels, style)
			if err != nil {
				return nil, identityError(err)
			}
			candidates = append(candidates, avatar)
		}
		return respx.OK(ctx, candidates), nil
	})
}
