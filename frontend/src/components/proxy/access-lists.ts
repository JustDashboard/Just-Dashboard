import type { AccessListSpec, AccessRun } from "@/lib/proxy/types-access-lists"

/**
 * An access list's addresses the way nginx's access module reads them, so
 * the page can say — before anything is saved — whether a list lets in the
 * person editing it. A list that refuses them locks them out of every site
 * that includes it at once.
 *
 * The server checks and writes every address itself; this only has to agree
 * with it about what is an address, and with nginx about who matches.
 */

type Family = 4 | 6

/** An address as 16-bit words: two for IPv4, eight for IPv6. */
type Address = { family: Family; words: number[] }

type Range = Address & { bits: number }

function parseV4(text: string): number[] | null {
  const parts = text.split(".")
  if (parts.length !== 4) return null
  const bytes: number[] = []
  for (const part of parts) {
    // No leading zeros: "010" is octal to some parsers and refused by Go's.
    if (!/^(0|[1-9][0-9]{0,2})$/.test(part) || Number(part) > 255) return null
    bytes.push(Number(part))
  }
  return [(bytes[0] << 8) | bytes[1], (bytes[2] << 8) | bytes[3]]
}

function parseV6(text: string): number[] | null {
  if (text.includes("%")) return null
  let head = text
  let tail: number[] = []
  const lastColon = text.lastIndexOf(":")
  if (lastColon >= 0 && text.slice(lastColon + 1).includes(".")) {
    const v4 = parseV4(text.slice(lastColon + 1))
    if (!v4) return null
    tail = v4
    head = text.slice(0, lastColon + 1) + "0:0"
  }
  const halves = head.split("::")
  if (halves.length > 2) return null
  const split = (part: string) => (part === "" ? [] : part.split(":"))
  const left = split(halves[0])
  const right = halves.length === 2 ? split(halves[1]) : []
  const groups = left.length + right.length
  if (halves.length === 1 ? groups !== 8 : groups > 7) return null
  const words: number[] = []
  for (const word of [...left, ...Array<string>(8 - groups).fill("0"), ...right]) {
    if (!/^[0-9a-fA-F]{1,4}$/.test(word)) return null
    words.push(parseInt(word, 16))
  }
  if (tail.length > 0) words.splice(6, 2, ...tail)
  return words
}

/** An IPv4 or IPv6 address, or null. */
export function parseAddress(text: string): Address | null {
  const trimmed = text.trim()
  if (trimmed.includes(":")) {
    const words = parseV6(trimmed)
    return words ? { family: 6, words } : null
  }
  const words = parseV4(trimmed)
  return words ? { family: 4, words } : null
}

function width(family: Family) {
  return family === 4 ? 32 : 128
}

function parseRange(text: string): Range | null {
  const [address, bits, ...rest] = text.trim().split("/")
  if (rest.length > 0) return null
  const parsed = parseAddress(address)
  if (!parsed) return null
  if (bits === undefined) return { ...parsed, bits: width(parsed.family) }
  if (!/^(0|[1-9][0-9]{0,2})$/.test(bits) || Number(bits) > width(parsed.family)) return null
  return { ...parsed, bits: Number(bits) }
}

/** The words with every bit past the first `bits` cleared. */
function masked(words: number[], bits: number) {
  return words.map((word, i) => {
    const kept = Math.min(16, Math.max(0, bits - 16 * i))
    return word & ((0xffff << (16 - kept)) & 0xffff)
  })
}

function sameWords(a: number[], b: number[]) {
  return a.length === b.length && a.every((word, i) => word === b[i])
}

function formatV4(words: number[]) {
  return [words[0] >> 8, words[0] & 0xff, words[1] >> 8, words[1] & 0xff].join(".")
}

function isV4Mapped(words: number[]) {
  return words.length === 8 && words.slice(0, 5).every((word) => word === 0) && words[5] === 0xffff
}

/** An address the way Go's netip writes it, which is how the server writes it. */
function formatAddress({ family, words }: Address) {
  if (family === 4) return formatV4(words)
  if (isV4Mapped(words)) return `::ffff:${formatV4(words.slice(6))}`
  // The longest run of two or more zero words becomes "::", the first of
  // equal runs (RFC 5952).
  let best = { at: -1, length: 1 }
  for (let i = 0; i < 8;) {
    let j = i
    while (j < 8 && words[j] === 0) j++
    if (j - i > best.length) best = { at: i, length: j - i }
    i = j > i ? j : i + 1
  }
  const hex = words.map((word) => word.toString(16))
  if (best.at < 0) return hex.join(":")
  return `${hex.slice(0, best.at).join(":")}::${hex.slice(best.at + best.length).join(":")}`
}

/**
 * Why an entry would be refused, in the server's words, or null for an
 * address or range the server takes. A range with bits past its length is
 * refused rather than masked: nginx would take 10.0.0.5/8 as 10.0.0.0/8, and
 * the list would read one thing and do another.
 */
export function checkEntry(text: string): string | null {
  const entry = text.trim()
  if (entry === "all") {
    return "Write addresses or ranges rather than all — an allow list is already closed with deny all"
  }
  const range = parseRange(entry)
  if (!range) return `"${entry}" is not an IP address or a range like 10.0.0.0/8`
  const network = masked(range.words, range.bits)
  if (entry.includes("/") && !sameWords(network, range.words)) {
    const written = formatAddress({ family: range.family, words: network })
    return `${entry} has bits set past its /${range.bits} — the range is ${written}/${range.bits}`
  }
  return null
}

/** Whether two entries name the same range; an address is its own /32 or /128. */
export function sameEntry(a: string, b: string) {
  const x = parseRange(a)
  const y = parseRange(b)
  return Boolean(
    x && y && x.family === y.family && x.bits === y.bits && sameWords(x.words, y.words),
  )
}

/**
 * What a list does for a visitor at `address`:
 * - "allowed": in, with no password;
 * - "password": in with a login from the list's password file;
 * - "refused": not in at all;
 * - "unknown": the address, or an entry, could not be read.
 */
export type AccessVerdict = "allowed" | "password" | "refused" | "unknown"

type Rule = { deny: boolean; range: Range | "all" }

/**
 * nginx keeps an IPv4 and an IPv6 list of rules, "all" in both, and checks a
 * client against its own family's in order: the first match decides, and no
 * match lets it through. An IPv4 address carried in IPv6 (::ffff:a.b.c.d) is
 * checked as IPv4 wherever there are IPv4 rules.
 */
function addressMatch(rules: Rule[], client: Address): "allow" | "deny" | null {
  const of = (family: Family) =>
    rules.filter((rule) => rule.range === "all" || rule.range.family === family)
  let family = client.family
  let words = client.words
  let list = of(family)
  if (family === 6 && isV4Mapped(words) && of(4).length > 0) {
    family = 4
    words = words.slice(6)
    list = of(4)
  }
  for (const rule of list) {
    if (rule.range === "all") return rule.deny ? "deny" : "allow"
    const { range } = rule
    if (sameWords(masked(words, range.bits), masked(range.words, range.bits))) {
      return rule.deny ? "deny" : "allow"
    }
  }
  return null
}

function addressPasses(rules: Rule[], client: Address) {
  return addressMatch(rules, client) !== "deny"
}

export function accessFor(spec: AccessListSpec, address: string): AccessVerdict {
  const client = parseAddress(address)
  if (!client) return "unknown"
  const rules: Rule[] = []
  for (const [entries, deny] of [
    [spec.deny, true],
    [spec.allow, false],
  ] as const) {
    for (const entry of entries) {
      const range = checkEntry(entry) === null ? parseRange(entry) : null
      if (!range) return "unknown"
      rules.push({ deny, range })
    }
  }
  if (spec.allow.length > 0) rules.push({ deny: true, range: "all" })
  const passes = addressPasses(rules, client)
  if (!spec.authFile) return passes ? "allowed" : "refused"
  if (spec.satisfy === "any" && spec.allow.length > 0) return passes ? "allowed" : "password"
  return passes ? "password" : "refused"
}

/**
 * What a site's block does for a visitor at `address`, with every list and
 * every rule of its own it reads there in one run, the way nginx joins them.
 * A list judged alone can read as letting the visitor in while a list read
 * before it, with its closing deny all, has already turned them away.
 */
export function accessForRun(run: AccessRun, address: string): AccessVerdict {
  const client = parseAddress(address)
  if (!client) return "unknown"
  const rules: Rule[] = []
  let password = false
  let satisfy = "all"
  for (const source of run.sources) {
    for (const rule of source.rules) {
      if (rule.address === "all") {
        rules.push({ deny: rule.deny, range: "all" })
        continue
      }
      const range = parseRange(rule.address)
      if (!range) return "unknown"
      rules.push({ deny: rule.deny, range })
    }
    password ||= Boolean(source.password)
    if (source.satisfy) satisfy = source.satisfy
  }
  const match = addressMatch(rules, client)
  if (!password) return match === "deny" ? "refused" : "allowed"
  // With satisfy any, only an address a rule allows gets in without a login.
  if (satisfy === "any" && rules.length > 0) return match === "allow" ? "allowed" : "password"
  return match === "deny" ? "refused" : "password"
}

/** The denied entry that takes in `entry`, if one does. */
function deniedBy(entry: string, deny: string[]) {
  const allowed = parseRange(entry)
  if (!allowed) return undefined
  return deny.find((denied) => {
    const range = parseRange(denied)
    return (
      range !== null &&
      range.family === allowed.family &&
      range.bits <= allowed.bits &&
      sameWords(masked(allowed.words, range.bits), masked(range.words, range.bits))
    )
  })
}

/**
 * Why the allow and deny lists together would be refused, in the server's
 * words: denials are written first, so an allowed address a denial takes in
 * is never let in while the list reads as allowing it.
 */
export function checkOverlap(allow: string[], deny: string[]): string | null {
  for (const entry of allow) {
    const denied = deniedBy(entry, deny)
    if (denied === undefined) continue
    return sameEntry(entry, denied)
      ? `${entry} is also denied, and denials are checked first — it would never be let in`
      : `${entry} is inside the denied ${denied}, and denials are checked first — it would never be let in`
  }
  return null
}

/**
 * Why the server would refuse a login prompt, or null. Counted in
 * characters, as the server counts them: a Cyrillic letter is one, not two.
 */
export function checkRealm(realm: string): string | null {
  const prompt = realm.trim()
  if (/["\\$;{}\u0000-\u001f\u007f]/.test(prompt)) {
    return "The login prompt may not contain double quotes, backslashes, $, semicolons, braces or control characters."
  }
  if (prompt.toLowerCase() === "off") {
    return 'A login prompt of "off" turns the password off — choose other words.'
  }
  const length = [...prompt].length
  if (length > 100) return `The login prompt is ${length} characters; keep it to 100.`
  return null
}
