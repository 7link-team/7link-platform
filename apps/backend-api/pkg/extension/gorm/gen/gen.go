// Package gen holds the shared mechanics for generating a module's typed query
// code. It exists so each module can own a three-line generator instead of a
// central registry that every new module has to be added to.
//
// It is build tooling: only a module's own dbgen command imports it.
package gen

import "gorm.io/gen"

// OutDir is where a module's generated query package is written, relative to
// the module directory that runs `go generate`.
const OutDir = "query"

// Generate writes the typed query package for models into OutDir, resolved
// against the working directory. Generation runs a directive with the
// working directory set to the directory of the file that carries it, so a
// module placing its directive in its own package root generates into its own
// query package with no path arithmetic.
func Generate(models ...any) {
	g := gen.NewGenerator(gen.Config{
		OutPath: OutDir,
		// WithoutContext is deliberately absent: every generated query takes the
		// caller's context, so cancellation and tracing reach the driver.
		Mode: gen.WithDefaultQuery | gen.WithQueryInterface,
	})
	g.ApplyBasic(models...)
	g.Execute()
}
