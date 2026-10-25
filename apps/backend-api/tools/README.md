# Go tools

Tool binaries use a separate module so their transitive dependencies do not
enter the backend runtime module graph.

From `apps/backend-api`, regenerate protobuf code with:

```sh
go tool -modfile=tools/go.mod buf generate
```

Lint with the pinned linter (also wired up as `make lint`):

```sh
go tool -modfile=tools/go.mod golangci-lint run ./...
```

Pinning the linter here means every developer resolves the same rule set and
the same findings, with no local install required.

## Coverage aggregation

`coverage` uses only the Go standard library. `make coverage-check` runs its
regression tests, compacts the atomic profile, checks every registered module
floor in one scan, and retains `go tool cover` for the filtered total. Duplicate
block counts are summed rather than discarding hits from other test packages;
inconsistent statement counts, malformed records, overflow, missing modules
and failed output writes fail the gate. No runtime dependency is added.
