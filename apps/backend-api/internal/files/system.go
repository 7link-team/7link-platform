package files

import (
	"context"
	"errors"
	"net/http"
	"time"

	filesi18n "7link.com/platform/internal/files/i18n"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type systemFileContextKey struct{}

func systemFileContext(ctx context.Context) context.Context {
	return context.WithValue(personalContext(ctx), systemFileContextKey{}, true)
}

func systemFileScope(ctx context.Context) bool {
	system, _ := ctx.Value(systemFileContextKey{}).(bool)
	return system || authz.ResolvedResourceOwner(ctx).ScopeType == "system"
}

func registerSystemFileHTTP(api huma.API, service *operationService, registrar *authz.Registrar) {
	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "list-system-files", Method: http.MethodGet, Path: "/system/files",
		Summary: "List system shared file roots", Tags: []string{"system"},
	}, authz.Guard{Resource: "system_file", Verb: "view"}, func(ctx context.Context, in *FilePageInput) (*respx.Body[filePage], error) {
		return listFileCollection(systemFileContext(ctx), service, fileCollection{scope: ownerSystem}, *in)
	})
	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "create-system-file", Method: http.MethodPost, Path: "/system/files",
		Summary: "Create a system shared directory or upload a file", Tags: []string{"system"}, DefaultStatus: http.StatusCreated,
		MaxBodyBytes: service.maxUploadBytes + (1 << 20),
	}, authz.Guard{Resource: "system_file", Verb: "create"}, func(ctx context.Context, in *fileCreateInput) (*respx.Body[fileEntryView], error) {
		ctx = systemFileContext(ctx)
		form := in.RawBody.Data()
		if form != nil && form.ParentID == "" && form.Kind == "directory" && form.Scope == ownerSystem && form.Name == "" && !form.File.IsSet {
			root, err := service.ensureSystemRoot(ctx)
			if err != nil {
				return nil, fileHTTPError(err)
			}
			return respx.OK(ctx, root), nil
		}
		return createFile(ctx, service, form, false)
	})
	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "create-system-file-operation", Method: http.MethodPost, Path: "/system/file-operations",
		Summary: "Copy or move system shared files synchronously", Tags: []string{"system"}, DefaultStatus: http.StatusCreated,
	}, authz.Guard{Resource: "system_file", Verb: "create"}, func(ctx context.Context, in *fileOperationInput) (*respx.Body[operationResult], error) {
		request, err := fileOperationRequestOf(in)
		if err != nil {
			return nil, err
		}
		result, err := service.run(systemFileContext(ctx), in.IdempotencyKey, request)
		if err != nil {
			return nil, fileHTTPError(err)
		}
		return respx.OK(ctx, result), nil
	})
}

func (s *operationService) ensureSystemRoot(ctx context.Context) (fileEntryView, error) {
	var result fileEntryView
	claims, err := s.basicClaims(ctx)
	if err != nil {
		return result, err
	}
	if !systemFileScope(ctx) {
		return result, errOperationForbidden
	}
	tree := directoryTree{prefix: s.runtime.TablePrefix()}
	err = s.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		authorizer, err := s.authorize(ctx, tx, claims, tree)
		if err != nil {
			return err
		}
		allowed, err := s.systemPolicy(ctx, tx, "system_file_create", claims.PrincipalID)
		if err != nil {
			return err
		}
		if !allowed {
			return errOperationForbidden
		}
		q := query.Bind(tx, tree.prefix)
		space, err := q.Space.WithContext(ctx).Where(q.Space.ScopeKind.Eq(spaceSystem), q.Space.SystemSlot.Eq(1)).Take()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			spaceID, idErr := s.nextID()
			if idErr != nil {
				return idErr
			}
			rootID, idErr := s.nextID()
			if idErr != nil {
				return idErr
			}
			if err := tree.createSystemSpaceOn(ctx, tx, spaceID, rootID, time.Now()); err != nil {
				return err
			}
			space, err = q.Space.WithContext(ctx).Where(q.Space.ID.Eq(spaceID)).Take()
			if err != nil {
				return err
			}
			event := audit.NewDomainEvent(ctx, 0, "file_root_created", "file_node", rootID)
			event.Detail["scope"] = ownerSystem
			if err := s.appender(ctx, tx, event); err != nil {
				return err
			}
		}
		if err != nil {
			return err
		}
		root, err := q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(space.ID), q.Node.ParentID.IsNull(), q.Node.RootSlot.Eq(1), q.Node.State.Eq("active")).Take()
		if err != nil {
			return err
		}
		if err := authorizer.access(ctx, tx, space.ID, claims.PrincipalID, root.ID, "list"); err != nil {
			return err
		}
		result, err = fileEntryViewInSpace(ctx, q, root, space)
		return err
	})
	return result, err
}

// systemFileMenu is the system file management page this module adds to the
// default system menu. Its gate is the module's own system_file_view code.
func systemFileMenu() core.SystemMenuPage {
	return core.SystemMenuPage{Branch: core.SystemMenuBasicManagement, Menu: core.Menu{
		Label: "File management", Translations: filesi18n.Translations(filesi18n.MenuSystemFiles),
		Route: "/system/files", Icon: "folder", SortOrder: 20,
		PermissionCode: authz.PermissionCode("system_file", "view"), Status: core.MenuActive,
	}}
}
