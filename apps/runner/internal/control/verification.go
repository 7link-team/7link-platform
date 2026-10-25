package control

import (
	"7link.com/runner/internal/executor"
	"context"
)

// runVerification sends only observations from the platform command process.
// Generic runtime LOG/EVIDENCE/COMPLETED receipts never enter this protocol.
func runVerification(ctx context.Context, c *Client, f fence, p executor.VerificationPlan) error {
	o, err := executor.RunVerification(ctx, p)
	if err != nil {
		return err
	}
	return c.JSON(ctx, "POST", "/runner/verifications/"+p.ID+"/receipts", struct {
		Fence       fence                            `json:"fence"`
		Nonce       string                           `json:"nonce"`
		Observation executor.VerificationObservation `json:"observation"`
	}{f, p.Nonce, o}, nil, true)
}
