# Backend integration tests

Tests that need database engines or other heavy test-only dependencies live in
this separate module so they do not enter the backend runtime dependency graph.
Fast unit and lightweight module-local SQLite tests stay next to their source
packages; shared migration integration fixtures live here.

The suite uses embedded PostgreSQL 16, file-backed SQLite, and the in-process
`go-mysql-server` compatibility engine. `go-mysql-server` is not MySQL 8 and
does not replace the separately pinned real-MySQL gate.

Run every isolated test from `apps/backend-api` with:

```sh
go -C tests test ./...
```
