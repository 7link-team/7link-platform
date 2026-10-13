import { build } from "esbuild";
import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
const root = path.dirname(fileURLToPath(import.meta.url));
await build({
  entryPoints: [path.join(root, "editor.jsx")],
  bundle: true,
  minify: true,
  legalComments: "eof",
  outfile: path.join(root, "workflow-editor.bundle.js"),
  define: { "process.env.NODE_ENV": '"production"' },
});
const packages = [
  "react",
  "react-dom",
  "scheduler",
  "@xyflow/react",
  "@xyflow/system",
  "zustand",
  "use-sync-external-store",
  "d3-selection",
  "d3-zoom",
  "d3-drag",
  "d3-dispatch",
  "d3-interpolate",
  "d3-color",
  "d3-transition",
  "d3-timer",
  "d3-ease",
  "classcat",
];
let notices =
  "Third-party software included in the prototype workflow editor.\nRegenerate: npm ci && npm run build\n\n";
for (const name of packages) {
  const folder = path.join(root, "node_modules", name);
  const meta = JSON.parse(
    await readFile(path.join(folder, "package.json"), "utf8"),
  );
  let license = "";
  for (const file of ["LICENSE", "LICENSE.md", "LICENSE.txt"]) {
    try {
      license = await readFile(path.join(folder, file), "utf8");
      break;
    } catch {}
  }
  if (!license) throw new Error("Missing license: " + name);
  notices += `${name} ${meta.version}\n${license}\n\n`;
}
await writeFile(path.join(root, "THIRD-PARTY-NOTICES.txt"), notices.trimEnd() + "\n");
