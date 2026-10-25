package query

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNotificationTablesApplyDeploymentPrefix(t *testing.T) {
	plain, prefixed := reflect.ValueOf(BindNames("")), reflect.ValueOf(BindNames("isolation_"))
	for i := 0; i < plain.NumField(); i++ {
		p := plain.Field(i).Interface().(interface{ String() string })
		q := prefixed.Field(i).Interface().(interface{ String() string })
		require.Equal(t, "isolation_"+p.String(), q.String())
	}
}
