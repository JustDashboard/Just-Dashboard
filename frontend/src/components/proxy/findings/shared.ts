import type { Finding } from "@/components/finding-list"

/**
 * What is wrong with the proxy, as one list.
 *
 * The overview used to answer "needs attention" with every certificate past
 * its warning window and every socket bound to a wildcard address — the
 * second of which is sshd and nginx on every server there is, so the list
 * was never empty and stopped meaning anything. These are the conditions an
 * operator would actually act on, each with what was measured, what it
 * means and what to do, in the shape the host overview and the security
 * pages already render.
 */
export type ProxyFinding = Finding & {
  href: string
  /**
   * What a snooze remembers the finding by, when its title and detail carry
   * something that moves on its own — days left, a timing — and would bring
   * a finding put aside "until it changes" back the next time it is read.
   * See findingFingerprint.
   */
  fingerprint?: string
  /** A fix the overview can carry out in place, for an operator. */
  remedy?: ProxyRemedy
}

/**
 * The fixes a finding can offer beside the page it opens. Each is a call the
 * page it points to already makes: enabling a site, and turning on the timer
 * certbot installed but nothing started.
 */
export type ProxyRemedy =
  { kind: "enable-site"; site: string } | { kind: "start-unit"; unit: string }

/**
 * The ports the security catalogue treats as a database or a control plane —
 * the same judgement the stream form applies before forwarding one.
 */
export const DANGEROUS_PORTS: Record<number, string> = {
  5432: "PostgreSQL",
  3306: "MySQL",
  6379: "Redis",
  27017: "MongoDB",
  11211: "memcached",
  9200: "Elasticsearch",
  2375: "the Docker API",
}
