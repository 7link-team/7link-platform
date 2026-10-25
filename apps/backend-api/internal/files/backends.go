package files

import (
	"context"
	"errors"
	"io"
	"slices"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"gorm.io/gorm"
)

// BackendConfig names a driver independently of an object's persisted backend
// identity. Old identities remain readable while writes use the active backend.
type BackendConfig struct {
	Driver  string            `mapstructure:"driver"`
	Options map[string]string `mapstructure:"options"`
}

type backendDriver struct {
	active  string
	drivers map[string]Driver
	db      func() *gorm.DB
	prefix  string
}

// The caller already loaded and authorized this row in its transaction. Never
// open another database connection just to rediscover its immutable identity.
func openStoredObject(ctx context.Context, driver Driver, object *model.Object) (io.ReadCloser, error) {
	if object == nil || driver == nil {
		return nil, ErrNotFound
	}
	if set, ok := driver.(*backendDriver); ok {
		stored := set.drivers[object.BackendID]
		if stored == nil {
			return nil, ErrUnsupported
		}
		return stored.Open(ctx, object.StorageKey)
	}
	return driver.Open(ctx, object.StorageKey)
}

func (d *backendDriver) current() Driver            { return d.drivers[d.active] }
func (d *backendDriver) Capabilities() Capabilities { return d.current().Capabilities() }
func (d *backendDriver) resolveName(ctx context.Context, key string) (string, error) {
	if !validKey(key) {
		return "", ErrInvalidKey
	}
	if d.db == nil || d.db() == nil {
		return "", ErrUnsupported
	}
	q := query.Bind(d.db(), d.prefix)
	rows, err := q.Object.WithContext(ctx).Where(q.Object.StorageKey.Eq(key), q.Object.State.Neq("deleted")).Limit(2).Find()
	if err != nil {
		return "", err
	}
	if len(rows) > 1 {
		return "", ErrUnsupported
	}
	backend := d.active
	if len(rows) == 1 {
		backend = rows[0].BackendID
	} else {
		intents, err := q.AccountPreparation.WithContext(ctx).Where(q.AccountPreparation.StorageKey.Eq(key), q.AccountPreparation.State.Neq("released")).Limit(2).Find()
		if err != nil {
			return "", err
		}
		if len(intents) > 1 {
			return "", ErrUnsupported
		}
		if len(intents) == 1 {
			backend = intents[0].BackendID
		}
	}
	driver := d.drivers[backend]
	if driver == nil {
		return "", ErrUnsupported
	}
	return backend, nil
}
func (d *backendDriver) resolve(ctx context.Context, key string) (Driver, error) {
	name, err := d.resolveName(ctx, key)
	if err != nil {
		return nil, err
	}
	return d.drivers[name], nil
}
func (a *AccountObjects) readableBackends() []string {
	if d, ok := a.driver.(*backendDriver); ok {
		names := make([]string, 0, len(d.drivers))
		for name := range d.drivers {
			names = append(names, name)
		}
		slices.Sort(names)
		return names
	}
	return []string{a.backend}
}
func (d *backendDriver) Put(ctx context.Context, key string, r io.Reader, size int64) (string, error) {
	return d.current().Put(ctx, key, r, size)
}
func (d *backendDriver) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	v, e := d.resolve(ctx, key)
	if e != nil {
		return nil, e
	}
	return v.Open(ctx, key)
}
func (d *backendDriver) Delete(ctx context.Context, key string) error {
	v, e := d.resolve(ctx, key)
	if e != nil {
		return e
	}
	return v.Delete(ctx, key)
}
func (d *backendDriver) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	v, e := d.resolve(ctx, key)
	if e != nil {
		return nil, e
	}
	r, ok := v.(RangeDriver)
	if !ok {
		return nil, ErrUnsupported
	}
	return r.OpenRange(ctx, key, offset, length)
}
func (d *backendDriver) Copy(ctx context.Context, source, destination string) (string, error) {
	name, e := d.resolveName(ctx, source)
	if e != nil {
		return "", e
	}
	v := d.drivers[name]
	if name != d.active {
		return "", ErrUnsupported
	}
	c, ok := v.(CopyDriver)
	if !ok {
		return "", ErrUnsupported
	}
	return c.Copy(ctx, source, destination)
}
func (d *backendDriver) Begin(ctx context.Context, key string) (string, error) {
	v, ok := d.current().(MultipartDriver)
	if !ok {
		return "", ErrUnsupported
	}
	return v.Begin(ctx, key)
}
func (d *backendDriver) WritePart(ctx context.Context, key, handle string, index int, r io.Reader, size int64) (PartReceipt, error) {
	v, ok := d.current().(MultipartDriver)
	if !ok {
		return PartReceipt{}, ErrUnsupported
	}
	return v.WritePart(ctx, key, handle, index, r, size)
}
func (d *backendDriver) InspectParts(ctx context.Context, key, handle string) ([]PartReceipt, error) {
	v, ok := d.current().(MultipartDriver)
	if !ok {
		return nil, ErrUnsupported
	}
	return v.InspectParts(ctx, key, handle)
}
func (d *backendDriver) Complete(ctx context.Context, key, handle string, parts []PartReceipt) (string, error) {
	v, ok := d.current().(MultipartDriver)
	if !ok {
		return "", ErrUnsupported
	}
	return v.Complete(ctx, key, handle, parts)
}
func (d *backendDriver) Abort(ctx context.Context, key, handle string) error {
	v, ok := d.current().(MultipartDriver)
	if !ok {
		return ErrUnsupported
	}
	return v.Abort(ctx, key, handle)
}
func (d *backendDriver) ReclaimWrite(ctx context.Context, key string) error {
	v, e := d.resolve(ctx, key)
	if e != nil {
		return e
	}
	if r, ok := v.(IncompleteWriteReclaimer); ok {
		return r.ReclaimWrite(ctx, key)
	}
	return v.Delete(ctx, key)
}

func (m *Module) configuredDriver(active string, options DriverConfig) (Driver, error) {
	current, err := m.registry.Build(active, options)
	if err != nil {
		return nil, err
	}
	if len(m.config.ReadBackends) == 0 && active == "local" {
		return current, nil
	}
	drivers := map[string]Driver{active: current}
	for name, config := range m.config.ReadBackends {
		if name == "" || len(name) > 64 || name == active {
			return nil, errors.New("files: invalid or duplicate read backend identity")
		}
		v, err := m.registry.Build(config.Driver, DriverConfig(config.Options))
		if err != nil {
			return nil, err
		}
		drivers[name] = v
	}
	return &backendDriver{active: active, drivers: drivers, prefix: m.runtime.TablePrefix(), db: func() *gorm.DB {
		if m.runtime.Database() == nil {
			return nil
		}
		return m.runtime.Database().Write()
	}}, nil
}
