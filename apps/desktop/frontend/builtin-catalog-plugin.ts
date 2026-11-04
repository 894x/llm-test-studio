import { execFileSync } from "node:child_process"
import path from "node:path"
import { fileURLToPath } from "node:url"
import type { Plugin } from "vite"

const moduleID = "virtual:builtin-catalog"
const resolvedID = "\0" + moduleID
const repositoryRoot = fileURLToPath(new URL("../../../", import.meta.url))
const dataRoot = path.join(repositoryRoot, "data")

export function builtinCatalogPlugin(): Plugin {
  return {
    name: "builtin-catalog",
    resolveId(id) {
      if (id === moduleID) return resolvedID
    },
    load(id) {
      if (id !== resolvedID) return
      const raw = execFileSync("go", ["run", "./cmd/generate-builtin-catalog"], {
        cwd: repositoryRoot, encoding: "utf8", maxBuffer: 32 * 1024 * 1024, timeout: 120_000,
        windowsHide: true,
      })
      JSON.parse(raw)
      return "export default JSON.parse(" + JSON.stringify(raw) + ")"
    },
    configureServer(server) {
      server.watcher.add(dataRoot)
      let timer: ReturnType<typeof setTimeout> | undefined
      const refresh = (_event: string, filename: string) => {
        const relative = path.relative(dataRoot, filename)
        if (relative.startsWith("..") || path.isAbsolute(relative) || !filename.endsWith(".json")) return
        clearTimeout(timer)
        timer = setTimeout(() => {
          for (const environment of Object.values(server.environments)) {
            const module = environment.moduleGraph.getModuleById(resolvedID)
            if (module) environment.moduleGraph.invalidateModule(module)
          }
          server.ws.send({ type: "full-reload" })
        }, 150)
      }
      server.watcher.on("all", refresh)
      server.httpServer?.once("close", () => {
        clearTimeout(timer)
        server.watcher.off("all", refresh)
      })
    },
  }
}
