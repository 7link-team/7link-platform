package migration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"syscall"
	"testing"
	"time"

	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/realtime"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"7link.com/platform/tests/internal/testdb"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type realtimeLatencyAuthority struct{}

func (realtimeLatencyAuthority) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{PrincipalID: 10, Subject: "10"}, nil
}
func (realtimeLatencyAuthority) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}
func (realtimeLatencyAuthority) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}
func (realtimeLatencyAuthority) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return false, nil
}

// Two runtime/datasource instances share only committed database state. The
// browser displays each observation before recording its latency; receipt time
// is compared to pre-transaction time, a conservative upper bound on commit.
func TestRealtimeCrossReplicaCommitToBrowserLatencyAcrossEngines(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var dsn string
			if dialect == "mysql" {
				// The in-memory MySQL engine can roll back concurrent commits
				// even from a read-only transaction. Cross-replica delivery must
				// exercise the real engine in the mandatory MySQL 8 gate.
				dsn = os.Getenv("MYSQL8_DSN")
				if dsn == "" {
					t.Skip("requires real MySQL 8; run through make test-mysql8")
				}
				raw, err := sql.Open("mysql", dsn)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, raw.Close()) })
				verifyRealMySQLServer(t, raw)
			} else {
				_, dsn = testdb.OpenPostgresWithDSN(t)
			}
			newRuntime := func() *appenv.Runtime {
				router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: dialect, DSN: dsn, Writable: true, Readable: true, MaxOpenConns: 4}})
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, router.Close()) })
				return appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix("rt_latency_"))
			}
			writerRT, readerRT := newRuntime(), newRuntime()
			writer := events.NewModule(writerRT)
			readerEvents := events.NewModule(readerRT)
			locks := dlock.NewModule(writerRT)
			raw, err := writerRT.Database().Write().DB()
			require.NoError(t, err)
			for _, source := range []goosex.Source{locks.MigrationSource(), writer.MigrationSource()} {
				source.Prefix = "rt_latency_"
				require.NoError(t, goosex.Migrate(t.Context(), raw, source, dialect))
			}
			ids, err := guid.New(1)
			require.NoError(t, err)
			authority := realtimeLatencyAuthority{}
			stream := realtime.NewModule(readerRT, realtime.Config{})
			services := module.NewServices()
			require.NoError(t, module.Provide[guid.Source](services, ids))
			require.NoError(t, module.Provide[*authz.Registrar](services, authz.NewRegistrar(authz.MustRegistry(), authority, authority, authority)))
			require.NoError(t, locks.ProvideServices(services))
			require.NoError(t, readerEvents.ProvideServices(services))
			require.NoError(t, stream.ProvideServices(services))
			require.NoError(t, services.Seal())
			for _, eventModule := range []*events.Module{writer, readerEvents} {
				require.NoError(t, eventModule.InjectServices(services))
				require.NoError(t, eventModule.AddTypes("test", events.TypeSpec{Name: "project.changed", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeFull}))
				require.NoError(t, eventModule.Initialize(t.Context()))
			}
			require.NoError(t, stream.InjectServices(services))
			topics, err := module.Resolve[realtime.TopicAuthorizers](services)
			require.NoError(t, err)
			require.NoError(t, topics.Register("project", func(context.Context, guid.ID, guid.ID, realtime.Topic) (bool, error) { return true, nil }))
			require.NoError(t, stream.Initialize(t.Context()))
			require.NoError(t, writer.Start(t.Context()))
			t.Cleanup(func() { require.NoError(t, writer.Quiesce(context.Background())) })
			require.NoError(t, stream.Start(t.Context()))
			t.Cleanup(func() { require.NoError(t, stream.Quiesce(context.Background())) })
			// Server B owns writes; server A owns SSE and the browser page.
			writes := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				sample, e := strconv.Atoi(r.URL.Query().Get("sample"))
				if e != nil {
					http.Error(w, "sample", 400)
					return
				}
				started := time.Now().UnixMilli()
				e = writerRT.Database().Write().Transaction(func(tx *gorm.DB) error {
					_, e := writer.Publish(r.Context(), tx, events.Event{TenantID: 1, ProjectID: 2, Type: "project.changed", Actor: events.Actor{Kind: "SYSTEM"}, Subject: events.Subject{Kind: "project", ID: 2}, Data: map[string]any{"sample": sample, "started_ms": started}})
					return e
				})
				if e != nil {
					http.Error(w, e.Error(), 500)
					return
				}
				w.WriteHeader(204)
			}))
			defer writes.Close()
			mux := http.NewServeMux()
			require.NoError(t, stream.RegisterHTTP(t.Context(), humago.New(mux, huma.DefaultConfig("test", "1"))))
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprintf(w, `<html><body><output id="rendered"></output><script>
window.results=[];window.failure='';
(async()=>{const response=await fetch('/tenants/1/event-stream?topic=tenant/1/project/2',{headers:{Authorization:'Bearer test'}});if(!response.ok)throw Error('SSE '+response.status);const reader=response.body.getReader(),decoder=new TextDecoder();let pending='',ready=false,expected=0;
async function next(){const r=await fetch(%q+'?sample='+expected);if(!r.ok)throw Error('write '+r.status)}
for(;;){const {value,done}=await reader.read();if(done)throw Error('early EOF');pending+=decoder.decode(value,{stream:true});let at;while((at=pending.indexOf('\n\n'))>=0){const frame=pending.slice(0,at);pending=pending.slice(at+2);if(frame.includes('event: ready')&&!ready){ready=true;await next()}if(!frame.includes('event: project.changed'))continue;const body=JSON.parse(frame.split('\n').find(s=>s.startsWith('data: ')).slice(6));const data=body.data;if(data.sample!==expected)throw Error('missing/duplicate sample '+expected);document.getElementById('rendered').textContent=String(data.sample);window.results.push(Date.now()-data.started_ms);expected++;if(expected===200){await reader.cancel();return}await next()}}
})().catch(e=>{window.failure=String(e)});
</script></body></html>`, writes.URL)
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			webPackage, err := filepath.Abs("../../../frontend-web/apps/web/package.json")
			require.NoError(t, err)
			// Use the repository's installed browser test dependency; do not install
			// a second automation package or silently skip missing browser coverage.
			program := fmt.Sprintf(`const {createRequire}=require('node:module');const req=createRequire(%q);const {chromium}=req('playwright');const fs=require('node:fs');(async()=>{const paths=['C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe','C:/Program Files/Google/Chrome/Application/chrome.exe'];const browser=await chromium.launch({headless:true,executablePath:paths.find(p=>fs.existsSync(p))});try{const page=await browser.newPage();await page.goto(%q);try{await page.waitForFunction(()=>window.failure||window.results.length===200,{},{timeout:180000})}catch(error){console.error(JSON.stringify(await page.evaluate(()=>({url:location.href,samples:window.results,error:window.failure,rendered:document.getElementById('rendered')?.textContent}))));throw error}const result=await page.evaluate(()=>({samples:window.results,error:window.failure}));console.log(JSON.stringify(result));if(result.error)process.exitCode=1}finally{await browser.close()}})().catch(e=>{console.error(e);process.exitCode=1})`, webPackage, server.URL)
			command := exec.CommandContext(t.Context(), "node", "-e", program)
			command.SysProcAttr = &syscall.SysProcAttr{}
			if field := reflect.ValueOf(command.SysProcAttr).Elem().FieldByName("HideWindow"); field.IsValid() && field.CanSet() {
				field.SetBool(true)
			}
			output, err := command.CombinedOutput()
			require.NoError(t, err, string(output))
			var result struct {
				Samples []int64 `json:"samples"`
				Error   string  `json:"error"`
			}
			require.NoError(t, json.Unmarshal(output, &result), string(output))
			require.Empty(t, result.Error)
			require.Len(t, result.Samples, 200)
			sort.Slice(result.Samples, func(i, j int) bool { return result.Samples[i] < result.Samples[j] })
			limit := int64(700)
			if dialect == "postgres" {
				limit = 300
			}
			t.Logf("%s actual cross-replica commit-to-browser: samples=200 P95=%dms max=%dms", dialect, result.Samples[189], result.Samples[199])
			require.LessOrEqual(t, result.Samples[189], limit)
		})
	}
}
