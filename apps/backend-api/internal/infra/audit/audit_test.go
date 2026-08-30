package audit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEntryKindConstants(t *testing.T) {
	assert.Equal(t, "http", string(KindHTTP))
	assert.Equal(t, "grpc", string(KindGRPC))
	assert.Equal(t, "stream", string(KindStream))
	assert.Equal(t, "job", string(KindJob))
	assert.Equal(t, "schedule", string(KindSchedule))
	assert.Equal(t, "message", string(KindMessage))
	assert.Equal(t, "internal", string(KindInternal))
}

func TestAccessAndOutcomeConstants(t *testing.T) {
	assert.Equal(t, "read", string(AccessRead))
	assert.Equal(t, "write", string(AccessWrite))
	assert.Equal(t, "success", string(OutcomeSuccess))
	assert.Equal(t, "failure", string(OutcomeFailure))
	assert.Equal(t, "anonymous", string(ActorAnonymous))
	assert.Equal(t, "success_summary", string(OmittedSuccessSummary))
}
