package identity

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/iam/identity/query"
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

func (s *Service) backfillMissingAvatars(ctx context.Context) error {
	if s.repo.writer() == nil {
		return nil
	}
	principals := query.BindWithNames(s.repo.writer(), s.repo.names).Principals
	var last guid.ID
	for {
		rows, err := principals.WithContext(ctx).Where(principals.ID.Gt(last), principals.Avatar.Eq(""), principals.Status.Eq("active")).Order(principals.ID.Asc()).Limit(100).Find()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if _, err := s.setMyAvatar(ctx, row.ID, avatarInput{}, true); err != nil {
				if errors.Is(err, ErrNotFound) || errors.Is(err, ErrPrincipalInactive) {
					continue
				}
				return fmt.Errorf("backfill avatar for principal %s: %w", row.ID.String(), err)
			}
		}
		last = rows[len(rows)-1].ID
	}
}

func (s *Service) migrateAvatarStorage(ctx context.Context, importer files.LegacyImporter) error {
	if s.repo.writer() == nil {
		return nil
	}
	principals := query.BindWithNames(s.repo.writer(), s.repo.names).Principals
	var last guid.ID
	for {
		do := principals.WithContext(ctx).Where(principals.Avatar.Neq("")).Order(principals.ID.Asc()).Limit(100)
		if !last.Zero() {
			do = do.Where(principals.ID.Gt(last))
		}
		rows, err := do.Find()
		if err != nil {
			return fmt.Errorf("scan legacy avatars: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			avatar := storedAvatar(row.Avatar)
			if avatar == nil {
				return fmt.Errorf("invalid avatar reference for principal %s", row.ID.String())
			}
			name, ok := avatarStorageName(row.ID, avatar)
			if !ok {
				return ErrInvalid
			}
			if err := importer(ctx, "avatars/"+row.ID.String()+"/"+name); err != nil {
				return fmt.Errorf("migrate avatar for principal %s: %w", row.ID.String(), err)
			}
		}
		last = rows[len(rows)-1].ID
	}
}

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

func avatarStorageName(id guid.ID, avatar *Avatar) (string, bool) {
	if avatar == nil {
		return "", false
	}
	var name string
	for _, prefix := range []string{
		"/api/avatars/" + id.String() + "/",
		"/api/iam/avatars/" + id.String() + "/",
	} {
		if strings.HasPrefix(avatar.URL, prefix) {
			name = strings.TrimPrefix(avatar.URL, prefix)
			break
		}
	}
	if len(name) != 52 || filepath.Base(name) != name || !strings.HasSuffix(name, ".png") {
		return "", false
	}
	if _, err := hex.DecodeString(strings.TrimSuffix(name, ".png")); err != nil {
		return "", false
	}
	return name, true
}

func currentAvatar(id guid.ID, avatar *Avatar) *Avatar {
	if avatar == nil {
		return nil
	}
	return &Avatar{Style: avatar.Style, URL: "/api/principals/" + id.String() + "/avatar"}
}

func candidateAvatar(avatar Avatar, id guid.ID) (Avatar, error) {
	name, ok := avatarStorageName(id, &avatar)
	if !ok {
		return Avatar{}, ErrInvalid
	}
	avatar.URL = "/api/me/avatar-candidates/" + strings.TrimSuffix(name, ".png")
	return avatar, nil
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

func (r *Repository) saveAvatarFile(ctx context.Context, id guid.ID, pixels []byte, style string) (Avatar, error) {
	if r.storage == nil {
		return Avatar{}, ErrUnavailable
	}
	var key [24]byte
	if _, err := rand.Read(key[:]); err != nil {
		return Avatar{}, err
	}
	name := hex.EncodeToString(key[:]) + ".png"
	if _, err := r.storage.Put(ctx, "avatars/"+id.String()+"/"+name, bytes.NewReader(pixels), int64(len(pixels))); err != nil {
		return Avatar{}, err
	}
	return Avatar{Style: style, URL: "/api/avatars/" + id.String() + "/" + name}, nil
}

func (r *Repository) initializeAvatar(ctx context.Context, row *model.Principal) error {
	if row.Avatar != "" {
		return nil
	}
	pixels, style, err := avatarPixels(avatarInput{})
	if err != nil {
		return err
	}
	avatar, err := r.saveAvatarFile(ctx, row.ID, pixels, style)
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

func (r *Repository) prepareAvatarFile(ctx context.Context, id guid.ID, pixels []byte, style string) (Avatar, *files.PreparedAccountObject, guid.ID, error) {
	if r.accountFiles == nil || r.inTx {
		return Avatar{}, nil, 0, ErrUnavailable
	}
	objectID, err := r.nextID()
	if err != nil {
		return Avatar{}, nil, 0, err
	}
	referenceID, err := r.nextID()
	if err != nil {
		return Avatar{}, nil, 0, err
	}
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		return Avatar{}, nil, 0, err
	}
	name := hex.EncodeToString(token[:]) + ".png"
	prepared, err := r.accountFiles.Stage(ctx, r.writer(), objectID, id, "avatars/"+id.String()+"/"+name, bytes.NewReader(pixels), int64(len(pixels)), time.Now().Add(24*time.Hour))
	if err != nil {
		return Avatar{}, nil, 0, err
	}
	return Avatar{Style: style, URL: "/api/avatars/" + id.String() + "/" + name}, prepared, referenceID, nil
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
		return *currentAvatar(id, avatar), nil
	}
	return Avatar{}, ErrNotFound
}

// SetMyAvatar writes a normalized PNG first, then saves its reference and audit event.
func (s *Service) SetMyAvatar(ctx context.Context, id guid.ID, in avatarInput) (Avatar, error) {
	return s.setMyAvatar(ctx, id, in, false)
}

func (s *Service) setMyAvatar(ctx context.Context, id guid.ID, in avatarInput, onlyMissing bool) (Avatar, error) {
	if s.repo.accountFiles == nil {
		return Avatar{}, ErrUnavailable
	}
	row, err := s.repo.lifecyclePrincipal(ctx, id, false)
	if err != nil {
		return Avatar{}, err
	}
	if row.Status != "active" {
		return Avatar{}, ErrPrincipalInactive
	}
	var pixels []byte
	style := in.Style
	if in.CandidateURL != "" {
		prefix := "/api/me/avatar-candidates/"
		candidateID := strings.TrimPrefix(in.CandidateURL, prefix)
		if !strings.HasPrefix(in.CandidateURL, prefix) || len(candidateID) != 48 || in.ImageData != "" {
			return Avatar{}, ErrInvalid
		}
		if _, err := hex.DecodeString(candidateID); err != nil {
			return Avatar{}, ErrInvalid
		}
		valid := false
		for _, value := range avatarStyles {
			valid = valid || style == value
		}
		if !valid {
			return Avatar{}, ErrInvalid
		}
	} else {
		pixels, style, err = avatarPixels(in)
	}
	if err != nil {
		return Avatar{}, err
	}
	avatar := Avatar{Style: style}
	var prepared *files.PreparedAccountObject
	var referenceID guid.ID
	if in.CandidateURL == "" {
		avatar, prepared, referenceID, err = s.repo.prepareAvatarFile(ctx, id, pixels, style)
	} else {
		avatar.URL = "/api/avatars/" + id.String() + "/" + strings.TrimPrefix(in.CandidateURL, "/api/me/avatar-candidates/") + ".png"
		referenceID, err = s.repo.nextID()
	}
	if err != nil {
		return Avatar{}, err
	}
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		row, err := repo.lifecyclePrincipal(ctx, id, true)
		if err != nil {
			return err
		}
		if row.Status != "active" {
			return ErrPrincipalInactive
		}
		if onlyMissing {
			if current := storedAvatar(row.Avatar); current != nil {
				avatar = *current
				return nil
			}
		}
		if prepared != nil {
			err = repo.accountFiles.ReplaceAvatar(ctx, repo.writer(), prepared, referenceID, id)
		} else {
			name, _ := avatarStorageName(id, &avatar)
			err = repo.accountFiles.UseAvatarCandidate(ctx, repo.writer(), id, referenceID, "avatars/"+id.String()+"/"+name, time.Now())
		}
		if errors.Is(err, files.ErrNotFound) {
			return ErrNotFound
		}
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
		return Avatar{}, err
	}
	return *currentAvatar(id, &avatar), nil
}

func (r *Repository) removeAvatarFile(id guid.ID, avatar *Avatar) {
	if avatar == nil || avatar.URL == "" {
		return
	}
	prefix := "/api/avatars/" + id.String() + "/"
	if !strings.HasPrefix(avatar.URL, prefix) {
		return
	}
	name := strings.TrimPrefix(avatar.URL, prefix)
	if filepath.Base(name) != name {
		return
	}
	if r.storage != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.storage.Delete(ctx, "avatars/"+id.String()+"/"+name)
	}
}

func (r *Repository) readAvatarFile(ctx context.Context, id guid.ID, name string) ([]byte, error) {
	if r.storage == nil {
		return nil, ErrUnavailable
	}
	reader, err := r.storage.Open(ctx, "avatars/"+id.String()+"/"+name)
	if err != nil {
		return nil, err
	}
	pixels, err := io.ReadAll(io.LimitReader(reader, 2*1024*1024+1))
	err = errors.Join(err, reader.Close())
	if err == nil && len(pixels) > 2*1024*1024 {
		return nil, ErrInvalid
	}
	return pixels, err
}

// AvatarCandidates creates a private expiring batch in one account transaction.
func (s *Service) AvatarCandidates(ctx context.Context, id guid.ID, requestedStyle string) ([]Avatar, error) {
	row, err := s.repo.lifecyclePrincipal(ctx, id, false)
	if err != nil {
		return nil, err
	}
	if row.Status != "active" {
		return nil, ErrPrincipalInactive
	}
	candidates := make([]Avatar, 0, 6)
	preparedFiles := make([]*files.PreparedAccountObject, 0, 6)
	referenceIDs := make([]guid.ID, 0, 6)
	expiry := time.Now().Add(24 * time.Hour)
	for i := 0; i < 6; i++ {
		pixels, style, err := avatarPixels(avatarInput{Style: requestedStyle})
		if err != nil {
			return nil, err
		}
		avatar, prepared, referenceID, err := s.repo.prepareAvatarFile(ctx, id, pixels, style)
		if err != nil {
			return nil, err
		}
		candidate, err := candidateAvatar(avatar, id)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
		preparedFiles = append(preparedFiles, prepared)
		referenceIDs = append(referenceIDs, referenceID)
	}
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		row, err := repo.lifecyclePrincipal(ctx, id, true)
		if err != nil {
			return err
		}
		if row.Status != "active" {
			return ErrPrincipalInactive
		}
		for i, prepared := range preparedFiles {
			if err := repo.accountFiles.Register(ctx, repo.writer(), prepared, referenceIDs[i], id, "avatar_candidate", &expiry); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return candidates, nil
}

func registerAvatarHTTP(api huma.API, s *Service, registrar *authz.Registrar) {
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "identity-my-avatar", Method: http.MethodGet, Path: "/me/avatar", Summary: "Read my avatar", Tags: []string{"identity"}}, func(ctx context.Context, in *ClosureBrowserInput) (*respx.Body[Avatar], error) {
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
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "identity-set-my-avatar", Method: http.MethodPut, Path: "/me/avatar", Summary: "Replace my avatar", Tags: []string{"identity"}, MaxBodyBytes: 2900000}, func(ctx context.Context, in *input) (*respx.Body[Avatar], error) {
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
		TenantID    string `query:"tenant_id" maxLength:"19"`
	}
	type candidateImageInput struct {
		ClosureBrowserInput
		CandidateID string `path:"candidate_id" pattern:"^[a-f0-9]{48}$" minLength:"48" maxLength:"48"`
	}
	type imageOutput struct {
		ContentType  string `header:"Content-Type"`
		CacheControl string `header:"Cache-Control"`
		NoSniff      string `header:"X-Content-Type-Options"`
		Body         []byte
	}
	authz.RegisterAuthenticated(registrar, api, huma.Operation{OperationID: "identity-avatar-image", Method: http.MethodGet, Path: "/principals/{principal_id}/avatar", Summary: "Read an authorized account avatar image", Tags: []string{"identity"}}, func(ctx context.Context, in *imageInput) (*imageOutput, error) {
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
		name, valid := avatarStorageName(id, avatar)
		if row.Status != "active" || !valid {
			return nil, huma.Error404NotFound("respx.not_found")
		}
		pixels, err := s.repo.readAvatarFile(ctx, id, name)
		if err != nil {
			return nil, huma.Error404NotFound("respx.not_found")
		}
		return &imageOutput{ContentType: "image/png", CacheControl: "private, no-store", NoSniff: "nosniff", Body: pixels}, nil
	})
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "identity-avatar-candidate-image", Method: http.MethodGet, Path: "/me/avatar-candidates/{candidate_id}", Summary: "Read my private expiring avatar candidate", Tags: []string{"identity"}}, func(ctx context.Context, in *candidateImageInput) (*imageOutput, error) {
		id, err := closurePrincipal(ctx, in.Cookie, in.Authorization)
		if err != nil {
			return nil, err
		}
		name := in.CandidateID + ".png"
		if s.repo.accountFiles == nil {
			return nil, huma.Error404NotFound("respx.not_found")
		}
		readable, err := s.repo.accountFiles.AccountKeyReadable(ctx, s.repo.writer(), id, "avatars/"+id.String()+"/"+name, time.Now())
		if err != nil {
			return nil, identityError(err)
		}
		if !readable {
			return nil, huma.Error404NotFound("respx.not_found")
		}
		pixels, err := s.repo.readAvatarFile(ctx, id, name)
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
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "identity-avatar-candidates", Method: http.MethodPost, Path: "/me/avatar-candidates", Summary: "Generate six private server-side avatar candidates", Tags: []string{"identity"}}, func(ctx context.Context, in *candidateInput) (*respx.Body[[]Avatar], error) {
		id, err := s.closureMutationPrincipal(ctx, in.ClosureBrowserInput, http.MethodPost)
		if err != nil {
			return nil, err
		}
		candidates, err := s.AvatarCandidates(ctx, id, in.Body.Style)
		if err != nil {
			return nil, identityError(err)
		}
		return respx.OK(ctx, candidates), nil
	})
}
