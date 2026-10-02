/**
 * The page's own keys of the address, and what to make of the router arriving
 * somewhere the reader did not last ask for.
 *
 * A write to the address reaches the router a moment after it is made, and
 * the router lands writes in the order they were sent. So two writes made
 * close together can end on the first of them: press Structure and, before
 * the address has caught up, Data — the second write asks for the address the
 * router still shows, nothing is sent for it, and the first one then lands
 * and stays. The same takes an applied filter that was removed at once.
 *
 * The section's context is what should keep the last write (the request is in
 * this area's contract). Until it does, this is the arithmetic that lets the
 * page do it for its own keys: it remembers where each of its writes was
 * heading, and when the router lands on one that has since been overtaken, it
 * knows that landing for what it is — late — and not the reader going there.
 */

/** Every key of the address the table editor reads or writes. */
export const ADDRESS_KEYS = [
  "schema",
  "table",
  "view",
  "filters",
  "where",
  "match",
  "sort",
  "page",
] as const

export type Address = Record<(typeof ADDRESS_KEYS)[number], string>

/** A write is forgotten after this long: the router dropped it, and nothing is still on its way. */
export const FLIGHT_MS = 10_000

export interface Flight {
  /** Where the page's last write leads, until the router shows it. */
  want: Address | null
  /** Where its writes that have not landed lead, the overtaken ones among them. */
  flying: readonly string[]
  /** When the last write was made. */
  at: number
}

export const SETTLED: Flight = { want: null, flying: [], at: 0 }

/** The page's keys as an address states them; `read` answers `""` for a key that is absent. */
export function addressOf(read: (key: string) => string): Address {
  return Object.fromEntries(ADDRESS_KEYS.map((key) => [key, read(key)])) as Address
}

/** Two addresses with the same key say the same thing. */
export function addressKey(address: Address): string {
  return JSON.stringify(ADDRESS_KEYS.map((key) => address[key]))
}

/** An address as the write that leads to it: every key named, the empty ones cleared. */
export function addressParams(address: Address): Record<string, string | null> {
  return Object.fromEntries(ADDRESS_KEYS.map((key) => [key, address[key] || null]))
}

/** `address` after a write: a key named `null` or `""` is cleared, one not named stays. */
export function written(
  address: Address,
  params: Readonly<Record<string, string | null | undefined>>,
): Address {
  const next = { ...address }
  for (const key of ADDRESS_KEYS) {
    if (key in params && params[key] !== undefined) next[key] = params[key] ?? ""
  }
  return next
}

/**
 * The page wrote `params`. `shown` is the address as the page reads it (its
 * earlier writes included) and `router` the key of the one the router shows.
 */
export function wrote(
  flight: Flight,
  shown: Address,
  router: string,
  params: Readonly<Record<string, string | null | undefined>>,
  now: number,
): Flight {
  const live = flight.want !== null && now - flight.at <= FLIGHT_MS
  const want = written(live && flight.want ? flight.want : shown, params)
  const key = addressKey(want)
  const flying = live ? flight.flying : []
  // The router already shows it, so nothing will be sent for it. With no
  // earlier write still on its way there is nothing left to wait for.
  if (key === router) return flying.length === 0 ? SETTLED : { want, flying, at: now }
  return { want, flying: flying.includes(key) ? flying : [...flying, key], at: now }
}

/**
 * The router shows another address. `rewrite` is the address to write again
 * when what landed is one of the page's own writes that a later one overtook;
 * otherwise the page follows the router — its last write arrived, or the
 * reader went somewhere by a link, Back or Forward.
 */
export function landed(
  flight: Flight,
  router: string,
  now: number,
): { flight: Flight; rewrite: Address | null } {
  if (!flight.want || now - flight.at > FLIGHT_MS) return { flight: SETTLED, rewrite: null }
  if (addressKey(flight.want) === router) return { flight: SETTLED, rewrite: null }
  if (!flight.flying.includes(router)) return { flight: SETTLED, rewrite: null }
  return {
    flight: { ...flight, flying: flight.flying.filter((key) => key !== router) },
    rewrite: flight.want,
  }
}
