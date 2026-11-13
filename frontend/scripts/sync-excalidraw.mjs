// Copies Excalidraw's fonts into public/ so a drawing's lettering comes from
// this server and not from a CDN (docs/internal/boards.md).
//
// Under the package's version, public/excalidraw/<version>/fonts, for the
// reason scripts/sync-monaco.mjs gives: the files have no hash in their names,
// and with the version in the path the browser can keep them for good instead
// of asking about each one on every visit. The folder is emptied first, so one
// version is all an image ever carries.
import { cp, mkdir, readFile, rm } from "node:fs/promises"
import { dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const pkg = resolve(root, "node_modules/@excalidraw/excalidraw")
const source = resolve(pkg, "dist/prod/fonts")
// The installed package's own word, which next.config.ts reads the same way.
const { version } = JSON.parse(await readFile(resolve(pkg, "package.json"), "utf8"))
const home = resolve(root, "public/excalidraw")
const target = resolve(home, version, "fonts")

await rm(home, { recursive: true, force: true })
await mkdir(dirname(target), { recursive: true })
await cp(source, target, { recursive: true, force: true })
