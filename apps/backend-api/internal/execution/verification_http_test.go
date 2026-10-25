package execution

import (
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestVerificationReceiptHTTPCompletesOnlyActualLeasedObservation(t *testing.T) {
	f, request := verificationFixture(t)
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = f.m.queueVerificationOn(f.human, tx, request); return e }))
	var plans []VerificationPlan
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		plans, e = f.m.pullVerificationOn(f.runner, tx, 7, f.fence)
		return e
	}))
	b, _ := json.Marshal(plans[0])
	code := 0
	in := &verificationReceiptInput{TaskID: id.String()}
	in.Body.Fence = f.fence
	in.Body.Nonce = plans[0].Nonce
	in.Body.Observation = VerificationObservation{PlanHash: hash(b), HeadCommit: request.HeadCommit, ExitCode: &code, Reason: "PROCESS_EXITED", OutputSHA256: hash(nil), StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()}
	_, err := f.m.verificationCompleteHTTP(f.runner, in)
	require.NoError(t, err)
	in.TaskID = "not-an-id"
	_, err = f.m.verificationCompleteHTTP(f.runner, in)
	require.Error(t, err)
	in.TaskID = id.String()
	f.m.machines.BindRunnerContext = func(context.Context, guid.ID) (context.Context, error) { return nil, ErrForbidden }
	_, err = f.m.verificationPullHTTP(f.runner, &verificationPullInput{Body: f.fence})
	require.Error(t, err)
	_, err = f.m.verificationCompleteHTTP(f.runner, in)
	require.Error(t, err)
	f.m.machines.BindRunnerContext = nil
	_, err = f.m.verificationPullHTTP(f.runner, &verificationPullInput{Body: f.fence})
	require.Error(t, err)
	_, err = f.m.verificationCompleteHTTP(f.runner, in)
	require.Error(t, err)
}

func TestVerificationHTTPUnavailableWriterCannotLeaseOrComplete(t *testing.T) {
	f := executionHarness(t)
	f.m.runtime = nil
	_, err := f.m.verificationPullHTTP(f.runner, &verificationPullInput{Body: f.fence})
	require.Error(t, err)
	in := &verificationReceiptInput{TaskID: "1"}
	in.Body.Fence = f.fence
	_, err = f.m.verificationCompleteHTTP(f.runner, in)
	require.Error(t, err)
}

func TestVerificationHTTPRequiresAuthenticatedMachineSession(t *testing.T) {
	f := executionHarness(t)
	_, err := f.m.verificationPullHTTP(f.human, &verificationPullInput{Body: f.fence})
	require.Error(t, err)
	_, err = f.m.verificationPullHTTP(f.runner, &verificationPullInput{Body: f.fence})
	require.NoError(t, err)
	f.sessionValid = false
	_, err = f.m.verificationPullHTTP(f.runner, &verificationPullInput{Body: f.fence})
	require.Error(t, err)
	input := &verificationReceiptInput{TaskID: "1"}
	input.Body.Fence = f.fence
	input.Body.Nonce = "forged"
	_, err = f.m.verificationCompleteHTTP(f.human, input)
	require.Error(t, err)
}
