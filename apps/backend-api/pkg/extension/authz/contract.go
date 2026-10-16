package authz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
)

// OperationContract carries the registered request schema, never executable rules.
// Schema references resolve only against the reachable definitions in Schemas.
type OperationContract struct {
	Locale        string                             `json:"locale"`
	Messages      map[string]respx.ValidationMessage `json:"messages"`
	IssueMessages map[string]respx.ValidationMessage `json:"issue_messages"`
	Version       string                             `json:"version"`
	OperationID   string                             `json:"operation_id"`
	Method        string                             `json:"method"`
	Path          string                             `json:"path"`
	Parameters    json.RawMessage                    `json:"parameters"`
	RequestBody   json.RawMessage                    `json:"request_body"`
	Schemas       map[string]json.RawMessage         `json:"schemas"`
}

type contractInput struct {
	Method string `query:"method" required:"true" enum:"GET,POST,PUT,PATCH,DELETE"`
}
type contractBase = huma.Context

type contractContext struct {
	contractBase
	operation *huma.Operation
}

func (c contractContext) Operation() *huma.Operation { return c.operation }

// Method-sensitive authorization (including read-only sessions) must evaluate
// the selected operation, not treat every metadata request as OPTIONS.
func (c contractContext) Method() string { return c.operation.Method }

// RegisterRequestContract opts a resource into OPTIONS after its operations
// have been registered. Authorization is the selected operation's exact chain;
// the business handler is never invoked. Call once per resource path.
func RegisterRequestContract(api huma.API, path string) error {
	item := api.OpenAPI().Paths[path]
	if item == nil || item.Options != nil {
		return fmt.Errorf("request contract requires an existing path without OPTIONS: %s", path)
	}
	selected := map[string]*huma.Operation{}
	contracts := map[string]OperationContract{}
	var params []*huma.Param
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		op := operations(item)[method]
		if op == nil {
			continue
		}
		if !IsPublicOperation(op) && len(op.Middlewares) == 0 {
			return fmt.Errorf("request contract requires active authorization: %s", op.OperationID)
		}
		contract, err := requestContract(api, op)
		if err != nil {
			return err
		}
		selected[method], contracts[method] = op, contract
		if params == nil {
			for _, p := range op.Parameters {
				if p.In == "path" {
					params = append(params, p)
				}
			}
		}
	}
	if len(selected) == 0 {
		return fmt.Errorf("request contract has no supported operations: %s", path)
	}
	middleware := func(ctx huma.Context, next func(huma.Context)) {
		// Browser CORS preflight is not a request for a form contract.
		if ctx.Header("Access-Control-Request-Method") != "" {
			_ = huma.WriteErr(api, ctx, http.StatusBadRequest, respx.InvalidArgument)
			return
		}
		op := selected[ctx.Query("method")]
		if op == nil {
			_ = huma.WriteErr(api, ctx, http.StatusUnprocessableEntity, respx.InvalidArgument)
			return
		}
		ctx.SetHeader("Cache-Control", "private, no-store")
		op.Middlewares.Handler(func(authorized huma.Context) {
			next(contractContext{contractBase: authorized, operation: ctx.Operation()})
		})(contractContext{contractBase: ctx, operation: op})
	}
	operation := huma.Operation{OperationID: "request-contract:" + path, Method: http.MethodOptions, Path: path, Summary: "Read an operation request contract", Tags: itemTag(selected), Parameters: params, Errors: []int{400, 401, 403, 404, 422, 503}, Middlewares: huma.Middlewares{middleware}, Metadata: map[string]any{authenticatedMetadataKey: true}, Security: UserSecurity()}
	huma.Register(api, operation, func(ctx context.Context, in *contractInput) (*respx.Body[OperationContract], error) {
		contract := contracts[in.Method]
		contract.Locale = i18n.CanonicalFromContext(ctx)
		contract.Messages, contract.IssueMessages = respx.ValidationMessages(ctx)
		contract, err := versionContract(contract)
		if err != nil {
			return nil, respx.Err(ctx, http.StatusInternalServerError, respx.InternalServerError)
		}
		return respx.OK(ctx, contract), nil
	})
	return nil
}

func itemTag(ops map[string]*huma.Operation) []string {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if op := ops[method]; op != nil {
			return append([]string(nil), op.Tags...)
		}
	}
	return nil
}

func requestContract(api huma.API, op *huma.Operation) (OperationContract, error) {
	out := OperationContract{OperationID: op.OperationID, Method: op.Method, Path: op.Path, Schemas: map[string]json.RawMessage{}}
	var err error
	out.Parameters, err = json.Marshal(op.Parameters)
	if err != nil {
		return out, err
	}
	out.RequestBody, err = json.Marshal(op.RequestBody)
	if err != nil {
		return out, err
	}
	var visit func(json.RawMessage) error
	visit = func(raw json.RawMessage) error {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		var walk func(any) error
		walk = func(value any) error {
			switch v := value.(type) {
			case map[string]any:
				if ref, ok := v["$ref"].(string); ok {
					const prefix = "#/components/schemas/"
					if !strings.HasPrefix(ref, prefix) {
						return fmt.Errorf("unsupported request schema reference: %s", ref)
					}
					name := strings.TrimPrefix(ref, prefix)
					if _, seen := out.Schemas[name]; !seen {
						schema := api.OpenAPI().Components.Schemas.Map()[name]
						if schema == nil {
							return fmt.Errorf("missing request schema: %s", name)
						}
						encoded, err := json.Marshal(schema)
						if err != nil {
							return err
						}
						out.Schemas[name] = encoded
						if err := visit(encoded); err != nil {
							return err
						}
					}
				}
				for _, child := range v {
					if err := walk(child); err != nil {
						return err
					}
				}
			case []any:
				for _, child := range v {
					if err := walk(child); err != nil {
						return err
					}
				}
			}
			return nil
		}
		return walk(value)
	}
	if err = visit(out.Parameters); err != nil {
		return out, err
	}
	if err = visit(out.RequestBody); err != nil {
		return out, err
	}
	return versionContract(out)
}

func versionContract(out OperationContract) (OperationContract, error) {
	out.Version = ""
	encoded, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	sum := sha256.Sum256(encoded)
	out.Version = hex.EncodeToString(sum[:])
	return out, nil
}
