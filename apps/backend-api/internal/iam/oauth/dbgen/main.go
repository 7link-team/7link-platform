// Command dbgen generates OAuth's typed query package.
package main

import (
	"7link.com/platform/internal/iam/oauth/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(
		model.Client{},
		model.AuthorizationCode{},
		model.Consent{},
		model.ConsentRequest{},
		model.RefreshToken{},
	)
}
