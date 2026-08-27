package guid

import (
	"context"
	"strconv"

	guidi18n "7link.com/platform/internal/infra/guid/i18n"
	guidv1 "7link.com/platform/internal/infra/guid/proto/gen/go/guid/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type grpcServer struct {
	guidv1.UnimplementedGuidServiceServer
	next func() (int64, error)
}

func (server *grpcServer) Next(context.Context, *guidv1.NextRequest) (*guidv1.NextResponse, error) {
	id, err := server.next()
	if err != nil {
		return nil, status.Error(codes.Unavailable, guidi18n.NotReady)
	}
	return &guidv1.NextResponse{Id: strconv.FormatInt(id, 10)}, nil
}

func (server *grpcServer) NextBatch(_ context.Context, request *guidv1.NextBatchRequest) (*guidv1.NextBatchResponse, error) {
	count := int(request.GetCount())
	if count < 1 || count > maxBatch {
		return nil, status.Errorf(codes.InvalidArgument, "count must be 1..%d", maxBatch)
	}
	ids := make([]string, 0, count)
	for range count {
		id, err := server.next()
		if err != nil {
			return nil, status.Error(codes.Unavailable, guidi18n.NotReady)
		}
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	return &guidv1.NextBatchResponse{Ids: ids}, nil
}
