// Command dbgen generates dlock's typed query package. It is invoked by the
// generate directive in the dlock package, so it inherits that package's
// directory as its working directory.
package main

import (
	"7link.com/platform/internal/infra/dlock/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() { gengorm.Generate(model.LockRow{}) }
