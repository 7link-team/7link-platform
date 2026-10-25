package control

import (
	"7link.com/runner/internal/executor"
	"context"
)

func runMerge(ctx context.Context, c *Client, f fence, p executor.MergePlan) error {
	o, err := executor.RunMerge(ctx, p)
	if err != nil {
		return err
	}
	return c.JSON(ctx, "POST", "/runner/merges/"+p.ID+"/receipts", struct {
		Fence       fence                     `json:"fence"`
		Nonce       string                    `json:"nonce"`
		Observation executor.MergeObservation `json:"observation"`
	}{f, p.Nonce, o}, nil, true)
}
