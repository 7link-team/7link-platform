# Go tools

Tool binaries use a separate module so their transitive dependencies do not
enter the backend runtime module graph.

From `apps/backend-api`, regenerate protobuf code with:

```sh
go tool -modfile=tools/go.mod buf generate
```
