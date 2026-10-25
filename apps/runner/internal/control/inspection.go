package control

import (
	"7link.com/runner/internal/inspection"
	"context"
)

type inspectionRequest struct {
	WorkdirPath string `json:"workdir_path"`
	ID          string `json:"id"`
	MachineID   string `json:"machine_id"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
}

// A version/path scan is sufficient only for RUNTIMES. It must never claim
// model catalogs, runtime authentication, Skills or MCP were inspected.
func completeInspection(ctx context.Context, c *Client, p Profile, f fence, request inspectionRequest, scan func(context.Context) inspection.Report) error {
	result := map[string]any{"session_id": f.SessionID, "conn_epoch": f.Epoch, "succeeded": false, "reason_code": "UNSUPPORTED_INSPECTION_KIND"}
	if request.Kind == "RUNTIMES" {
		report := scan(ctx)
		inventory, err := inventoryFromReport(report)
		if err != nil || ctx.Err() != nil {
			result["reason_code"] = "INSPECTION_FAILED"
		} else {
			result["succeeded"] = true
			result["reason_code"] = ""
			result["inventory"] = inventory
		}
	}
	if request.Kind == "MODELS" || request.Kind == "CAPABILITIES" {
		result["succeeded"] = true
		result["reason_code"] = ""
		result["discovery"] = inspection.DiscoverScope(request.Kind, request.WorkdirPath)
	}
	if request.ID == "" {
		result["kind"] = request.Kind
		delete(result, "succeeded")
		delete(result, "reason_code")
		return c.JSON(ctx, "PUT", "/machines/"+p.MachineID+"/discovery", result, nil, true)
	}
	return c.JSON(ctx, "PUT", "/machines/"+p.MachineID+"/inspections/"+request.ID+"/result", result, nil, true)
}
