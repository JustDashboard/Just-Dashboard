import type {
  DNSConnection,
  DNSConnectionRequest,
  DNSServiceView,
  DNSEngine,
} from "@/lib/network-dns-services"

export type DNSConnectionDraft = {
  name: string
  engine: DNSEngine
  endpoint: string
  serverName: string
  customCA: boolean
  ca: string
  management: boolean
  username: string
  password: string
  token: string
}
export type DNSFormErrors = Partial<Record<keyof DNSConnectionDraft, string>>

export function dnsConnectionDraft(connection?: DNSConnection): DNSConnectionDraft {
  return {
    name: connection?.name ?? "",
    engine: connection?.engine ?? "adguard",
    endpoint: connection?.endpoint ?? "",
    serverName: connection?.serverName ?? "",
    customCA: connection?.customCA ?? false,
    ca: "",
    management: connection?.management ?? false,
    username: "",
    password: "",
    token: "",
  }
}

function endpointProblem(value: string): string | undefined {
  const match = /^(https?):\/\/(\[[0-9a-fA-F:]+\]|[0-9.]+):(\d+)\/?$/.exec(value)
  if (!match || Number(match[3]) < 1 || Number(match[3]) > 65535)
    return "Use an HTTP(S) origin with a literal IP and explicit port, without a path."
  const host = match[2]
  if (!host.startsWith("[")) {
    const octets = host.split(".")
    if (
      octets.length !== 4 ||
      octets.some((part) => !/^(0|[1-9]\d{0,2})$/.test(part) || Number(part) > 255) ||
      host === "0.0.0.0" ||
      (Number(octets[0]) >= 224 && Number(octets[0]) <= 239)
    )
      return "Use a unicast literal IP address."
    if (match[1] === "http" && octets[0] !== "127")
      return "HTTP credentials require loopback. Use verified HTTPS for another address."
  } else {
    let parsed: URL
    try {
      parsed = new URL(value)
    } catch {
      return "Use a complete unicast IPv6 address in brackets."
    }
    if (parsed.hostname === "[::]" || parsed.hostname.toLowerCase().startsWith("[ff"))
      return "Use a unicast literal IP address."
    if (match[1] === "http" && parsed.hostname !== "[::1]")
      return "HTTP credentials require loopback. Use verified HTTPS for another address."
  }
}

export function prepareDNSConnection(
  draft: DNSConnectionDraft,
  connection?: DNSConnection,
): { request?: DNSConnectionRequest; errors: DNSFormErrors } {
  const errors: DNSFormErrors = {}
  const name = draft.name.trim()
  const endpoint = draft.endpoint.trim().replace(/\/$/, "")
  const serverName = draft.serverName.trim()
  if (!name || name.length > 80 || /[\x00-\x1f\x7f]/.test(name))
    errors.name = "Give this connection a name of at most 80 characters."
  errors.endpoint = endpointProblem(endpoint)
  if (connection && (draft.engine !== connection.engine || endpoint !== connection.endpoint))
    errors.endpoint = "A replacement keeps the same native engine and management origin."
  if (
    serverName &&
    (serverName.length > 253 ||
      !serverName
        .replace(/\.$/, "")
        .split(".")
        .every((part) => /^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$/.test(part)))
  )
    errors.serverName = "Use complete DNS labels for the verified TLS server name."
  if (draft.customCA && (!draft.ca.trim() || draft.ca.length > 32768 || draft.ca.includes("\0")))
    errors.ca = "Re-enter the custom CA certificate, up to 32 KiB, or select system trust."
  if (!endpoint.startsWith("https://") && (serverName || draft.customCA))
    errors.serverName = "TLS identity and custom trust require HTTPS."
  const required =
    draft.engine === "adguard"
      ? (["username", "password"] as const)
      : draft.engine === "pihole"
        ? (["password"] as const)
        : (["token"] as const)
  for (const key of required) {
    if (!draft[key] || draft[key].length > 4096 || /[\x00-\x1f\x7f]/.test(draft[key]))
      errors[key] = `Enter the complete native ${key}, up to 4096 characters.`
  }
  if (draft.engine === "technitium" && /\s/.test(draft.token))
    errors.token = "Enter a native API token without whitespace."
  if (Object.values(errors).some(Boolean)) return { errors }
  return {
    errors: {},
    request: {
      name,
      engine: draft.engine,
      endpoint,
      management: draft.management,
      ...(serverName ? { serverName } : {}),
      ...(draft.customCA ? { ca: draft.ca } : {}),
      credential:
        draft.engine === "adguard"
          ? { username: draft.username, password: draft.password }
          : draft.engine === "pihole"
            ? { password: draft.password }
            : { token: draft.token },
    },
  }
}

export function dnsStageProblem(view: DNSServiceView, stale: boolean, admin: boolean) {
  if (!admin) return "Native DNS reviews require system administration access."
  if (stale) return "Refresh the native reading before reviewing a change."
  if (view.state !== "available" || !view.snapshot)
    return "The native owner is unavailable. Refresh before reviewing a change."
  if (!view.connection.management)
    return "This connection is read-only. Replace its configuration to explicitly allow management."
}

export function dnsDraftLines(value: string, max: number, required = false): string[] {
  const lines = value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
  if (lines.length > max || (required && lines.length === 0))
    throw new Error(`Enter ${required ? "1" : "0"}–${max} entries, one per line.`)
  if (new Set(lines).size !== lines.length) throw new Error("Entries must be unique.")
  return lines
}
