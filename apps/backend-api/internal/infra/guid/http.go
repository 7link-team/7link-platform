package guid

import (
	"context"
	"net/http"

	guidi18n "7link.com/platform/internal/infra/guid/i18n"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

const maxBatch = 100

type batchInput struct {
	Count int `path:"count" minimum:"1" maximum:"100" doc:"number of IDs to generate"`
}

func registerHTTP(api huma.API, next func() (int64, error)) {
	huma.Register(api, huma.Operation{
		OperationID: "next-guid",
		Method:      http.MethodGet,
		Path:        "/guid",
		Summary:     "Generate a globally unique ID",
		Tags:        []string{"GUID"},
	}, func(ctx context.Context, _ *struct{}) (*respx.Body[ID], error) {
		id, err := next()
		if err != nil {
			return nil, huma.Error503ServiceUnavailable(guidi18n.NotReady)
		}
		return respx.OK(ctx, ID(id)), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "next-guid-batch",
		Method:      http.MethodGet,
		Path:        "/guid/{count}",
		Summary:     "Generate globally unique IDs",
		Tags:        []string{"GUID"},
	}, func(ctx context.Context, input *batchInput) (*respx.Body[[]ID], error) {
		ids := make([]ID, 0, input.Count)
		for range input.Count {
			id, err := next()
			if err != nil {
				return nil, huma.Error503ServiceUnavailable(guidi18n.NotReady)
			}
			ids = append(ids, ID(id))
		}
		return respx.OK(ctx, ids), nil
	})
}
