/**
 * A saved connection string, changed in place.
 *
 * Editing a connection used to mean rebuilding its string from five fields,
 * which wrote a string with none of the options the saved one carried: a
 * PostgreSQL connection that required TLS came back with `sslmode=disable`, a
 * MongoDB one lost its `replicaSet`, a MySQL one its `parseTime`. The server
 * replaces the stored string whole, so whatever the form did not know about
 * was gone.
 *
 * So the string is never rebuilt. It is read for the five things a form
 * shows — host, port, account, password, database — and a change replaces
 * exactly the characters that say that one thing. Everything else, option for
 * option and byte for byte, is what was saved.
 *
 * The shape is read off the string itself, not off a driver's name: a URL
 * (`scheme://account@host:port/database?options`), the Go MySQL driver's own
 * form (`account:password@tcp(host:port)/database?options`), or a file path.
 */

export type DsnShape = "url" | "native" | "path"

export type DsnParts = {
  shape: DsnShape
  /** A URL's scheme, without the `://`. */
  scheme: string
  user: string
  password: string
  /** One host. Empty with `hosts` when the string names several. */
  host: string
  port: string
  /** Several addresses as written (`a:27017,b:27017`): shown, never split. */
  hosts: string
  database: string
  /** What is carried besides, as `name=value` — every one of them is kept. */
  options: string[]
}

export type DsnChange = Partial<Pick<DsnParts, "host" | "port" | "user" | "password" | "database">>

type Url = {
  scheme: string
  userinfo: string | undefined
  authority: string
  path: string
  query: string
  fragment: string
}

const URL_FORM = /^([a-z][a-z0-9+.-]*):\/\/([^/?#]*)([^?#]*)(\?[^#]*)?(#.*)?$/i
const NATIVE_FORM = /^(.*)@(tcp|unix)\(([^)]*)\)\/([^?]*)(\?.*)?$/

function decode(text: string): string {
  try {
    return decodeURIComponent(text)
  } catch {
    // A stray `%` is a literal percent sign in somebody's password.
    return text
  }
}

function splitUrl(dsn: string): Url | undefined {
  const match = URL_FORM.exec(dsn)
  if (!match) return undefined
  const [, scheme, authority, path, query = "", fragment = ""] = match
  // The account ends at the last `@` of the authority: a password typed
  // unescaped may hold one of its own.
  const at = authority.lastIndexOf("@")
  return {
    scheme,
    userinfo: at < 0 ? undefined : authority.slice(0, at),
    authority: at < 0 ? authority : authority.slice(at + 1),
    path,
    query,
    fragment,
  }
}

function joinUrl(url: Url): string {
  const account = url.userinfo === undefined ? "" : `${url.userinfo}@`
  return `${url.scheme}://${account}${url.authority}${url.path}${url.query}${url.fragment}`
}

/** `host:port`, with an IPv6 literal in its brackets. */
function splitAddress(address: string): { host: string; port: string } {
  const bracketed = /^\[([^\]]*)\](?::(\d*))?$/.exec(address)
  if (bracketed) return { host: bracketed[1], port: bracketed[2] ?? "" }
  const cut = address.lastIndexOf(":")
  // More than one colon and no brackets is a bare IPv6 address, not a port.
  if (cut < 0 || address.indexOf(":") !== cut) return { host: address, port: "" }
  return { host: address.slice(0, cut), port: address.slice(cut + 1) }
}

function joinAddress(host: string, port: string): string {
  const name = host.includes(":") && !host.startsWith("[") ? `[${host}]` : host
  return port ? `${name}:${port}` : name
}

function splitQuery(query: string): string[] {
  return query.replace(/^\?/, "").split("&").filter(Boolean)
}

/** The parameter a string names its database with, when it does so in its options. */
function databaseParam(pairs: string[]): number {
  return pairs.findIndex((pair) => /^database=/i.test(pair))
}

/** Whether the database is said in the options rather than in the path. */
function namedInOptions(url: Url): boolean {
  return url.path.replace(/^\//, "") === "" && databaseParam(splitQuery(url.query)) >= 0
}

/** What a saved string says, for a form to show. */
export function readDsn(dsn: string): DsnParts {
  const raw = dsn.trim()
  const native = NATIVE_FORM.exec(raw)
  if (native && !URL_FORM.test(raw)) {
    const [, account, , address, database, query = ""] = native
    const cut = account.indexOf(":")
    const { host, port } = splitAddress(address)
    return {
      shape: "native",
      scheme: "",
      user: cut < 0 ? account : account.slice(0, cut),
      password: cut < 0 ? "" : account.slice(cut + 1),
      host,
      port,
      hosts: "",
      database,
      options: splitQuery(query),
    }
  }
  const url = splitUrl(raw)
  if (!url) {
    return {
      shape: "path",
      scheme: "",
      user: "",
      password: "",
      host: "",
      port: "",
      hosts: "",
      database: raw,
      options: [],
    }
  }
  const cut = url.userinfo?.indexOf(":") ?? -1
  const several = url.authority.includes(",")
  const { host, port } = several ? { host: "", port: "" } : splitAddress(url.authority)
  const pairs = splitQuery(url.query)
  const inOptions = namedInOptions(url)
  const named = inOptions ? pairs[databaseParam(pairs)] : undefined
  return {
    shape: "url",
    scheme: url.scheme,
    user: decode(cut < 0 ? (url.userinfo ?? "") : (url.userinfo ?? "").slice(0, cut)),
    password: cut < 0 ? "" : decode((url.userinfo ?? "").slice(cut + 1)),
    host,
    port,
    hosts: several ? url.authority : "",
    database: named ? decode(named.slice(named.indexOf("=") + 1)) : decode(url.path.slice(1)),
    options: [...pairs.filter((pair) => pair !== named), ...(url.fragment ? [url.fragment] : [])],
  }
}

/**
 * The saved string with the named parts changed and nothing else touched. A
 * part whose new value is what the string already says is left as written, so
 * a form that sends back every field changes only what was edited.
 */
export function patchDsn(dsn: string, change: DsnChange): string {
  const raw = dsn.trim()
  const now = readDsn(raw)
  const differs = <K extends keyof DsnChange>(key: K): boolean =>
    change[key] !== undefined && change[key] !== now[key]

  if (now.shape === "path") return differs("database") ? (change.database ?? "").trim() : raw

  if (now.shape === "native") {
    const match = NATIVE_FORM.exec(raw)
    if (!match) return raw
    const [, account, network, address, database, query = ""] = match
    const user = differs("user") ? (change.user ?? "") : now.user
    const password = differs("password") ? (change.password ?? "") : now.password
    const nextAccount =
      differs("user") || differs("password")
        ? password || account.includes(":")
          ? `${user}:${password}`
          : user
        : account
    const nextAddress =
      differs("host") || differs("port")
        ? joinAddress(change.host ?? now.host, change.port ?? now.port)
        : address
    const nextDatabase = differs("database") ? (change.database ?? "") : database
    return `${nextAccount}@${network}(${nextAddress})/${nextDatabase}${query}`
  }

  const url = splitUrl(raw)
  if (!url) return raw
  if (differs("user") || differs("password")) {
    const info = url.userinfo ?? ""
    const cut = info.indexOf(":")
    // The half that was not edited is kept exactly as it was written.
    const user = differs("user")
      ? encodeURIComponent(change.user ?? "")
      : cut < 0
        ? info
        : info.slice(0, cut)
    const password = differs("password")
      ? encodeURIComponent(change.password ?? "")
      : cut < 0
        ? ""
        : info.slice(cut + 1)
    url.userinfo = password ? `${user}:${password}` : user || undefined
  }
  if ((differs("host") || differs("port")) && !now.hosts) {
    url.authority = joinAddress(change.host ?? now.host, change.port ?? now.port)
  }
  if (differs("database")) {
    const database = change.database ?? ""
    if (namedInOptions(url)) {
      const pairs = splitQuery(url.query)
      const at = databaseParam(pairs)
      const name = pairs[at].slice(0, pairs[at].indexOf("="))
      pairs[at] = `${name}=${encodeURIComponent(database)}`
      url.query = `?${pairs.join("&")}`
    } else {
      url.path = `/${encodeURIComponent(database)}`
    }
  }
  return joinUrl(url)
}

/**
 * A string with whatever stands where a password would hidden, for showing
 * what will be saved.
 */
export function maskedDsn(dsn: string, mask = "••••••"): string {
  const now = readDsn(dsn)
  if (!now.password) return dsn.trim()
  if (now.shape === "native") {
    return dsn.trim().replace(/^([^:@]*:).*(@(?:tcp|unix)\()/, `$1${mask}$2`)
  }
  const url = splitUrl(dsn.trim())
  if (!url || url.userinfo === undefined) return dsn.trim()
  const cut = url.userinfo.indexOf(":")
  url.userinfo = `${cut < 0 ? url.userinfo : url.userinfo.slice(0, cut)}:${mask}`
  return joinUrl(url)
}
