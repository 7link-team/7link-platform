// Package gen holds the shared mechanics for generating a module's typed query
// code. It exists so each module can own a three-line generator instead of a
// central registry that every new module has to be added to.
//
// It is build tooling: only a module's own dbgen command imports it.
package gen

import (
	"fmt"
	"path/filepath"
	"runtime"

	"gorm.io/gen"
)

// OutDir is where a module's generated query package is written, relative to
// the module directory containing its dbgen command.
const OutDir = "query"

// Generate writes the typed query package beside the calling module's dbgen
// directory, independently of the working directory used to run the command.
func Generate(models ...any) {
	_, source, _, ok := runtime.Caller(1)
	if !ok {
		panic("query generator: cannot locate calling dbgen command")
	}
	outPath, err := queryDir(source)
	if err != nil {
		panic(err)
	}
	g := gen.NewGenerator(gen.Config{
		OutPath: outPath,
		// WithoutContext is deliberately absent: every generated query takes the
		// caller's context, so cancellation and tracing reach the driver.
		Mode: gen.WithDefaultQuery | gen.WithQueryInterface,
	})
	g.ApplyBasic(models...)
	g.Execute()
}

func queryDir(source string) (string, error) {
	// A trimmed or unexpected caller path must fail rather than write into an
	// unrelated directory. Run generators without -trimpath to retain sources.
	if !filepath.IsAbs(source) || filepath.Base(filepath.Dir(source)) != "dbgen" {
		return "", fmt.Errorf("query generator: expected absolute dbgen source path, got %q", source)
	}
	return filepath.Join(filepath.Dir(filepath.Dir(source)), OutDir), nil
}
