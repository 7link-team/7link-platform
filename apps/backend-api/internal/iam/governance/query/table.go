package query

import (
	"7link.com/platform/internal/iam/governance/model"
	"gorm.io/gorm"
)

type (
	AccessRequests    = accessRequest
	ApprovalSteps     = approvalStep
	AccessReviews     = accessReview
	AccessReviewItems = accessReviewItem
)

type Tables struct {
	AccessRequests    *AccessRequests
	ApprovalSteps     *ApprovalSteps
	AccessReviews     *AccessReviews
	AccessReviewItems *AccessReviewItems
}

func Bind(db *gorm.DB, prefix string) *Tables {
	q := Use(db)
	return &Tables{
		AccessRequests:    q.AccessRequest.Table(prefix + model.AccessRequest{}.TableName()),
		ApprovalSteps:     q.ApprovalStep.Table(prefix + model.ApprovalStep{}.TableName()),
		AccessReviews:     q.AccessReview.Table(prefix + model.AccessReview{}.TableName()),
		AccessReviewItems: q.AccessReviewItem.Table(prefix + model.AccessReviewItem{}.TableName()),
	}
}
