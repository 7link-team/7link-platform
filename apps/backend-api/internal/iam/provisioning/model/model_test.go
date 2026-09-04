package model

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tableNamer interface{ TableName() string }

func TestProvisioningModelsOwnExactTableSet(t *testing.T) {
	t.Parallel()
	models := []tableNamer{Directory{}, Credential{}, Resource{}, Target{}, TargetResource{}}
	expected := []string{"iam_scim_directories", "iam_scim_credentials", "iam_scim_resources", "iam_scim_targets", "iam_scim_target_resources"}
	require.Len(t, models, 5)
	for index, item := range models {
		assert.Equal(t, expected[index], item.TableName())
	}
}

func TestProvisioningModelsPreserveNullableTimes(t *testing.T) {
	t.Parallel()
	pointer := reflect.TypeFor[*time.Time]()
	for _, field := range []struct {
		model any
		name  string
	}{{Credential{}, "ExpiresAt"}, {Credential{}, "LastUsedAt"}, {Credential{}, "RevokedAt"}, {Resource{}, "DeletedAt"}, {TargetResource{}, "DeletedAt"}} {
		typeOf := reflect.TypeOf(field.model)
		actual, ok := typeOf.FieldByName(field.name)
		require.True(t, ok, "%s.%s", typeOf.Name(), field.name)
		assert.Equal(t, pointer, actual.Type, "%s.%s", typeOf.Name(), field.name)
	}
}
