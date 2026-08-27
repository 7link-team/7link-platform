# Backend integration tests

Tests that need database engines or other heavy test-only dependencies live in
this separate module so they do not enter the backend runtime dependency graph.
Fast unit and SQLite tests stay next to their source packages.

Run every isolated test from `apps/backend-api` with:

```sh
go -C tests test ./...
```
