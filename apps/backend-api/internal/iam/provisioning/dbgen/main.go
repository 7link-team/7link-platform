// Command dbgen generates provisioning's typed query package.
package main

import (
	"7link.com/platform/internal/iam/provisioning/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.Directory{}, model.Credential{}, model.Resource{}, model.Target{}, model.TargetResource{})
}
