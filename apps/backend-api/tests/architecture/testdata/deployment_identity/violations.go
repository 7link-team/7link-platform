// Package deploymentidentity is the deployment-identity gate's fixture. Go
// tooling ignores testdata, so this file is parsed but never built.
package deploymentidentity

import _ "7link.com/platform/pkg/config"

const module = "7link.com/platform/internal/app"

var (
	banner   = "SEVEN LINK"
	audience = "7link-api"
	key      = "sevenlink_session"
)
