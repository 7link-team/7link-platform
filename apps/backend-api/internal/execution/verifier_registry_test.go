package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func verifierRegistryFixture(t *testing.T) *executionFixture {
	f := executionHarness(t)
	f.m.verifierRegistry.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.Contains(t, []string{"verifier_manage", "policy_read"}, r.Permission)
		return authz.Explanation{Allowed: f.authorized}, nil
	}
	idem := idempotency.NewModule(f.m.runtime)
	raw, err := f.db.DB()
	require.NoError(t, err)
	source := idem.MigrationSource()
	source.Prefix = "ex_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, f.m.ids))
	services.Seal()
	require.NoError(t, idem.InjectServices(services))
	f.m.commentIdempotency = idem
	return f
}
func registryDefinition() TenantVerifierDefinition {
	return TenantVerifierDefinition{Key: "unit-tests", Name: "Unit tests", Layer: "AUTOMATED", State: "ACTIVE", Required: true, AppliesTo: VerifierPolicy{WorkItemTypes: []string{"BUG", "TEST"}, RiskLevels: []string{"HIGH"}, ProjectIDs: []guid.ID{9}}, Spec: VerifierSpec{Argv: []string{"go", "test", "./..."}, ReportFormat: "NONE", StdoutTailBytes: 8192}, TimeoutMS: 600000, ChangeNote: "Initial approved command"}
}
func putRegistry(t *testing.T, f *executionFixture, id guid.ID, version int, in TenantVerifierDefinition) VerifierView {
	t.Helper()
	var out VerifierView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = f.m.putVerifierOn(f.human, tx, 7, id, version, in)
		return err
	}))
	return out
}
func TestVerifierRegistryVersionsMetadataSelectionArchiveAndRollback(t *testing.T) {
	f := verifierRegistryFixture(t)
	in := registryDefinition()
	first := putRegistry(t, f, 0, 0, in)
	require.Positive(t, first.ID)
	require.Positive(t, first.Current.ID)
	in.Name = "Renamed tests"
	in.Required = false
	meta := putRegistry(t, f, first.ID, 1, in)
	require.Equal(t, 1, meta.CurrentVersionNo)
	require.Equal(t, first.Current.ID, meta.Current.ID)
	require.Equal(t, 2, meta.Version)
	in.Spec.Argv = []string{"go", "test", "-count=1", "./..."}
	in.TimeoutMS = 120000
	in.ChangeNote = "Reproducible test invocation"
	next := putRegistry(t, f, first.ID, 2, in)
	require.Equal(t, 2, next.CurrentVersionNo)
	require.NotEqual(t, first.Current.ID, next.Current.ID)
	var selected []VerifierView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		selected, err = f.m.selectVerifiersOn(f.human, tx, 7, 9, "BUG", "HIGH")
		return err
	}))
	require.Len(t, selected, 1)
	require.Equal(t, next.Current, selected[0].Current)
	for _, miss := range []struct {
		tenant, project guid.ID
		kind, risk      string
	}{{8, 9, "BUG", "HIGH"}, {7, 10, "BUG", "HIGH"}, {7, 9, "FEATURE", "HIGH"}, {7, 9, "BUG", "LOW"}} {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			v, err := f.m.selectVerifiersOn(f.human, tx, miss.tenant, miss.project, miss.kind, miss.risk)
			require.Empty(t, v)
			return err
		}))
	}
	var historical VerifierVersionView
	page, err := f.m.verifierVersionsHTTP(f.human, &verifierVersionsInput{VerifierIDInput: VerifierIDInput{VerifierID: first.ID.String()}, Limit: 50})
	require.NoError(t, err)
	require.Len(t, page.Body.Data.Items, 2)
	historical = page.Body.Data.Items[0]
	require.Equal(t, first.Current, historical)
	before := next
	injected := errors.New("outer rollback")
	in.TimeoutMS = 3000
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.putVerifierOn(f.human, tx, 7, first.ID, 3, in)
		if err != nil {
			return err
		}
		return injected
	}), injected)
	got, err := f.m.getVerifierHTTP(f.human, &VerifierIDInput{VerifierID: first.ID.String()})
	require.NoError(t, err)
	require.Equal(t, before, got.Body.Data)
	in.TimeoutMS = 120000
	in.State = "ARCHIVED"
	archived := putRegistry(t, f, first.ID, 3, in)
	require.Equal(t, 2, archived.CurrentVersionNo)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		v, err := f.m.selectVerifiersOn(f.human, tx, 7, 9, "BUG", "HIGH")
		require.Empty(t, v)
		return err
	}))
	in.State = "ACTIVE"
	active := putRegistry(t, f, first.ID, 4, in)
	require.Equal(t, 2, active.CurrentVersionNo)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.putVerifierOn(f.human, tx, 7, first.ID, 4, in); return err }), ErrConflict)
	f.authorized = false
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.putVerifierOn(f.human, tx, 7, first.ID, 5, in); return err }), ErrForbidden)
	_, err = f.m.getVerifierHTTP(f.human, &VerifierIDInput{VerifierID: first.ID.String()})
	require.Error(t, err)
}
func TestVerifierRegistryRejectsInvalidDefinitionsWithoutEffects(t *testing.T) {
	for _, fault := range []string{"key", "layer", "state", "type", "risk", "project", "timeout", "name", "argv", "workdir", "env", "report", "tail", "mixed", "nul", "note"} {
		t.Run(fault, func(t *testing.T) {
			f := verifierRegistryFixture(t)
			in := registryDefinition()
			switch fault {
			case "key":
				in.Key = "../escape"
			case "layer":
				in.Layer = "UNKNOWN"
			case "state":
				in.State = "DELETED"
			case "type":
				in.AppliesTo.WorkItemTypes = []string{"UNKNOWN"}
			case "risk":
				in.AppliesTo.RiskLevels = []string{"unknown"}
			case "project":
				in.AppliesTo.ProjectIDs = []guid.ID{-1}
			case "timeout":
				in.TimeoutMS = 999
			case "name":
				in.Name = "\n"
			case "argv":
				in.Spec.Argv = nil
			case "workdir":
				in.Spec.Workdir = "../escape"
			case "env":
				in.Spec.Env = map[string]string{"bad-key": "x"}
			case "report":
				in.Spec.ReportGlobs = []string{"/absolute"}
			case "tail":
				in.Spec.StdoutTailBytes = 100
			case "mixed":
				in.Spec.AgentID = 12
			case "nul":
				in.Spec.Argv = []string{"go\x00"}
			case "note":
				in.ChangeNote = strings.Repeat("x", 2001)
			}
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.putVerifierOn(f.human, tx, 7, 0, 0, in); return err }), ErrInvalidArgument)
			for _, table := range []string{f.m.names.Verifiers.String(), f.m.names.VerifierVersions.String(), "effects"} {
				var n int64
				require.NoError(t, f.db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
	for _, layer := range []string{"HUMAN", "AI_ASSISTED"} {
		t.Run(layer, func(t *testing.T) {
			f := verifierRegistryFixture(t)
			in := registryDefinition()
			in.Layer = layer
			if layer == "HUMAN" {
				in.Spec = VerifierSpec{Instructions: "Inspect the actual result", EligibleRoleKeys: []string{"reviewer"}}
			} else {
				in.Spec = VerifierSpec{AgentID: 21, PromptTemplate: "Review the exact commit", MaxCostMicros: 1000}
			}
			v := putRegistry(t, f, 0, 0, in)
			b, err := json.Marshal(v.Current.Spec)
			require.NoError(t, err)
			require.NotContains(t, string(b), "argv")
		})
	}
}

func TestVerifierRegistryAuthorizationAndEffectFailure(t *testing.T) {
	for _, fault := range []string{"inactive", "tenant", "actor", "policy", "audit", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			f := verifierRegistryFixture(t)
			switch fault {
			case "inactive":
				f.humanActive = false
			case "tenant":
				f.human = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 8, PrincipalID: 11})
			case "actor":
				f.human = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7})
			case "policy":
				f.authorized = false
			case "audit":
				f.m.audit = nil
			case "outbox":
				f.pub.fail = true
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, err := f.m.putVerifierOn(f.human, tx, 7, 0, 0, registryDefinition())
				return err
			}))
			for _, table := range []string{f.m.names.Verifiers.String(), f.m.names.VerifierVersions.String(), "effects"} {
				var n int64
				require.NoError(t, f.db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
}

func TestVerifierRegistryEveryStorageFailurePreservesImmutableVersionHistory(t *testing.T) {
	for _, command := range []string{"create", "metadata", "version", "select"} {
		for _, kind := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				f := verifierRegistryFixture(t)
				in := registryDefinition()
				var existing VerifierView
				if command != "create" {
					existing = putRegistry(t, f, 0, 0, in)
				}
				if command == "metadata" {
					in.Name = "Updated registry name"
				}
				if command == "version" {
					in.Spec.Argv = []string{"go", "test", "-count=1", "./..."}
				}
				snapshot := func() map[string][]map[string]any {
					out := map[string][]map[string]any{}
					for _, table := range []string{f.m.names.Verifiers.String(), f.m.names.VerifierVersions.String(), "effects"} {
						var rows []map[string]any
						require.NoError(t, f.db.Table(table).Order("rowid").Find(&rows).Error)
						out[table] = rows
					}
					return out
				}
				before := snapshot()
				count := 0
				injected := errors.New("verifier storage unavailable")
				hook := func(tx *gorm.DB) {
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch kind {
				case "query":
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("verifier_transaction_failure", hook))
				case "create":
					require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("verifier_transaction_failure", hook))
				case "update":
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("verifier_transaction_failure", hook))
				}
				err := f.db.Transaction(func(tx *gorm.DB) error {
					if command == "select" {
						_, e := f.m.selectVerifiersOn(f.human, tx, 7, 9, "BUG", "HIGH")
						return e
					}
					_, e := f.m.putVerifierOn(f.human, tx, 7, existing.ID, existing.Version, in)
					return e
				})
				require.NoError(t, f.db.Callback().Query().Remove("verifier_transaction_failure"))
				require.NoError(t, f.db.Callback().Create().Remove("verifier_transaction_failure"))
				require.NoError(t, f.db.Callback().Update().Remove("verifier_transaction_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				require.Equal(t, before, snapshot())
				return count
			}
			total := 0
			t.Run(command+"/"+kind+"/success", func(t *testing.T) { total = exercise(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(fmt.Sprintf("%s/%s/failure_%d", command, kind, at), func(t *testing.T) { require.GreaterOrEqual(t, exercise(t, at), at) })
			}
		}
	}
}

func TestBindVerifierRegistryRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindVerifierRegistry(module.NewServices()), module.ErrServicesProviding)
}
