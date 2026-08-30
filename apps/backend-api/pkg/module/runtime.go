package module

// Runtime is the immutable app-level environment shared by modules. The app
// builds it once; modules that need the environment receive it as a constructor
// parameter and retain only the narrow capabilities they use. Passed by value;
// never mutated after construction.
//
// DB is the app's datasource router as an opaque value: pkg/module cannot import
// the gorm extension (pkg/extension/gorm's own tests construct telemetry, which
// imports pkg/module, so a gorm import here would be an import cycle). Each
// database-consuming module narrows rt.DB to the capability it needs with a
// type assertion in its constructor:
//
//	var db gormx.Writer
//	if router, ok := rt.DB.(*gormx.DatasourceRouter); ok {
//		db = router
//	}
//
// Redis, Cache, ... are added only when a real module needs one.
type Runtime struct {
	// Name and Version are the application identity, used by telemetry
	// (OTel resource), audit (recorder service identity), and pprof (UI title).
	Name    string
	Version string

	// DB is the datasource router (*gormx.DatasourceRouter). It serves every
	// database consumer: Write (dlock / wuid / audit), Probe/DatasourceStats/
	// DatasourceNames (database-monitor), DatasourceStats (metrics),
	// RegisterQueryObserver.
	DB any
}
