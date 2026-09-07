// Command dbgen generates audit's typed query package.
package main

import (
	"7link.com/platform/internal/iam/audit/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.Event{}, model.Head{}, model.RetentionPolicy{}, model.ArchiveJob{})
}
