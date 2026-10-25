package governance

// Requirement decisions share the current direct Human decision grants. Their
// content/version/review guards belong to delivery, not work-item evidence facts.
func requirementReviewAuthorityRequest(req AuthorityRequest) bool {
	return req.Resource.Kind == "requirement" && (req.Code == "decision_approve" || req.Code == "decision_request_changes")
}
