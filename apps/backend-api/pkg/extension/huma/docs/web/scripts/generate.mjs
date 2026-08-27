import { readFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const packages = [
  "@scalar/api-reference",
  "@stoplight/elements",
  "esbuild",
  "openapi-ui-dist",
  "rapidoc",
  "react",
  "react-dom",
  "redoc",
  "swagger-ui-dist",
  "tsx",
  "yaml"
];

let packageJSON = await readJSON(join(webRoot, "package.json"));
const latest = new Map(
  await Promise.all(packages.map(async (name) => [name, await latestVersion(name)]))
);
const upgrades = packages.filter(
  (name) => packageJSON.devDependencies[name] !== latest.get(name)
);
const dependenciesMissing = await anyDependencyMissing(packageJSON.devDependencies);

if (upgrades.length > 0) {
  runNPM([
    "install",
    "--save-dev",
    "--save-exact",
    "--ignore-scripts",
    ...upgrades.map((name) => `${name}@${latest.get(name)}`)
  ]);
  packageJSON = await readJSON(join(webRoot, "package.json"));
} else if (dependenciesMissing) {
  runNPM(["install", "--ignore-scripts"]);
}

const stoplight = await readFile(
  join(webRoot, "node_modules", "@stoplight/elements/web-components.min.js"),
  "utf8"
);
const mermaidVersion = stoplight.match(
  /https:\/\/unpkg\.com\/mermaid@([^/]+)\/dist\/mermaid\.min\.js/
)?.[1];
if (!mermaidVersion) {
  throw new Error("Cannot determine the Mermaid version used by Stoplight");
}

if (
  packageJSON.devDependencies.mermaid !== mermaidVersion ||
  (await installedVersion("mermaid")) !== mermaidVersion
) {
  runNPM([
    "install",
    "--save-dev",
    "--save-exact",
    "--ignore-scripts",
    `mermaid@${mermaidVersion}`
  ]);
}

runNPM(["run", "build", "--silent"]);

async function latestVersion(name) {
  const endpoint = `https://registry.npmjs.org/${encodeURIComponent(name)}/latest`;
  const response = await fetch(endpoint, {
    headers: { "user-agent": "7link-doc-assets/1.0" },
    signal: AbortSignal.timeout(30_000)
  });
  if (!response.ok) {
    throw new Error(`Cannot check ${name}: HTTP ${response.status}`);
  }
  const metadata = await response.json();
  if (typeof metadata.version !== "string" || metadata.version.length === 0) {
    throw new Error(`Registry returned no version for ${name}`);
  }
  return metadata.version;
}

async function anyDependencyMissing(dependencies) {
  for (const [name, version] of Object.entries(dependencies)) {
    if ((await installedVersion(name)) !== version) {
      return true;
    }
  }
  return false;
}

async function installedVersion(name) {
  try {
    return (await readJSON(join(webRoot, "node_modules", name, "package.json"))).version;
  } catch (error) {
    if (error.code === "ENOENT") {
      return undefined;
    }
    throw error;
  }
}

async function readJSON(path) {
  return JSON.parse(await readFile(path, "utf8"));
}

function runNPM(args) {
  const executable = process.platform === "win32" ? "npm.cmd" : "npm";
  run(executable, args);
}

function run(command, args) {
  const result = spawnSync(command, args, { cwd: webRoot, stdio: "inherit" });
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
}
