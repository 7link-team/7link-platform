// Command dbgen generates governance's typed query package.
package main

import (
	"7link.com/platform/internal/iam/governance/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.AccessRequest{}, model.ApprovalStep{}, model.AccessReview{}, model.AccessReviewItem{})
}
