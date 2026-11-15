/**
 * The addresses of the three things Performance opens as pages of their own:
 * a statement, a table and a session.
 *
 *     /databases/7/performance/statements/<key>
 *     /databases/7/performance/tables/<schema>.<name>
 *     /databases/7/performance/sessions/<pid>
 *
 * Each is one path segment, and what goes in it is not ours to choose: a
 * table is named by whoever made it (`reports.2026/q1`), a session's id on
 * one engine is `12,3301`, and a statement another role ran has no id at all.
 * A percent-escape would say all of that, and is the one spelling that does
 * not arrive: a proxy in front of the dashboard may decode `%2F` into a
 * separator before the router sees it, and the router decodes a segment once
 * on the server and not at all in the browser. So a segment here holds no
 * percent sign. What `encodeURIComponent` would have escaped is written with
 * `~` where the `%` stood, which no layer between the address bar and the
 * page reads as anything, and a dot inside a name is escaped too, so the one
 * dot left in a table's segment is the one between its schema and its name.
 *
 * `public.orders` is still `public.orders`: only a name that needs it reads
 * as escaped.
 */

/** One value as (part of) a path segment. The empty string is `~`. */
export function segment(value: string): string {
  if (value === "") return "~"
  return encodeURIComponent(value)
    .replace(/[.~]/g, (char) => `%${char.charCodeAt(0).toString(16).toUpperCase()}`)
    .replaceAll("%", "~")
}

/** What `segment` was given, or `null` for a segment it never wrote. */
export function unsegment(text: string): string | null {
  if (text === "~") return ""
  if (text === "" || text.includes("%") || text.includes(".")) return null
  try {
    return decodeURIComponent(text.replaceAll("~", "%"))
  } catch {
    return null
  }
}

/** A table's segment: its schema and its name around one dot, or the name alone where the engine has no schema. */
export function tableSegment(schema: string | undefined, name: string): string {
  return schema ? `${segment(schema)}.${segment(name)}` : segment(name)
}

export function parseTableSegment(text: string): { schema: string; name: string } | null {
  const parts = text.split(".")
  if (parts.length > 2) return null
  const name = unsegment(parts[parts.length - 1])
  const schema = parts.length === 2 ? unsegment(parts[0]) : ""
  if (name === null || schema === null || name === "") return null
  return { schema, name }
}

type Params = Record<string, string | null | undefined>

function withQuery(path: string, params?: Params): string {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(params ?? {})) if (value) query.set(key, value)
  const text = query.toString()
  return text ? `${path}?${text}` : path
}

const base = (id: number | string) => `/databases/${id}/performance`

/** A statement's page. `key` is its key in the list (`statementKeys`), not always the engine's id alone. */
export function statementHref(id: number | string, key: string, params?: Params): string {
  return withQuery(`${base(id)}/statements/${segment(key)}`, params)
}

export function tableHref(
  id: number | string,
  schema: string | undefined,
  name: string,
  params?: Params,
): string {
  return withQuery(`${base(id)}/tables/${tableSegment(schema, name)}`, params)
}

export function sessionHref(id: number | string, pid: string, params?: Params): string {
  return withQuery(`${base(id)}/sessions/${segment(pid)}`, params)
}

type Query = { get: (name: string) => string | null }

/**
 * Where an address that opened one of these in a sheet goes now, or `null`
 * when it opened none.
 *
 * Until 0.8.7 a statement, a table and a session were each a panel over
 * their list, named by a key of the list's own address:
 * `?view=statements&statement=…`, `?view=history&statement=…`,
 * `?view=tables&object=<schema>/<name>` and `?view=sessions&session=…`. Those
 * are in bookmarks, in boards and in what the assistant was told, so each
 * still lands on what it named. What narrowed or ordered the list goes with
 * it, since the page's rail is that list.
 */
export function formerSheetHref(
  id: number | string,
  /** The view the address opens: the one it names, or the engine's first. */
  view: string | undefined,
  query: Query,
): string | null {
  const statement = query.get("statement")
  if (statement && (view === "statements" || view === "history")) {
    return statementHref(id, statement, {
      tab: view === "history" ? "history" : null,
      sort: view === "statements" ? query.get("sort") : null,
    })
  }
  const object = query.get("object")
  if (object && view === "tables") {
    const at = object.indexOf("/")
    if (at < 0) return null
    try {
      return tableHref(
        id,
        decodeURIComponent(object.slice(0, at)),
        decodeURIComponent(object.slice(at + 1)),
        { scope: query.get("scope") },
      )
    } catch {
      return null
    }
  }
  const session = query.get("session")
  if (session && view === "sessions") {
    return sessionHref(id, session, { state: query.get("state") })
  }
  return null
}
