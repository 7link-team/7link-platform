package health

import "context"

type HealthModule struct {
}

func NewModule() *HealthModule {
	return &HealthModule{}
}

func (h *HealthModule) Initialize() error {

	return nil
}

func (h *HealthModule) Start() error {
	return nil
}

func (h *HealthModule) CheckHealth(ctx context.Context) error {
	return nil
}

func (h *HealthModule) Stop() error {
	return nil
}

func (h *HealthModule) Deinitialize() error {
	return nil
}
