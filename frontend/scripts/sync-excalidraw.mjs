import { cp, mkdir } from "node:fs/promises"
import { dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const source = resolve(root, "node_modules/@excalidraw/excalidraw/dist/prod/fonts")
const target = resolve(root, "public/excalidraw/fonts")

await mkdir(dirname(target), { recursive: true })
await cp(source, target, { recursive: true, force: true })
