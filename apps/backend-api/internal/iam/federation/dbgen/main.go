// Command dbgen generates federation's typed query package.
package main

import (
	"7link.com/platform/internal/iam/federation/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.Provider{}, model.IdentityLink{}, model.SAMLServiceProvider{}, model.SAMLIDPKey{}, model.ReplayClaim{})
}
