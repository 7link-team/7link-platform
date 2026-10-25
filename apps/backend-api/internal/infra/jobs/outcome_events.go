package jobs

import (
	"context"
	"errors"

	"7link.com/platform/internal/infra/events"

	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// WireJobEvents joins the durable job outcome and domain outbox in the caller's
// transaction. Neither provider imports the other; wiring completes before
// Initialize seals their catalogs. Migration-only/minimal registries may omit
func WireJobEvents(services *module.Services) error {
	connector, err := module.Resolve[DeadLetterConnector](services)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if connector.Bind == nil {
		return ErrUnavailable
	}
	publisher, err := module.Resolve[events.Publisher](services)
	if err != nil {
		return err
	}
	catalog, err := module.Resolve[events.Catalog](services)
	if err != nil {
		return err
	}
	if err := catalog.AddTypes(Name, events.TypeSpec{Name: "job.dead_lettered", SchemaVersion: 1, SubjectKind: "background_job", Realtime: events.RealtimeNone}); err != nil {
		return err
	}
	return connector.Bind(func(ctx context.Context, tx *gorm.DB, dead DeadLetter) error {
		_, err := publisher.Publish(ctx, tx, events.Event{
			TenantID: dead.TenantID, Type: "job.dead_lettered", Actor: events.Actor{Kind: "SYSTEM"},
			Subject: events.Subject{Kind: "background_job", ID: dead.JobID}, SubjectVersion: int64(dead.Version), CorrelationID: dead.CorrelationID,
			Data: struct {
				Kind     string `json:"kind"`
				Attempts int    `json:"attempts"`
			}{Kind: dead.Kind, Attempts: dead.Attempts},
		})
		return err
	})
}
