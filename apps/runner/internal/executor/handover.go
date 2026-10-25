package executor

// HandoverPlan is issued only by the server's retirement command. It has no
// assignment, repository or source commit, and runs in a disposable directory.
type HandoverPlan struct {
	RetirementID     string `json:"retirement_id"`
	AgentPrincipalID string `json:"agent_principal_id"`
}

func validHandover(p Plan) bool {
	return p.Handover != nil && validFullAutoGrant(p.Handover.RetirementID) && validFullAutoGrant(p.Handover.AgentPrincipalID) && p.ExecutorPrincipalID == p.Handover.AgentPrincipalID && p.AuthorizationMode == "READ_ONLY" && p.BaseCommit == "" && p.Distillation == nil && p.QualityFeedback == "" && len(p.Artifacts) == 0 && len(p.Knowledge) == 0
}
