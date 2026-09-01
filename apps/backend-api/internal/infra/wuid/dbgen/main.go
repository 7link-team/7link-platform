// Command dbgen generates wuid's typed query package. It is invoked by the
// generate directive in the wuid package, so it inherits that package's
// directory as its working directory.
package main

import (
	"7link.com/platform/internal/infra/wuid/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() { gengorm.Generate(model.WorkerRow{}) }
