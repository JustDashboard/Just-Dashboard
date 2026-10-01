import { del, downloadUrl, get, getText, post, type Query } from "@/lib/api"
import { keyQuery, prefixQuery } from "@/components/database/redis/bytes"
import { nodeAt, parseJsonDoc, type JsonNode } from "@/components/database/redis/keys/json-doc"
import type {
  RedisAnalysis,
  RedisBulkRequest,
  RedisBulkResult,
  RedisBytes,
  RedisClassifyResponse,
  RedisClientInfo,
  RedisCommandReference,
  RedisCommandResult,
  RedisCommandStats,
  RedisDeleteRequest,
  RedisKeyMeta,
  RedisLatency,
  RedisMembers,
  RedisPage,
  RedisPubSub,
  RedisServer,
  RedisSlowlog,
  RedisStats,
  RedisStreamInfo,
  RedisStreamPending,
  RedisTree,
  RedisWriteRequest,
} from "@/components/database/redis/types"

/**
 * Which logical database a request is about. `db` left out is the one the
 * connection string names — which is not database 0 — so it is sent only
 * once the reader, or the address, has named one.
 */
export type RedisTarget = { id: number; db: number | undefined }

const at = (id: number) => `/databases/${id}`

/**
 * An answer held to the one field the page cannot do without. A server that
 * answers one of these routes with something else — an older backend, a
 * proxy's page — is a failed read with a sentence, not a page that throws
 * half-way through drawing it.
 */
function shaped<T>(value: unknown, field: string, what: string): T {
  if (typeof value === "object" && value !== null && field in value) return value as T
  throw new Error(`The server's answer for ${what} could not be read.`)
}

export function redisServer(id: number, signal?: AbortSignal) {
  return get<unknown>(`${at(id)}/redis/server`, undefined, signal).then((answer) =>
    shaped<RedisServer>(answer, "features", "the server"),
  )
}

export function redisStats(id: number, signal?: AbortSignal) {
  return get<unknown>(`${at(id)}/stats`, undefined, signal).then(
    (answer) => shaped<RedisStats>(answer, "server", "the server's counters").server,
  )
}

export function redisKeys(
  target: RedisTarget,
  query: { pattern: string; type?: string; cursor: string; count?: number },
  signal?: AbortSignal,
) {
  return get<unknown>(
    `${at(target.id)}/keys`,
    {
      pattern: query.pattern || "*",
      type: query.type || undefined,
      // As the server handed it over: a scan cursor is an unsigned 64-bit
      // number, and a JavaScript number would round the last of its digits.
      cursor: query.cursor,
      count: query.count ?? 200,
      db: target.db,
    },
    signal,
  ).then((answer) => shaped<RedisPage>(answer, "keys", "the keys"))
}

export function redisTree(
  target: RedisTarget,
  query: { prefix: RedisBytes; pattern?: string; type?: string; cursor?: string; leaves?: number },
  signal?: AbortSignal,
) {
  return get<unknown>(
    `${at(target.id)}/keys/tree`,
    {
      ...prefixQuery(query.prefix),
      pattern: query.pattern || undefined,
      type: query.type || undefined,
      cursor: query.cursor,
      leaves: query.leaves ?? 500,
      db: target.db,
    },
    signal,
  ).then((answer) => shaped<RedisTree>(answer, "folders", "the namespaces"))
}

export function redisMeta(target: RedisTarget, key: RedisBytes, signal?: AbortSignal) {
  return get<unknown>(
    `${at(target.id)}/keys/meta`,
    { ...keyQuery(key), db: target.db },
    signal,
  ).then((answer) => shaped<RedisKeyMeta>(answer, "type", "the key"))
}

export type MembersQuery = {
  cursor?: string
  count?: number
  match?: string
  order?: "asc" | "desc"
  min?: string
  max?: string
  from?: string
  to?: string
  path?: string
}

export function redisMembers(
  target: RedisTarget,
  key: RedisBytes,
  query: MembersQuery = {},
  signal?: AbortSignal,
) {
  const sent: Query = { ...keyQuery(key), db: target.db }
  for (const [name, value] of Object.entries(query)) {
    if (value !== undefined && value !== "") sent[name] = value
  }
  return get<unknown>(`${at(target.id)}/keys/members`, sent, signal).then((answer) =>
    shaped<RedisMembers>(answer, "rows", "the key's value"),
  )
}

/**
 * A JSON document, read as the text the server sent. The answer is not handed
 * to `JSON.parse` for the document's sake: a 64-bit number in it would come
 * back rounded, and an editor that saved what it showed would write the
 * rounding into the key. The facts around the document are read the ordinary
 * way; the matches are kept as nodes that hold every digit.
 */
export async function redisJson(
  target: RedisTarget,
  key: RedisBytes,
  path: string,
  signal?: AbortSignal,
): Promise<{ info: RedisMembers; matches: JsonNode[] | undefined }> {
  const query = new URLSearchParams()
  for (const [name, value] of Object.entries({ ...keyQuery(key), path, db: target.db })) {
    if (value !== undefined) query.set(name, String(value))
  }
  const text = await getText(`${at(target.id)}/keys/members?${query}`, signal)
  const doc = parseJsonDoc(text)
  const info = shaped<RedisMembers>(doc ? JSON.parse(text) : undefined, "rows", "the document")
  const matches = doc && nodeAt(doc, ["json", "matches"])
  return { info, matches: matches?.kind === "array" ? matches.items : undefined }
}

/** One whole value as a file the browser saves: a string, a hash field, a list element. */
export function redisRawUrl(
  target: RedisTarget,
  key: RedisBytes,
  member: { field?: RedisBytes; index?: number } = {},
) {
  const field =
    member.field === undefined
      ? {}
      : typeof member.field === "string"
        ? { field: member.field }
        : { fieldB64: member.field.base64 }
  return downloadUrl(`${at(target.id)}/keys/raw`, {
    ...keyQuery(key),
    ...field,
    index: member.index,
    db: target.db,
  })
}

const db = (target: RedisTarget): Query => ({ db: target.db })

export function redisWrite(target: RedisTarget, body: RedisWriteRequest) {
  return post<{ ok: true; id?: string }>(`${at(target.id)}/keys/value`, body, {
    query: db(target),
  })
}

export function redisExpire(
  target: RedisTarget,
  body: { key: RedisBytes; ttl?: number; at?: number },
) {
  return post<{ ok: true; pttl: number }>(`${at(target.id)}/keys/expire`, body, {
    query: db(target),
  })
}

export function redisPersist(target: RedisTarget, key: RedisBytes) {
  return post<{ ok: true; pttl: number }>(
    `${at(target.id)}/keys/persist`,
    { key },
    { query: db(target) },
  )
}

export function redisRename(
  target: RedisTarget,
  body: { key: RedisBytes; to: RedisBytes; overwrite?: boolean },
) {
  return post<{ ok: true }>(`${at(target.id)}/keys/rename`, body, { query: db(target) })
}

export function redisCopy(
  target: RedisTarget,
  body: { key: RedisBytes; to: RedisBytes; toDb?: number; overwrite?: boolean },
) {
  return post<{ ok: true }>(`${at(target.id)}/keys/copy`, body, { query: db(target) })
}

export function redisDelete(target: RedisTarget, body: RedisDeleteRequest) {
  return del<{ removed: number }>(`${at(target.id)}/keys`, { body, query: db(target) })
}

export function redisBulk(target: RedisTarget, body: RedisBulkRequest) {
  return post<RedisBulkResult>(`${at(target.id)}/keys/bulk`, body, { query: db(target) })
}

export function redisStream(target: RedisTarget, key: RedisBytes, signal?: AbortSignal) {
  return get<unknown>(
    `${at(target.id)}/keys/stream`,
    { ...keyQuery(key), db: target.db },
    signal,
  ).then((answer) => shaped<RedisStreamInfo>(answer, "groups", "the stream"))
}

export function redisStreamPending(
  target: RedisTarget,
  key: RedisBytes,
  query: { group: string; cursor?: string; count?: number },
  signal?: AbortSignal,
) {
  return get<unknown>(
    `${at(target.id)}/keys/stream/pending`,
    { ...keyQuery(key), ...query, db: target.db },
    signal,
  ).then((answer) => shaped<RedisStreamPending>(answer, "entries", "the pending entries"))
}

export function redisGroupSet(
  target: RedisTarget,
  body: { key: RedisBytes; group: RedisBytes; id?: string; setId?: boolean },
) {
  return post<{ ok: true }>(`${at(target.id)}/keys/stream/groups`, body, { query: db(target) })
}

export function redisGroupRemove(
  target: RedisTarget,
  // `consumer` present — the empty name included — removes that consumer and
  // keeps the group; left out, the group is destroyed.
  body: { key: RedisBytes; group: RedisBytes; consumer?: RedisBytes },
) {
  return del<{ removed: number }>(`${at(target.id)}/keys/stream/groups`, {
    body,
    query: db(target),
  })
}

export function redisAck(
  target: RedisTarget,
  body: { key: RedisBytes; group: RedisBytes; ids: string[] },
) {
  return post<{ acknowledged: number }>(`${at(target.id)}/keys/stream/ack`, body, {
    query: db(target),
  })
}

export function redisTrim(target: RedisTarget, body: { key: RedisBytes; maxLen: number }) {
  return post<{ removed: number }>(`${at(target.id)}/keys/stream/trim`, body, {
    query: db(target),
  })
}

export function redisClassify(target: RedisTarget, command: string) {
  return post<RedisClassifyResponse>(`${at(target.id)}/redis/classify`, {
    command,
    db: target.db,
  })
}

export function redisCommand(target: RedisTarget, command: string) {
  return post<RedisCommandResult>(`${at(target.id)}/redis/command`, { command, db: target.db })
}

export function redisCommands(id: number, signal?: AbortSignal) {
  return get<unknown>(`${at(id)}/redis/commands`, undefined, signal).then((answer) =>
    shaped<RedisCommandReference>(answer, "commands", "the command reference"),
  )
}

export function redisCommandStats(id: number, signal?: AbortSignal) {
  return get<unknown>(`${at(id)}/redis/commandstats`, undefined, signal).then((answer) =>
    shaped<RedisCommandStats>(answer, "commands", "the command statistics"),
  )
}

export function redisLatency(id: number, signal?: AbortSignal) {
  return get<unknown>(`${at(id)}/redis/latency`, undefined, signal).then((answer) =>
    shaped<RedisLatency>(answer, "events", "the latency monitor"),
  )
}

export function redisClients(id: number, signal?: AbortSignal) {
  return get<unknown>(`${at(id)}/redis/clients`, undefined, signal).then(
    (answer) => shaped<{ clients: RedisClientInfo[] }>(answer, "clients", "the clients").clients,
  )
}

export function redisKillClient(id: number, client: number) {
  return post<{ ok: true }>(`${at(id)}/redis/clients/kill`, { id: client })
}

export function redisSlowlog(id: number, signal?: AbortSignal) {
  return get<unknown>(`${at(id)}/redis/slowlog`, { count: 256 }, signal).then((answer) =>
    shaped<RedisSlowlog>(answer, "entries", "the slow log"),
  )
}

export function redisSlowlogReset(id: number) {
  return post<unknown>(`${at(id)}/redis/slowlog/reset`)
}

export function redisSave(id: number, mode: "bgsave" | "bgrewriteaof") {
  return post<{ ok: true; status: string }>(`${at(id)}/redis/save`, { mode })
}

export function redisAnalysis(target: RedisTarget, sample: number, signal?: AbortSignal) {
  return get<unknown>(
    `${at(target.id)}/redis/analysis`,
    { sample, top: 50, db: target.db },
    signal,
  ).then((answer) => shaped<RedisAnalysis>(answer, "types", "the memory analysis"))
}

export function redisPubSub(id: number, pattern: string, signal?: AbortSignal) {
  return get<unknown>(`${at(id)}/redis/pubsub`, { pattern: pattern || undefined }, signal).then(
    (answer) => shaped<RedisPubSub>(answer, "channels", "the channels"),
  )
}

export function redisPublish(id: number, channel: string, message: string) {
  return post<{ receivers: number }>(`${at(id)}/redis/publish`, { channel, message })
}
