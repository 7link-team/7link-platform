// Command dbgen generates identity's typed query package.
package main

import (
	"7link.com/platform/internal/iam/identity/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(
		model.Principal{},
		model.PrincipalLifecycle{},
		model.ServiceAccount{},
		model.ServiceAccountCredential{},
	)
}
