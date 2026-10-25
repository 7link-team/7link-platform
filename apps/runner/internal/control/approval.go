package control

import (
	"7link.com/runner/internal/executor"
	"context"
	"errors"
	"time"
)

func runtimePermissionHandler(c *Client, f fence, id string, epoch int64) executor.PermissionHandler {
	return func(ctx context.Context, req executor.PermissionRequest) (executor.PermissionDecision, error) {
		var view struct {
			ID        string    `json:"id"`
			State     string    `json:"state"`
			Reason    string    `json:"reason"`
			ExpiresAt time.Time `json:"expires_at"`
		}
		path := "/executions/" + id + "/approval-requests"
		err := c.JSON(ctx, "POST", path, map[string]any{"fence": f, "request": map[string]any{"dispatch_epoch": epoch, "request_id": req.RequestID, "tool": req.Tool, "input": req.Input}}, &view, true)
		if err != nil {
			return executor.PermissionDecision{}, err
		}
		if !validID(view.ID) {
			return executor.PermissionDecision{}, errors.New("invalid permission request acknowledgement")
		}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			switch view.State {
			case "ALLOW", "DENY":
				return executor.PermissionDecision{State: view.State, Reason: view.Reason}, nil
			case "EXPIRED":
				return executor.PermissionDecision{State: "DENY", Reason: "Human permission request expired"}, nil
			case "PENDING":
			default:
				return executor.PermissionDecision{}, errors.New("unknown permission decision")
			}
			if view.ExpiresAt.IsZero() || !view.ExpiresAt.After(time.Now()) {
				return executor.PermissionDecision{State: "DENY", Reason: "Human permission request expired"}, nil
			}
			select {
			case <-ctx.Done():
				return executor.PermissionDecision{}, ctx.Err()
			case <-ticker.C:
			}
			if err = c.JSON(ctx, "POST", path+"/"+view.ID+"/polls", map[string]any{"fence": f, "dispatch_epoch": epoch}, &view, true); err != nil {
				return executor.PermissionDecision{}, err
			}
		}
	}
}
