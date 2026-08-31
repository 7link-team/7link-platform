package appenv_test

import (
	"reflect"
	"testing"

	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
)

func TestRuntimePreservesApplicationIdentityAndManagers(t *testing.T) {
	router := &gormx.DatasourceRouter{}
	runtime := appenv.NewRuntime(appenv.NewIdentity("api", "1.0.0"), appenv.WithDatabase(router))

	assert.Equal(t, "api", runtime.Identity().Name())
	assert.Equal(t, "1.0.0", runtime.Identity().Version())
	assert.Same(t, router, runtime.Database())
}

func TestIdentityExposesNoMutableFields(t *testing.T) {
	identityType := reflect.TypeFor[appenv.Identity]()
	for index := range identityType.NumField() {
		assert.False(t, identityType.Field(index).IsExported())
	}

	runtime := appenv.NewRuntime(appenv.NewIdentity("api", "1.0.0"))
	assert.Equal(t, "api", runtime.Identity().Name())
	assert.Nil(t, runtime.Database())
}
