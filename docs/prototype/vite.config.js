import { defineConfig } from "vite";
import { fileURLToPath } from "node:url";
export default defineConfig({
  base: "./",
  build: {
    rollupOptions: {
      input: {
        main: fileURLToPath(new URL("./index.html", import.meta.url)),
        review: fileURLToPath(new URL("./review.html", import.meta.url)),
      },
      output: {
        manualChunks(id) {
          if (id.includes("@xyflow") || id.includes("d3-")) return "flow";
          if (id.includes("lucide-react")) return "icons";
          if (id.includes("/react/") || id.includes("/react-dom/"))
            return "react";
        },
      },
    },
  },
});
