import type { Icon } from "@/components/icons"
import type { NotificationChannel } from "@/lib/types"

/**
 * What an alerts page knows about its kinds of rule, and nothing else: the
 * proxy's and the host's Alerts pages draw the same tile, cards, picture and
 * form (`kit.tsx`), and differ only in this table and in how each kind's
 * parameters read. The kit is generic over the page's union of kinds, so a
 * proxy kind cannot be handed to the host's table.
 */

/** One kind of rule, as every part of the kit names and draws it. */
export type AlertKindSpec = {
  label: string
  /** A few words under the name on the form's card. */
  hint: string
  /** The sentence behind them, as a card's tooltip; the hint where there is none. */
  detail?: string
  /** What an add card says the rule would be added with; the hint where there is none. */
  fact?: string
  /** The kind's own glyph: in the tile's corner on a product, or the tile itself. */
  icon: Icon
}

/** What a rule watches, as the picture's left column groups the kinds. */
export type AlertArea<K extends string> = {
  key: string
  title: string
  /**
   * The product the area is drawn as, where it is one: a fixed id, or one
   * read from the page (the proxy's engine is nginx or Caddy).
   */
  product?: string | ((engine: string | undefined) => string | undefined)
  fallback: Icon
  kinds: readonly K[]
}

/** A page's kinds: each one's words, the order they are offered in, the picture's areas. */
export type AlertKinds<K extends string> = {
  kinds: Record<K, AlertKindSpec>
  /** The order the form and the add cards offer them in. */
  order: readonly K[]
  areas: readonly AlertArea<K>[]
}

/** A rule, in the fields the kit reads. */
export type AlertRuleView<K extends string> = { kind: K; enabled: boolean; channels: number[] }

/** Whether a string from the server is one of the page's kinds (history outlives a kind). */
export function isAlertKind<K extends string>(kinds: AlertKinds<K>, kind: string): kind is K {
  return Object.prototype.hasOwnProperty.call(kinds.kinds, kind)
}

/** The area a kind is drawn in; the first where none names it. */
export function areaOf<K extends string>(kinds: AlertKinds<K>, kind: K): AlertArea<K> {
  return kinds.areas.find((area) => area.kinds.includes(kind)) ?? kinds.areas[0]
}

/** The product an area is drawn as, for this page's engine. */
export function areaProduct<K extends string>(
  area: AlertArea<K>,
  engine?: string,
): string | undefined {
  return typeof area.product === "function" ? area.product(engine) : area.product
}

/** The product a kind of rule is about: Let's Encrypt for a certificate, Docker for a container. */
export function kindProduct<K extends string>(
  kinds: AlertKinds<K>,
  kind: K,
  engine?: string,
): string | undefined {
  return areaProduct(areaOf(kinds, kind), engine)
}

/** Whether an enabled rule tells this channel: one with no channels named tells every enabled one. */
export function reaches(rules: AlertRuleView<string>[], channel: NotificationChannel): boolean {
  return (
    channel.enabled &&
    rules.some(
      (rule) => rule.enabled && (rule.channels.length === 0 || rule.channels.includes(channel.id)),
    )
  )
}

/** Firing and holding subjects, a muted one in neither. */
export function alertCounts(subjects: { state: string; muted: boolean }[]): {
  firing: number
  holding: number
} {
  let firing = 0
  let holding = 0
  for (const subject of subjects) {
    if (subject.muted) continue
    if (subject.state === "firing") firing++
    else if (subject.state === "pending") holding++
  }
  return { firing, holding }
}
