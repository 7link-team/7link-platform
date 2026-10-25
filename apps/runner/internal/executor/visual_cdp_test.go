package executor

import (
	"context"
	"testing"
)

func TestVisualCDPRejectsRemoteOrNonWebsocketEndpoints(t *testing.T) {
	for _, endpoint := range []string{"", "http://127.0.0.1:1/", "ws://example.invalid:1/", "ws://user@127.0.0.1:1/"} {
		if c, e := dialVisualCDP(context.Background(), endpoint); e == nil {
			c.conn.Close()
			t.Fatal("remote endpoint accepted", endpoint)
		}
	}
}
