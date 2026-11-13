/*
  Serve the last `bun run build` the way the image does.

    bun run start --hostname 127.0.0.1 --port 43117

  The build is `output: "standalone"`, and what an operator runs is
  `node server.js` from `.next/standalone`, with `.next/static` and `public`
  copied beside it (frontend/Dockerfile). `next start` is another server: it
  reads the whole of node_modules and the project's own `public`, so a file
  the standalone trace leaves out, or one only the project folder has, was
  served to every browser spec and missing in the image. The editor shipped
  that way once. Next also says of it that it "does not work with
  output: standalone", which the documented command contradicted.

  So `start` is this: the same copies the Dockerfile makes, then the same
  server, in this process. The flags are `next start`'s, so every command in
  the documents and in CI reads as it did.
*/
import { cpSync, existsSync, rmSync } from "node:fs"
import { createRequire } from "node:module"
import { dirname, resolve } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

/** The folders the image copies beside the standalone server, as [from, to] under the project. */
export const COPIED = [
  [".next/static", ".next/standalone/.next/static"],
  ["public", ".next/standalone/public"],
]

/**
 * Where to listen, from `next start`'s flags.
 *
 * The port falls back to PORT and then 3000, as `next start` does. The host
 * falls back to loopback and never to HOSTNAME: a shell or a container sets
 * that to the machine's name, which the standalone server would bind as an
 * address, and a test server has no reason to listen anywhere routable.
 */
export function listenOptions(argv, env = {}) {
  let hostname = "127.0.0.1"
  let port = env.PORT ?? "3000"
  for (let i = 0; i < argv.length; i++) {
    const [flag, inline] = argv[i].startsWith("--") ? argv[i].split(/=(.*)/s, 2) : [argv[i]]
    const value = () => {
      const next = inline ?? argv[++i]
      if (next === undefined || next === "") throw new Error(`${flag} needs a value`)
      return next
    }
    if (flag === "--hostname" || flag === "-H") hostname = value()
    else if (flag === "--port" || flag === "-p") port = value()
    else throw new Error(`unknown argument ${argv[i]}; this takes --hostname and --port`)
  }
  if (!/^\d+$/.test(String(port)) || Number(port) > 65535)
    throw new Error(`"${port}" is not a port`)
  return { hostname, port: Number(port) }
}

function main() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
  const server = resolve(root, ".next/standalone/server.js")
  let listen
  try {
    listen = listenOptions(process.argv.slice(2), process.env)
  } catch (error) {
    console.error(error.message)
    process.exit(2)
  }
  if (!existsSync(server)) {
    console.error(
      "There is no build to serve (.next/standalone/server.js): run `bun run build` first.",
    )
    process.exit(1)
  }
  // Replaced, not merged: a file an earlier build left there is one this
  // build does not have, and serving it would hide exactly that.
  for (const [from, to] of COPIED) {
    rmSync(resolve(root, to), { recursive: true, force: true })
    cpSync(resolve(root, from), resolve(root, to), { recursive: true })
  }
  process.env.HOSTNAME = listen.hostname
  process.env.PORT = String(listen.port)
  // The server changes into its own folder and listens; it is CommonJS.
  createRequire(import.meta.url)(server)
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main()
