import path from "node:path"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vitest/config"
import { builtinCatalogPlugin } from "./builtin-catalog-plugin.ts"

export default defineConfig({
  plugins: [builtinCatalogPlugin(), react()],
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "./src"),
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: "./src/test/setup.ts",
  },
})
