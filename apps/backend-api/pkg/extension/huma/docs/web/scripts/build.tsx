import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { build } from "esbuild";
import { mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outputRoot = resolve(webRoot, "../site");

const renderers = [
  { id: "swagger", label: "Swagger UI" },
  { id: "redoc", label: "ReDoc" },
  { id: "scalar", label: "Scalar" },
  { id: "stoplight", label: "Stoplight" },
  { id: "rapidoc", label: "RapiDoc" },
  { id: "openapi-ui", label: "openapi-ui" }
] as const;

const specPages = [
  { id: "openapi-json", label: "OpenAPI JSON" },
  { id: "openapi-yaml", label: "OpenAPI YAML" }
] as const;

const pages = [...renderers, ...specPages] as const;

const assets: Array<[string, string, ((data: Buffer) => Buffer)?]> = [
  ["@scalar/api-reference/dist/browser/standalone.js", "scalar.js"],
  ["swagger-ui-dist/swagger-ui.css", "swagger-ui.css"],
  ["swagger-ui-dist/swagger-ui-bundle.js", "swagger-ui-bundle.js"],
  ["redoc/bundles/redoc.standalone.js", "redoc.js"],
  ["rapidoc/dist/rapidoc-min.js", "rapidoc.js"],
  ["@stoplight/elements/styles.min.css", "stoplight.css"],
  ["@stoplight/elements/web-components.min.js", "stoplight.js", localizeStoplight],
  ["mermaid/dist/mermaid.min.js", "mermaid.min.js"],
  ["openapi-ui-dist/lib/openapi-ui.umd.js", "openapi-ui.js"]
];

await mkdir(join(outputRoot, "assets"), { recursive: true });
await copySourceAsset("app.css");
await bundleApp();

for (const [source, target, transform] of assets) {
  let data = await readFile(join(webRoot, "node_modules", source));
  if (transform) data = transform(data);
  await writeIfChanged(join(outputRoot, "assets", target), data);
}

for (const page of pages) {
  const html = "<!doctype html>" + renderToStaticMarkup(<Document page={page.id} />);
  await writeIfChanged(join(outputRoot, page.id, "index.html"), Buffer.from(html));
  if (page.id === "swagger") {
    await writeIfChanged(join(outputRoot, "index.html"), Buffer.from(html));
  }
}

function Document({ page }: { page: (typeof pages)[number]["id"] }) {
  const active = pages.find((item) => item.id === page)!;
  return (
    <html lang="en">
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <meta name="color-scheme" content="light" />
        <title>{`${active.label} - API Documentation`}</title>
        <link rel="stylesheet" href="/docs/assets/app.css" />
        <script src="/docs/config.js"></script>
        <script defer src="/docs/assets/app.js"></script>
      </head>
      <body data-renderer={page}>
        <a className="skip-link" href="#renderer-root">Skip to API documentation</a>
        <header className="app-header">
          <a className="brand" href="/docs/" aria-label="API documentation home">
            <span className="brand-mark" aria-hidden="true">7</span>
            <span><strong data-api-name>API</strong><small>Documentation</small></span>
          </a>
          <nav className="renderer-tabs" aria-label="Documentation renderer">
            {renderers.map((item) => (
              <a
                key={item.id}
                href={`/docs/${item.id}/`}
                aria-current={item.id === page ? "page" : undefined}
              >
                {item.label}
              </a>
            ))}
          </nav>
          <nav className="resource-links" aria-label="API resources">
            <a href="/docs/openapi-json/" aria-current={page === "openapi-json" ? "page" : undefined}>JSON</a>
            <a href="/docs/openapi-yaml/" aria-current={page === "openapi-yaml" ? "page" : undefined}>YAML</a>
          </nav>
        </header>
        <main id="renderer-root" className={`renderer renderer-${page}`} aria-live="polite">
          <div className="loading" role="status">Loading API definition...</div>
        </main>
      </body>
    </html>
  );
}

async function copySourceAsset(name: string) {
  await writeIfChanged(join(outputRoot, "assets", name), await readFile(join(webRoot, "src", name)));
}

async function bundleApp() {
  const result = await build({
    entryPoints: [join(webRoot, "src/app.js")],
    bundle: true,
    format: "iife",
    minify: true,
    platform: "browser",
    write: false
  });
  await writeIfChanged(join(outputRoot, "assets/app.js"), result.outputFiles[0].contents);
}

function localizeStoplight(data: Buffer) {
  const source = data.toString();
  const remote = /https:\/\/unpkg\.com\/mermaid@[^/]+\/dist\/mermaid\.min\.js/g;
  if (!remote.test(source)) {
    throw new Error("Stoplight bundle no longer contains the expected Mermaid URL");
  }
  return Buffer.from(source.replaceAll(remote, "/docs/assets/mermaid.min.js"));
}

async function writeIfChanged(target: string, data: Buffer) {
  await mkdir(dirname(target), { recursive: true });
  try {
    const current = await readFile(target);
    if (current.equals(data)) return;
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
  }

  const temporary = `${target}.tmp`;
  await writeFile(temporary, data);
  try {
    await rename(temporary, target);
  } finally {
    await rm(temporary, { force: true });
  }
}
