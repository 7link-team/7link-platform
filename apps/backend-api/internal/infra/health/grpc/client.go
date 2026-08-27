package grpc

import (
	"context"

	healthv1 "7link.com/platform/internal/infra/health/proto/gen/go/health/v1"
	"7link.com/platform/internal/infra/health/service"
	"7link.com/platform/pkg/i18n"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Client is the module-level gRPC client. It hides generated protobuf types
// from callers while preserving the shared response code and localized message.
type Client struct {
	rpc healthv1.HealthServiceClient
}

type CheckResponse struct {
	Code    int
	Message string
	Data    service.CheckData
}

func NewClient(conn grpc.ClientConnInterface) *Client {
	return &Client{rpc: healthv1.NewHealthServiceClient(conn)}
}

func (c *Client) Check(ctx context.Context, opts ...grpc.CallOption) (CheckResponse, error) {
	ctx = withLocale(ctx)
	result, err := c.rpc.Check(ctx, &healthv1.CheckRequest{}, opts...)
	if err != nil {
		return CheckResponse{}, err
	}

	data := service.CheckData{Status: service.StatusUnknown, Modules: make([]service.ModuleStatus, 0)}
	if result.GetData() != nil {
		data.Status = serviceStatus(result.GetData().GetStatus())
		for _, module := range result.GetData().GetModules() {
			data.Modules = append(data.Modules, service.ModuleStatus{
				Name:   module.GetName(),
				Status: serviceStatus(module.GetStatus()),
			})
		}
	}
	return CheckResponse{
		Code:    int(result.GetCode()),
		Message: result.GetMessage(),
		Data:    data,
	}, nil
}

func serviceStatus(status healthv1.CheckData_ServingStatus) service.Status {
	switch status {
	case healthv1.CheckData_SERVING_STATUS_SERVING:
		return service.StatusServing
	case healthv1.CheckData_SERVING_STATUS_NOT_SERVING:
		return service.StatusNotServing
	default:
		return service.StatusUnknown
	}
}

func withLocale(ctx context.Context) context.Context {
	if outgoing, ok := metadata.FromOutgoingContext(ctx); ok && len(outgoing.Get("accept-language")) > 0 {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "accept-language", i18n.CanonicalFromContext(ctx))
}
