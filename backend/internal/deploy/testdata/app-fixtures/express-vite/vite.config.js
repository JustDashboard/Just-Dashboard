import path from "path"
export default {
  root: path.resolve(import.meta.dirname, "client"),
  build: { outDir: path.resolve(import.meta.dirname, "dist/public"), emptyOutDir: true },
}
