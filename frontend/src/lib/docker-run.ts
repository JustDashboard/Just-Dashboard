import type { ContainerSpec, MountSpec, PortMapping } from "@/lib/types"

/**
 * Turns a `docker run` command into the form's spec.
 *
 * This is the single most useful thing a Docker panel can do for someone who
 * does not know Docker. Every project's README documents itself as a `docker
 * run` line; the reader's options today are to paste it into a shell they may
 * not have open, or to translate twenty flags into a form by hand. Dockge
 * built a `docker run`-to-compose converter for the same reason and it is one
 * of the most-cited things about it.
 *
 * Parsing is deliberately lenient. A command it does not fully understand
 * still produces a usable spec plus a list of what it skipped, because a form
 * filled in nine tenths of the way with an honest note about the rest is worth
 * far more than an error. Nothing here executes anything — the output is data
 * for a form the operator still has to submit.
 */

export type ParsedRun = {
  spec: ContainerSpec
  /** What could not be read, and what was read and had to be filled in by hand. */
  warnings: string[]
  /**
   * Real Docker flags the visual form has nowhere to put.
   *
   * Kept apart from `warnings` because it is a different statement. A warning
   * says "this could not be read"; this says "this was understood and the
   * editor cannot represent it" — which is the operator's cue that the
   * container they are about to create is not the one the command describes.
   * Silently dropping `--gpus all` produces a container that starts and then
   * has no GPU, which is the worst possible way to find out.
   */
  unsupported: string[]
  /**
   * The command as it was pasted. Kept so the operator can go back to it: the
   * form is a reading of the command, not a replacement for it.
   */
  original: string
}

const emptySpec = (): ContainerSpec => ({
  name: "",
  image: "",
  env: [],
  ports: [],
  mounts: [],
  labels: [],
  networks: [],
  limits: {},
  start: true,
  pull: "missing",
})

/**
 * Splits a command line into tokens, honouring quotes and line continuations.
 *
 * A README's command is almost always wrapped across lines with trailing
 * backslashes, so handling those is not an edge case — it is the common case.
 */
export function tokenise(input: string): string[] {
  const tokens: string[] = []
  let current = ""
  let quote: '"' | "'" | null = null
  let started = false

  const push = () => {
    if (started) tokens.push(current)
    current = ""
    started = false
  }

  for (let i = 0; i < input.length; i++) {
    const ch = input[i]

    if (quote) {
      if (ch === quote) {
        quote = null
        continue
      }
      // Only double quotes process escapes, as in a real shell.
      if (ch === "\\" && quote === '"' && i + 1 < input.length) {
        current += input[++i]
        started = true
        continue
      }
      current += ch
      started = true
      continue
    }

    if (ch === '"' || ch === "'") {
      quote = ch
      // An empty quoted string is still a token: -e FOO="" means something.
      started = true
      continue
    }
    if (ch === "\\") {
      const next = input[i + 1]
      if (next === "\n" || next === "\r") {
        // Line continuation: swallow the backslash and the newline.
        i++
        if (next === "\r" && input[i + 1] === "\n") i++
        continue
      }
      if (next !== undefined) {
        current += next
        started = true
        i++
        continue
      }
      continue
    }
    if (/\s/.test(ch)) {
      push()
      continue
    }
    // A comment line in a pasted block is documentation, not an argument.
    if (ch === "#" && !started) {
      while (i < input.length && input[i] !== "\n") i++
      continue
    }
    current += ch
    started = true
  }
  push()
  return tokens
}

/** Flags that take no value, in both their long and short spellings. */
const BOOLEAN_FLAGS = new Set([
  "-d",
  "--detach",
  "--rm",
  "-i",
  "--interactive",
  "-t",
  "--tty",
  "-it",
  "-ti",
  "-itd",
  "-dit",
  "--privileged",
  "--init",
  "--read-only",
  "--no-healthcheck",
  "-q",
  "--quiet",
  "--sig-proxy",
])

/** Flags whose value we understand and can put somewhere in the spec. */
const VALUE_FLAGS = new Set([
  "--name",
  "-p",
  "--publish",
  "-v",
  "--volume",
  "--mount",
  "--tmpfs",
  "-e",
  "--env",
  "--env-file",
  "-l",
  "--label",
  "--restart",
  "--network",
  "--net",
  "--network-alias",
  "-h",
  "--hostname",
  "--add-host",
  "--dns",
  "-u",
  "--user",
  "-w",
  "--workdir",
  "-m",
  "--memory",
  "--memory-swap",
  "--cpus",
  "--pids-limit",
  "--shm-size",
  "--device",
  "--cap-add",
  "--cap-drop",
  "--entrypoint",
  "--stop-signal",
  "--health-cmd",
  "--health-interval",
  "--health-timeout",
  "--health-retries",
  "--health-start-period",
  "--pull",
  "--platform",
  "--log-driver",
  "--log-opt",
  "--expose",
  "--security-opt",
  "--ulimit",
  "--sysctl",
  "--gpus",
  "--label-file",
  "--cpuset-cpus",
  "--runtime",
  "--userns",
  "--ipc",
  "--pid",
  "--stop-timeout",
])

export function parseDockerRun(input: string): ParsedRun {
  const spec = emptySpec()
  const warnings: string[] = []
  let tokens = tokenise(input)

  // Tolerate `sudo docker run …`, `podman run …`, and a bare flag list with no
  // command in front of it — all three turn up in READMEs.
  while (
    tokens.length &&
    ["sudo", "docker", "podman", "run", "container", "-"].includes(tokens[0])
  ) {
    tokens = tokens.slice(1)
  }
  if (tokens.length === 0) {
    return {
      spec,
      warnings: ["Nothing to read in that command."],
      unsupported: [],
      original: input,
    }
  }

  // Flags recognised as real Docker flags that the visual form has nowhere to
  // put. Collected separately from parse warnings because they are a different
  // statement: not "this could not be read" but "this was understood and the
  // editor cannot represent it", which is the operator's cue to keep the
  // original command.
  const unsupported: string[] = []
  const health: string[] = []
  let healthInterval: number | undefined
  let healthRetries: number | undefined
  let healthTimeout: number | undefined
  let healthStart: number | undefined
  let noHealth = false
  const aliases: string[] = []

  let i = 0
  for (; i < tokens.length; i++) {
    const token = tokens[i]
    if (!token.startsWith("-")) break // the image, and everything after it

    // --flag=value and -e=value both appear in the wild.
    let flag = token
    let inlineValue: string | undefined
    const eq = token.indexOf("=")
    if (eq > 1 && token.startsWith("--")) {
      flag = token.slice(0, eq)
      inlineValue = token.slice(eq + 1)
    }

    // -eFOO=bar, -p8080:80 — short flags with the value stuck on.
    if (!flag.startsWith("--") && flag.length > 2 && !BOOLEAN_FLAGS.has(flag)) {
      inlineValue = flag.slice(2)
      flag = flag.slice(0, 2)
    }

    if (BOOLEAN_FLAGS.has(flag)) {
      applyBoolean(spec, flag, () => (noHealth = true))
      continue
    }

    const value = inlineValue ?? tokens[++i]
    if (value === undefined) {
      warnings.push(`\`${flag}\` was given no value and has been ignored.`)
      continue
    }
    if (!VALUE_FLAGS.has(flag)) {
      unsupported.push(`${flag} ${value}`)
      continue
    }

    switch (flag) {
      case "--name":
        spec.name = value
        break
      case "-p":
      case "--publish": {
        const port = parsePublish(value)
        if (port) spec.ports!.push(port)
        else warnings.push(`Could not read the port mapping \`${value}\`.`)
        break
      }
      case "--expose": {
        const [portText, proto] = value.split("/")
        const port = Number(portText)
        if (port) spec.ports!.push({ hostPort: 0, containerPort: port, protocol: proto || "tcp" })
        break
      }
      case "-v":
      case "--volume": {
        const mount = parseVolumeFlag(value)
        if (mount) spec.mounts!.push(mount)
        else warnings.push(`Could not read the volume \`${value}\`.`)
        break
      }
      case "--mount": {
        const mount = parseMountFlag(value)
        if (mount) spec.mounts!.push(mount)
        else warnings.push(`Could not read the mount \`${value}\`.`)
        break
      }
      case "--tmpfs":
        spec.mounts!.push({ type: "tmpfs", target: value.split(":")[0] })
        break
      case "-e":
      case "--env": {
        const [name, ...rest] = value.split("=")
        // `-e FOO` with no value means "pass FOO through from the shell",
        // which cannot survive a paste into a form.
        if (rest.length === 0) {
          warnings.push(
            `\`${name}\` was passed through from the shell's own environment, so its value is not in this command. Fill it in below.`,
          )
          spec.env!.push({ name, value: "" })
        } else {
          spec.env!.push({ name, value: rest.join("=") })
        }
        break
      }
      case "--env-file":
        warnings.push(
          `Variables from \`${value}\` are not included — that file is read at run time. Add the ones you need below.`,
        )
        break
      case "-l":
      case "--label": {
        const [name, ...rest] = value.split("=")
        spec.labels!.push({ name, value: rest.join("=") })
        break
      }
      case "--restart": {
        const [policy, retries] = value.split(":")
        spec.restartPolicy = policy
        if (retries) spec.maxRetries = Number(retries) || 0
        break
      }
      case "--network":
      case "--net":
        if (["host", "none", "bridge"].includes(value) || value.startsWith("container:")) {
          spec.networkMode = value
        } else {
          spec.networks!.push(value)
        }
        break
      case "--network-alias":
        aliases.push(value)
        break
      case "-h":
      case "--hostname":
        spec.hostname = value
        break
      case "--add-host":
        spec.extraHosts = [...(spec.extraHosts ?? []), value]
        break
      case "--dns":
        spec.dns = [...(spec.dns ?? []), value]
        break
      case "-u":
      case "--user":
        spec.user = value
        break
      case "-w":
      case "--workdir":
        spec.workingDir = value
        break
      case "-m":
      case "--memory":
        spec.limits.memoryMb = parseSizeMb(value)
        break
      case "--memory-swap":
        spec.limits.memorySwapMb = parseSizeMb(value)
        break
      case "--cpus":
        spec.limits.cpus = Number(value) || undefined
        break
      case "--pids-limit":
        spec.limits.pidsLimit = Number(value) || undefined
        break
      case "--shm-size":
        spec.limits.shmSizeMb = parseSizeMb(value)
        break
      case "--device": {
        const [host, container, permissions] = value.split(":")
        spec.devices = [...(spec.devices ?? []), { host, container, permissions }]
        break
      }
      case "--cap-add":
        spec.capAdd = [...(spec.capAdd ?? []), value]
        break
      case "--cap-drop":
        spec.capDrop = [...(spec.capDrop ?? []), value]
        break
      case "--entrypoint":
        spec.entrypoint = tokenise(value)
        break
      case "--stop-signal":
        spec.stopSignal = value
        break
      case "--health-cmd":
        health.push(value)
        break
      case "--health-interval":
        healthInterval = parseSeconds(value)
        break
      case "--health-timeout":
        healthTimeout = parseSeconds(value)
        break
      case "--health-start-period":
        healthStart = parseSeconds(value)
        break
      case "--health-retries":
        healthRetries = Number(value) || undefined
        break
      case "--pull":
        spec.pull = value === "always" ? "always" : "missing"
        break
      case "--log-driver":
        spec.logging = { ...spec.logging, driver: value }
        break
      case "--log-opt": {
        const [name, ...rest] = value.split("=")
        spec.logging = {
          ...spec.logging,
          options: { ...spec.logging?.options, [name]: rest.join("=") },
        }
        break
      }
      // `--gpus all` and `--security-opt` change what the container is allowed
      // to do, so they cannot quietly vanish: the form has nowhere to put them
      // and the warning says exactly that rather than leaving the operator to
      // discover it when the container starts without a GPU.
      default:
        unsupported.push(`${flag} ${value}`)
    }
  }

  if (i < tokens.length) {
    spec.image = tokens[i]
    const command = tokens.slice(i + 1)
    if (command.length) spec.command = command
  }
  if (!spec.image) {
    warnings.push(
      "No image name found. A `docker run` command ends with the image and, optionally, a command.",
    )
  }
  if (noHealth) {
    spec.health = { test: [], disable: true }
  } else if (health.length) {
    spec.health = {
      test: health,
      intervalSeconds: healthInterval,
      timeoutSeconds: healthTimeout,
      startPeriodSeconds: healthStart,
      retries: healthRetries,
    }
  }
  if (aliases.length) {
    warnings.push(
      `Network aliases (${aliases.join(", ")}) are set after the container is created — attach it to the network afterwards to add them.`,
    )
  }
  if (!spec.name && spec.image) {
    // Docker would invent a two-word name. A name derived from the image is
    // both more useful and what most people would have typed.
    spec.name = suggestName(spec.image)
  }
  return { spec, warnings, unsupported, original: input }
}

function applyBoolean(spec: ContainerSpec, flag: string, noHealthcheck: () => void) {
  switch (flag) {
    case "--rm":
      spec.autoRemove = true
      break
    case "-t":
    case "--tty":
      spec.tty = true
      break
    case "-i":
    case "--interactive":
      spec.openStdin = true
      break
    case "-it":
    case "-ti":
      spec.tty = true
      spec.openStdin = true
      break
    case "-itd":
    case "-dit":
      spec.tty = true
      spec.openStdin = true
      break
    case "--privileged":
      spec.privileged = true
      break
    case "--init":
      spec.init = true
      break
    case "--read-only":
      spec.readOnlyRootfs = true
      break
    case "--no-healthcheck":
      noHealthcheck()
      break
    // -d and the quiet flags describe how the CLI behaves, not the container.
  }
}

/**
 * Reads `[host-ip:][host-port:]container-port[/protocol]`.
 *
 * The three-part form with an address is the one that matters most here: it is
 * how a README tells you to keep something off the internet, and dropping it
 * silently would turn a safe command into an exposed one.
 */
function parsePublish(value: string): PortMapping | null {
  const [spec, proto] = value.split("/")
  const parts = spec.split(":")
  const protocol = proto === "udp" ? "udp" : "tcp"

  if (parts.length === 1) {
    // `-p 80` publishes the container port on a random host port.
    const port = Number(parts[0])
    return port ? { hostPort: 0, containerPort: port, protocol } : null
  }
  if (parts.length === 2) {
    const [host, container] = parts.map(Number)
    return container ? { hostPort: host || 0, containerPort: container, protocol } : null
  }
  if (parts.length >= 3) {
    // An IPv6 address contains colons of its own; everything but the last two
    // parts is the address.
    const containerPort = Number(parts[parts.length - 1])
    const hostPort = Number(parts[parts.length - 2])
    const hostIp = parts.slice(0, -2).join(":")
    return containerPort ? { hostIp, hostPort: hostPort || 0, containerPort, protocol } : null
  }
  return null
}

/** Reads `-v source:target[:ro]`, in all three of its forms. */
function parseVolumeFlag(value: string): MountSpec | null {
  const parts = value.split(":")
  // An absolute Windows path would break this; this dashboard runs on Linux.
  if (parts.length === 1) {
    // `-v /data` is an anonymous volume at that path.
    return { type: "volume", target: parts[0] }
  }
  const [source, target, ...options] = parts
  if (!target) return null
  return {
    type:
      source.startsWith("/") || source.startsWith("./") || source.startsWith("~")
        ? "bind"
        : "volume",
    source,
    target,
    readOnly: options.includes("ro"),
  }
}

/** Reads the `--mount type=bind,source=…,target=…` form. */
function parseMountFlag(value: string): MountSpec | null {
  const fields: Record<string, string> = {}
  for (const part of value.split(",")) {
    const [key, ...rest] = part.split("=")
    fields[key.trim()] = rest.join("=").trim()
  }
  const target = fields.target ?? fields.destination ?? fields.dst
  if (!target) return null
  const source = fields.source ?? fields.src
  const type = (fields.type ?? "volume") as MountSpec["type"]
  return {
    type: type === "bind" || type === "tmpfs" ? type : "volume",
    source,
    target,
    readOnly: "readonly" in fields || fields.readonly === "true" || fields.ro === "true",
  }
}

/** Reads Docker's size suffixes: 512m, 2g, 1024k, or plain bytes. */
function parseSizeMb(value: string): number | undefined {
  const match = /^(\d+(?:\.\d+)?)\s*([bkmg])?b?$/i.exec(value.trim())
  if (!match) return undefined
  const size = Number(match[1])
  switch ((match[2] ?? "").toLowerCase()) {
    case "g":
      return Math.round(size * 1024)
    case "m":
      return Math.round(size)
    case "k":
      return Math.max(1, Math.round(size / 1024))
    default:
      return Math.max(1, Math.round(size / (1024 * 1024)))
  }
}

/** Reads Go duration strings, which is what the health flags take. */
function parseSeconds(value: string): number | undefined {
  const match = /^(\d+(?:\.\d+)?)(ms|s|m|h)?$/.exec(value.trim())
  if (!match) return undefined
  const size = Number(match[1])
  switch (match[2]) {
    case "ms":
      return Math.max(1, Math.round(size / 1000))
    case "m":
      return Math.round(size * 60)
    case "h":
      return Math.round(size * 3600)
    default:
      return Math.round(size)
  }
}

/**
 * A container name derived from an image reference: `ghcr.io/org/app:1.2` →
 * `app`. Docker's own fallback is a random two-word name, which is memorable
 * and tells you nothing about what it runs.
 */
export function suggestName(image: string): string {
  const withoutTag = image.split("@")[0].split(":")[0]
  const last = withoutTag.split("/").pop() ?? withoutTag
  const cleaned = last.replace(/[^a-zA-Z0-9_.-]/g, "-")
  return /^[a-zA-Z0-9]/.test(cleaned) ? cleaned : `app-${cleaned}`.replace(/-+$/, "")
}
