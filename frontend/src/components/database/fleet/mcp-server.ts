import type { ApiToken, ApiTokenConnection } from "@/lib/types"

/** The endpoint an AI client is pointed at. Only an MCP key opens it. */
export const MCP_PATH = "/api/v1/mcp"

/** What the dashboard is called in a client's own list of servers. */
const SERVER_NAME = "just-dashboard"

export type McpClient = "claude-code" | "cursor" | "vscode"

/**
 * The clients the dialog writes a configuration for, each with where that
 * configuration goes. All three speak Streamable HTTP and send a header with
 * every request, which is all the endpoint asks of a client.
 */
export const MCP_CLIENTS: { id: McpClient; label: string; where: string }[] = [
  { id: "claude-code", label: "Claude Code", where: "Run it once in a terminal." },
  {
    id: "cursor",
    label: "Cursor",
    where: "Put it in .cursor/mcp.json in a project, or ~/.cursor/mcp.json for every project.",
  },
  { id: "vscode", label: "VS Code", where: "Put it in .vscode/mcp.json in a workspace." },
]

/** A client's configuration for this dashboard, with the key in it when there is one. */
export function clientConfig(client: McpClient, origin: string, key = "vpsd_…"): string {
  const url = `${origin}${MCP_PATH}`
  const headers = { Authorization: `Bearer ${key}` }
  switch (client) {
    case "claude-code":
      return `claude mcp add --transport http ${SERVER_NAME} ${url} --header "Authorization: Bearer ${key}"`
    case "cursor":
      return JSON.stringify({ mcpServers: { [SERVER_NAME]: { url, headers } } }, null, 2)
    case "vscode":
      return JSON.stringify({ servers: { [SERVER_NAME]: { type: "http", url, headers } } }, null, 2)
  }
}

/** What a key reaches, by connection id. */
export type Grants = Record<number, ApiTokenConnection["access"]>

export function grantsOf(token?: Pick<ApiToken, "connections">): Grants {
  return Object.fromEntries((token?.connections ?? []).map((c) => [c.connectionId, c.access]))
}

/** The grants as the server takes them, in id order so a request reads the same twice. */
export function grantList(grants: Grants): ApiTokenConnection[] {
  return Object.entries(grants)
    .map(([id, access]) => ({ connectionId: Number(id), access }))
    .sort((a, b) => a.connectionId - b.connectionId)
}

export function sameGrants(a: Grants, b: Grants): boolean {
  const left = grantList(a)
  const right = grantList(b)
  return (
    left.length === right.length &&
    left.every((g, i) => g.connectionId === right[i].connectionId && g.access === right[i].access)
  )
}

/**
 * The grants an owner can still hand out: one who may no longer change a
 * database keeps every database, to read. The server refuses a write grant
 * from such an owner, so a replacement made without this would fail outright.
 */
export function grantsWithin(grants: Grants, canChange: boolean): Grants {
  if (canChange) return grants
  return Object.fromEntries(Object.keys(grants).map((id) => [id, "read" as const]))
}

/**
 * What a key reaches, in the words a list row has room for: "reads shop,
 * changes notes". A database since forgotten has taken its grant with it, so
 * an id with no name is one the list has not caught up with yet.
 */
export function grantSummary(
  token: Pick<ApiToken, "connections">,
  nameOf: (id: number) => string | undefined,
): string {
  const named = (token.connections ?? [])
    .map((c) => ({ ...c, name: nameOf(c.connectionId) }))
    .filter((c): c is ApiTokenConnection & { name: string } => c.name !== undefined)
  if (named.length === 0) return "no databases"
  const byAccess = (access: ApiTokenConnection["access"]) =>
    named
      .filter((c) => c.access === access)
      .map((c) => c.name)
      .sort((a, b) => a.localeCompare(b))
  const reads = byAccess("read")
  const changes = byAccess("write")
  return [
    reads.length > 0 && `reads ${listWords(reads)}`,
    changes.length > 0 && `changes ${listWords(changes)}`,
  ]
    .filter(Boolean)
    .join(", ")
}

/** Up to three names, then a count: a row is one line. */
function listWords(names: string[]): string {
  if (names.length <= 3) return names.join(", ")
  return `${names.slice(0, 2).join(", ")} and ${names.length - 2} more`
}
