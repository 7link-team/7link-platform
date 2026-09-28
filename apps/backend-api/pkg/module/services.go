package module

import (
	"errors"
	"fmt"
	"reflect"
)

var (
	ErrServiceMissing    = errors.New("module: service is not registered")
	ErrServiceDuplicate  = errors.New("module: service is already registered")
	ErrServicesProviding = errors.New("module: services registry is still accepting providers")
	ErrServicesSealed    = errors.New("module: services registry is sealed")
)

// Services is the application-scoped service registry used during module
// wiring. Modules receive their dependencies before Initialize is called.
type Services struct {
	values map[reflect.Type]any
	sealed bool
}

// NewServices returns an empty, unsealed service registry that accepts
// providers until Seal.
func NewServices() *Services {
	return &Services{values: make(map[reflect.Type]any)}
}

// Seal completes the provider phase. Services may only be resolved after the
// registry is sealed, and no more services may be provided.
func (services *Services) Seal() error {
	if services == nil {
		return errors.New("module: services registry is nil")
	}
	services.sealed = true
	return nil
}

// Provide registers one application-scoped implementation for service type T.
func Provide[T any](services *Services, service T) error {
	if services == nil {
		return errors.New("module: services registry is nil")
	}
	if services.sealed {
		return ErrServicesSealed
	}
	serviceType := reflect.TypeFor[T]()
	value := reflect.ValueOf(service)
	if !value.IsValid() || (canBeNil(value.Kind()) && value.IsNil()) {
		return fmt.Errorf("module: register %s: service is nil", serviceType)
	}
	if _, exists := services.values[serviceType]; exists {
		return fmt.Errorf("%w: %s", ErrServiceDuplicate, serviceType)
	}
	services.values[serviceType] = service
	return nil
}

// Resolve returns the application-scoped implementation registered for T.
func Resolve[T any](services *Services) (T, error) {
	var zero T
	if services == nil {
		return zero, errors.New("module: services registry is nil")
	}
	if !services.sealed {
		return zero, ErrServicesProviding
	}
	serviceType := reflect.TypeFor[T]()
	value, exists := services.values[serviceType]
	if !exists {
		return zero, fmt.Errorf("%w: %s", ErrServiceMissing, serviceType)
	}
	service, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("module: registered service %T does not implement %s", value, serviceType)
	}
	return service, nil
}

func canBeNil(kind reflect.Kind) bool {
	switch kind {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return true
	default:
		return false
	}
}
