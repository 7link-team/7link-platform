package query

import (
	"7link.com/platform/internal/iam/governance/model"
	gormx "7link.com/platform/pkg/extension/gorm"
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

// TableNames is the physical name of every table governance owns under one
// deployment prefix.
type TableNames struct {
	AccessRequests    gormx.TableName
	ApprovalSteps     gormx.TableName
	AccessReviews     gormx.TableName
	AccessReviewItems gormx.TableName
}

// BindNames returns the physical name of every table governance owns. It is the
// only place the deployment prefix is applied to one of them, so a deployment
// cannot end up with half of governance's tables prefixed.
func BindNames(prefix string) TableNames {
	return TableNames{
		AccessRequests:    gormx.NewTableName(prefix, model.AccessRequest{}.TableName()),
		ApprovalSteps:     gormx.NewTableName(prefix, model.ApprovalStep{}.TableName()),
		AccessReviews:     gormx.NewTableName(prefix, model.AccessReview{}.TableName()),
		AccessReviewItems: gormx.NewTableName(prefix, model.AccessReviewItem{}.TableName()),
	}
}

// Bind applies prefix to every table owned by the governance module. It builds
// from BindNames, so the prefix reaches the generated query objects through one
// path rather than four.
func Bind(db *gorm.DB, prefix string) *Tables {
	names := BindNames(prefix)
	q := Use(db)
	return &Tables{
		AccessRequests:    q.AccessRequest.Table(names.AccessRequests.String()),
		ApprovalSteps:     q.ApprovalStep.Table(names.ApprovalSteps.String()),
		AccessReviews:     q.AccessReview.Table(names.AccessReviews.String()),
		AccessReviewItems: q.AccessReviewItem.Table(names.AccessReviewItems.String()),
	}
}
