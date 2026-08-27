# Embedded OpenAPI documentation site

`web` is a React static-site generator for the API documentation. It emits a
standalone HTML file for the default Scalar view and each supported renderer:

- Scalar
- Swagger UI
- ReDoc
- RapiDoc
- Stoplight Elements
- openapi-ui

Run from `apps/backend-api`:

```sh
go generate ./pkg/extension/huma/docs
```

Generation checks the npm registry and only runs `npm install` when a package
has a newer version or local dependencies are missing. It then writes the site
to `site`, which the Go package embeds. Files whose content is unchanged are not
rewritten.

To rebuild from the currently locked dependencies without checking for
upgrades:

```sh
cd pkg/extension/huma/docs/web
npm ci
npm run build
```
