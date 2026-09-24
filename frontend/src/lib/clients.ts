/**
 * What is on the other end of a session or a key, read from what the server
 * was told about it: a user agent, an address, the name a key was given.
 *
 * The account pages draw each of these as the product it is — Chrome on
 * macOS as Chrome's mark over Apple's, an address on the tailnet as
 * Tailscale's — so a list of sessions answers "is that my phone or somebody
 * else" before a word of it is read. Every function here returns no product
 * for a thing it cannot name, and the caller keeps a glyph: a guessed logo is
 * the drawing lying about the row (§14).
 */

export type Device = "desktop" | "phone" | "tablet" | "program"

export type ClientAgent = {
  /** What a person would call it: "Chrome", "curl". */
  client: string
  /** A `product-logo` id for the client, when it is one the product carries. */
  product?: string
  /** "macOS", "Android"; empty when the agent does not say. */
  os: string
  osProduct?: string
  device: Device
}

/**
 * Order matters: every Chromium browser also says `Chrome/`, and Chrome also
 * says `Safari/`, so the specific names are tried before the ones they
 * imitate.
 */
const BROWSERS: [RegExp, string, string?][] = [
  [/EdgA?\/|EdgiOS\/|Edge\//, "Edge", "edge"],
  [/OPR\/|OPiOS\/|Opera/, "Opera", "opera"],
  [/Vivaldi\//, "Vivaldi", "vivaldi"],
  [/Brave\//, "Brave", "brave"],
  [/SamsungBrowser\//, "Samsung Internet"],
  [/Firefox\/|FxiOS\//, "Firefox", "firefox"],
  [/Chrome\/|CriOS\//, "Chrome", "chrome"],
  [/Safari\//, "Safari", "safari"],
]

/** Things that sign in without a browser: a script, a CLI, an API client. */
const PROGRAMS: [RegExp, string, string?][] = [
  [/^curl\//i, "curl", "curl"],
  [/^Wget\//i, "Wget"],
  [/^Go-http-client\//, "Go", "go"],
  [
    /^python-requests\/|^python-urllib\/|^Python-urllib\/|^python-httpx\/|aiohttp\//i,
    "Python",
    "python",
  ],
  [/^node-fetch\/|^undici|^axios\/|^node\//i, "Node.js", "nodejs"],
  [/^Bun\//, "Bun", "bun"],
  [/^Deno\//, "Deno", "deno"],
  [/^PostmanRuntime\//, "Postman", "postman"],
]

const SYSTEMS: [RegExp, string, string?][] = [
  [/Windows/, "Windows", "windows"],
  [/iPad/, "iPadOS", "apple"],
  [/iPhone|iPod/, "iOS", "apple"],
  [/Android/, "Android", "android"],
  [/CrOS/, "ChromeOS", "chrome"],
  [/Mac OS X|Macintosh/, "macOS", "apple"],
  [/Ubuntu/, "Ubuntu", "ubuntu"],
  [/Fedora/, "Fedora", "fedora"],
  [/Linux|X11/, "Linux", "linux"],
]

export function parseAgent(ua: string): ClientAgent {
  const program = PROGRAMS.find(([pattern]) => pattern.test(ua))
  if (program) return { client: program[1], product: program[2], os: "", device: "program" }

  const browser = BROWSERS.find(([pattern]) => pattern.test(ua))
  const system = SYSTEMS.find(([pattern]) => pattern.test(ua))
  const device: Device =
    /iPad|Tablet/.test(ua) || (/Android/.test(ua) && !/Mobi/.test(ua))
      ? "tablet"
      : /Mobi|iPhone|iPod/.test(ua)
        ? "phone"
        : /^Mozilla\//.test(ua)
          ? "desktop"
          : "program"
  return {
    client: browser?.[1] ?? (ua.split(/[\s/]/)[0] || "Unknown client"),
    product: browser?.[2],
    os: system?.[1] ?? "",
    osProduct: system?.[2],
    device,
  }
}

/**
 * A user agent as a person would say it: "Chrome on macOS", "curl". A table
 * of sessions is read to answer "is that my phone or somebody else", and a
 * 120-character token string does not answer it.
 */
export function describeClient(ua: string) {
  const { client, os } = parseAgent(ua)
  return os ? `${client} on ${os}` : client
}

export type AgentKind = "browser" | "program" | "bot" | "unknown"

/**
 * The crawlers the request log names on their own, drawn as the search engine
 * that sends them. "Other bots" is every other agent that says it is one, and
 * no product.
 */
const BOTS: Record<string, string | undefined> = {
  Googlebot: "google",
  bingbot: "bing",
  "Other bots": undefined,
}

/**
 * A crawler, from a whole user agent — which dresses as a browser
 * (`Mozilla/5.0 (compatible; Googlebot/2.1)`, and a smartphone Googlebot
 * names Chrome and Android too), so `parseAgent` would draw it as one. The two
 * the request log names on their own are the search engine that sends them;
 * any other agent that says it is a bot is a crawler with no mark.
 */
export function crawlerOf(ua: string): { name: string; product?: string } | undefined {
  const named = (["Googlebot", "bingbot"] as const).find((name) => new RegExp(name, "i").test(ua))
  if (named) return { name: named, product: BOTS[named] }
  return /bot\b|crawl|spider/i.test(ua) ? { name: "A crawler" } : undefined
}

/**
 * What the request log's agent family is — the backend's `agentFamily()`
 * reduces a user agent to "Chrome", "curl", "Googlebot", or its first word.
 * A family is not a user agent, so it is read as the product token it would
 * start one with (`Chrome/`), through the same tables `parseAgent` uses.
 * Anything else that is not "Mozilla" is a program's own first word
 * (`okhttp`, `Wget`): a program, but not one with a mark. Bare "Mozilla" is a
 * browser-shaped agent the backend could not name, and so is unknown.
 */
export function agentProduct(family: string): { product?: string; kind: AgentKind } {
  if (family in BOTS) return { product: BOTS[family], kind: "bot" }
  const token = `${family}/`
  const browser = BROWSERS.find(([pattern]) => pattern.test(token))
  if (browser) return { product: browser[2], kind: "browser" }
  const program = PROGRAMS.find(([pattern]) => pattern.test(token))
  if (program) return { product: program[2], kind: "program" }
  return { kind: family && family !== "Mozilla" ? "program" : "unknown" }
}

/**
 * The host a URL, an scp-style Git remote or a bare host names, lowercased
 * and without its user, port or path: `git@github.com:owner/repo` is
 * github.com, `https://token@gitlab.com/x.git` gitlab.com. What a reader
 * types into a Host field is any of these, and a mark that only knew one
 * shape would come and go as the field is filled in.
 */
export function hostOf(hostOrUrl: string) {
  return hostOrUrl
    .trim()
    .toLowerCase()
    .replace(/^[a-z][a-z0-9+.-]*:\/\//, "")
    .replace(/^[^@/]*@/, "")
    .split(/[/:?#]/)[0]
}

/**
 * The product a table names for a host or any domain above it, nearest
 * first: `news.ycombinator.com` is ycombinator.com's, `api.github.com`
 * github.com's.
 */
export function productOfHost(host: string, table: Record<string, string>) {
  for (let name = host; name.includes("."); name = name.slice(name.indexOf(".") + 1)) {
    if (table[name]) return table[name]
  }
  return undefined
}

/** The sites a visitor arrives from often enough to be worth their mark. */
const REFERERS: Record<string, string> = {
  "github.com": "github",
  "gitlab.com": "gitlab",
  "bing.com": "bing",
  "duckduckgo.com": "duckduckgo",
  "ycombinator.com": "ycombinator",
  "reddit.com": "reddit",
  "t.co": "x",
  "x.com": "x",
  "twitter.com": "x",
  "linkedin.com": "linkedin",
  "lnkd.in": "linkedin",
  "facebook.com": "facebook",
  "discord.com": "discord",
  "slack.com": "slack",
  "t.me": "telegram",
  "telegram.org": "telegram",
  "claude.ai": "claude",
}

/**
 * The site a request came from, as the product it is — the request log's
 * referer hosts, or a full referer from a row. Google answers on a domain
 * per country (`google.co.uk`, `google.com.br`), so it is matched by shape
 * rather than listed.
 */
export function refererProduct(host: string): string | undefined {
  const name = hostOf(host)
  if (/(^|\.)google\.(com?\.)?[a-z]{2,3}$/.test(name)) return "google"
  return productOfHost(name, REFERERS)
}

export type NetworkKind = "tailscale" | "local" | "server" | "internet"

export type Network = {
  kind: NetworkKind
  /** Where the address is, in words: "Tailscale", "local network". */
  label: string
  product?: string
}

function ipv4(address: string): number[] | undefined {
  const parts = address.split(".")
  if (parts.length !== 4) return undefined
  const octets = parts.map(Number)
  return octets.every((n, i) => /^\d{1,3}$/.test(parts[i]) && n <= 255) ? octets : undefined
}

/**
 * Where an address sits, as far as the address itself says. Tailscale hands
 * out 100.64.0.0/10 and fd7a:115c:a1e0::/48, and on a dashboard that answers
 * on a tailnet an address in either is somebody on it; the private ranges are
 * a LAN or a container network; loopback is this machine. Everything else is
 * the internet — which is a fact about the session, not a verdict on it.
 */
export function networkOf(ip: string): Network {
  const address = ip
    .trim()
    .toLowerCase()
    .replace(/^::ffff:/, "")
  const v4 = ipv4(address)
  if (v4) {
    const [a, b] = v4
    if (a === 100 && b >= 64 && b <= 127) {
      return { kind: "tailscale", label: "Tailscale", product: "tailscale" }
    }
    if (a === 127) return { kind: "server", label: "this server" }
    // RFC 1918 reserves 172.16 through 172.31 and nothing either side:
    // 172.217 is Google. 169.254 is link-local and 0/8 "this network" —
    // neither leaves the machine's own segment.
    if (
      a === 0 ||
      a === 10 ||
      (a === 172 && b >= 16 && b <= 31) ||
      (a === 192 && b === 168) ||
      (a === 169 && b === 254)
    ) {
      return { kind: "local", label: "local network" }
    }
    return { kind: "internet", label: "internet" }
  }
  if (address.startsWith("fd7a:115c:a1e0:")) {
    return { kind: "tailscale", label: "Tailscale", product: "tailscale" }
  }
  if (address === "::1") return { kind: "server", label: "this server" }
  if (/^f[cd][0-9a-f]{2}:/.test(address) || /^fe[89ab][0-9a-f]:/.test(address)) {
    return { kind: "local", label: "local network" }
  }
  return { kind: "internet", label: "internet" }
}

/**
 * The words in a key's name that say where it lives. Minting a key asks for
 * exactly that — "a CI pipeline, a cron job, a laptop" — so `github-actions`
 * is a key GitHub holds, and drawing it with GitHub's mark is reading the
 * name, not guessing. A name with none of these (`backup-cron`, `laptop`)
 * keeps the key glyph.
 */
const KEY_WORDS: Record<string, string> = {
  github: "github",
  gh: "github",
  actions: "github",
  gitlab: "gitlab",
  gitea: "gitea",
  jenkins: "jenkins",
  ansible: "ansible",
  terraform: "terraform",
  tofu: "terraform",
  opentofu: "terraform",
  kubernetes: "kubernetes",
  k8s: "kubernetes",
  kubectl: "kubernetes",
  helm: "kubernetes",
  homeassistant: "home-assistant",
  hass: "home-assistant",
  n8n: "n8n",
  grafana: "grafana",
  prometheus: "prometheus",
  homepage: "homepage",
  uptimekuma: "uptime-kuma",
  kuma: "uptime-kuma",
  beszel: "beszel",
  healthchecks: "healthchecks",
  ntfy: "ntfy",
  gotify: "gotify",
  portainer: "portainer",
  postman: "postman",
  python: "python",
  node: "nodejs",
  nodejs: "nodejs",
  bun: "bun",
  deno: "deno",
  curl: "curl",
  docker: "docker",
  claude: "claude",
}

export function keyProduct(name: string): string | undefined {
  const words = name
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter(Boolean)
  return wordsProduct(words, KEY_WORDS)
}

/**
 * The first word a table names a product for, reading each word with the
 * one after it first, so `home-assistant` is not read as a thing named
 * "home". A key's name, a variable's and a host's labels are all read this
 * way.
 */
export function wordsProduct(words: string[], table: Record<string, string>) {
  for (let i = 0; i < words.length; i++) {
    const id = table[words[i] + (words[i + 1] ?? "")] ?? table[words[i]]
    if (id) return id
  }
  return undefined
}
