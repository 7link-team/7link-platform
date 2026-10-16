package idempotency

import (
	"crypto/sha256"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestHashRequestPreservesTypedOrderExactNumbersAndRetryCursor(t *testing.T) {
	type Request struct {
		Amount int64          `json:"amount"`
		Seen   int64          `json:"seen_seq"`
		Nested map[string]int `json:"nested"`
		Name   string         `json:"name"`
	}
	body := Request{Amount: 9007199254740993, Seen: 1, Nested: map[string]int{"seen_seq": 2}, Name: "项目"}
	got, err := HashRequest("POST", "/projects/{id}", map[string]string{"id": "9"}, body)
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256([]byte("POST\n/projects/{id}\n{\"id\":\"9\"}\n{\"amount\":9007199254740993,\"nested\":{\"seen_seq\":2},\"name\":\"项目\"}")), got)
	body.Seen = 55
	next, err := HashRequest("POST", "/projects/{id}", map[string]string{"id": "9"}, body)
	require.NoError(t, err)
	require.Equal(t, got, next)
	body.Nested["seen_seq"] = 3
	next, err = HashRequest("POST", "/projects/{id}", map[string]string{"id": "9"}, body)
	require.NoError(t, err)
	require.NotEqual(t, got, next)
	for _, body := range []any{nil, []int{1, 2}, "value", json.RawMessage(`{"seen_seq":12}`)} {
		_, err := HashRequest("POST", "/x", nil, body)
		require.NoError(t, err)
	}
	_, err = HashRequest("POST", "/x", nil, make(chan int))
	require.Error(t, err)
	for _, bad := range []string{`{`, `{"a":`, `{"a":1`, `{"a":1} true`} {
		_, err := withoutSeenSeq([]byte(bad))
		require.Error(t, err)
	}
}
