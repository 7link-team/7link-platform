package fleet

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestEnsembleHTTPCommandsReplayAndReauthorize(t *testing.T) {
	m, db, ctx, _, _ := ensembleHarness(t)
	store := idempotency.NewModule(m.runtime)
	raw, e := db.DB()
	require.NoError(t, e)
	source := store.MigrationSource()
	source.Prefix = "dir_"
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, m.ids))
	require.NoError(t, services.Seal())
	require.NoError(t, store.InjectServices(services))
	m.idempotency = store
	router := fleetBusinessRouter(t, m)
	base := "/tenants/7/entities/9/ensemble-plans"
	headers := map[string]string{"Idempotency-Key": "create-alternatives"}
	body := `{"work_item_id":"20","execution_ids":["11","12"],"reason":"compare actual execution alternatives"}`
	first := fleetHTTPRequest(router, "POST", base, body, headers)
	require.Equal(t, 201, first.Code, first.Body.String())
	var a, b struct {
		Data EnsembleView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &a))
	again := fleetHTTPRequest(router, "POST", base, body, headers)
	require.Equal(t, 201, again.Code, again.Body.String())
	require.NoError(t, json.Unmarshal(again.Body.Bytes(), &b))
	require.Equal(t, a.Data, b.Data)
	conflict := fleetHTTPRequest(router, "POST", base, `{"work_item_id":"20","execution_ids":["11","13"],"reason":"different actual execution alternatives"}`, headers)
	require.Equal(t, 409, conflict.Code, conflict.Body.String())
	require.Equal(t, 200, fleetHTTPRequest(router, "GET", base+"?work_item_id=20", "", nil).Code)
	path := base + "/" + a.Data.ID.String()
	get := fleetHTTPRequest(router, "GET", path, "", nil)
	require.Equal(t, 200, get.Code, get.Body.String())
	decision := `{"action":"COMPARE","version":1,"reason":"freeze actual persisted results"}`
	h := map[string]string{"Idempotency-Key": "freeze-comparison"}
	changed := fleetHTTPRequest(router, "POST", path+"/decisions", decision, h)
	require.Equal(t, 201, changed.Code, changed.Body.String())
	replay := fleetHTTPRequest(router, "POST", path+"/decisions", decision, h)
	require.Equal(t, 201, replay.Code, replay.Body.String())
	require.NoError(t, json.Unmarshal(changed.Body.Bytes(), &a))
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &b))
	require.Equal(t, a.Data, b.Data)
	m.ensemble.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return ErrMachineForbidden }
	denied := fleetHTTPRequest(router, "POST", path+"/decisions", decision, h)
	require.Equal(t, 403, denied.Code, denied.Body.String())
	require.Equal(t, 403, fleetHTTPRequest(router, "GET", path, "", nil).Code)
}
