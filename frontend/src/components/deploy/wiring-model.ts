import type { DeploymentDomainRoute, DeploymentRuntimeService } from "@/lib/types"

/**
 * How the Overview's wiring reads a project of many parts, kept apart from
 * the picture so it is unit-tested (wiring-model.test.js): the Runtime node's
 * containers and the Domains node's names.
 *
 * A stack of eight apps named its eight containers in full under Runtime, two
 * lines of `jd-e480-bazarr-1` and its like, each app wrapped in the project's
 * prefix and a number. The node now names three by their Compose service and
 * counts the rest into the Runtime page.
 */

/** The most containers the Runtime node names before it counts the rest. */
export const LISTED_SERVICES = 4

/**
 * What the Runtime node calls a container: in a release of several services,
 * the Compose service it runs (`bazarr`), which the container's name only
 * wraps in the project's prefix and a number. A replica and a release of one
 * container keep the container's own name, which is what tells them apart.
 */
export function serviceLabel(
  service: Pick<DeploymentRuntimeService, "name" | "service" | "replica">,
  several: boolean,
): string {
  return several && service.service && !service.replica ? service.service : service.name
}

/**
 * The containers the node names and how many it counts instead: all of them
 * up to `LISTED_SERVICES`, else the first three, any that is not running
 * before those that are, so the one that needs a look is never in the count.
 */
export function listedServices<T extends Pick<DeploymentRuntimeService, "state">>(
  services: T[],
): { shown: T[]; hidden: number } {
  if (services.length <= LISTED_SERVICES) return { shown: services, hidden: 0 }
  const ordered = [
    ...services.filter((service) => service.state !== "running"),
    ...services.filter((service) => service.state === "running"),
  ]
  const shown = ordered.slice(0, LISTED_SERVICES - 1)
  return { shown, hidden: services.length - shown.length }
}

/*
 * Up to two names are listed, each on its own line with its certificate,
 * because folding two saves no height and hides one. From three, a stack's
 * apps or a site with its aliases, the node leads with the address the
 * deployment is reached at, folds the rest behind "N more", and says every
 * certificate in one line: a list of seven names stacked in a node was the
 * tallest thing on the page and told the reader seven times that a
 * certificate was valid.
 */

/** The most names the node lists before it folds them. */
export const LISTED_DOMAINS = 2

type Certificate = DeploymentDomainRoute["certificate"]

/** The certificate states, the one that most needs a look first. */
const SEVERITY: Certificate[] = [
  "expired",
  "missing",
  "expiring",
  "unavailable",
  "not requested",
  "valid",
]

/** The names in the order the node draws them: the deployment's own address first. */
export function leadFirst(
  domains: DeploymentDomainRoute[],
  host: string | undefined,
): DeploymentDomainRoute[] {
  const lead = domains.findIndex((domain) => domain.hostname === host)
  if (lead <= 0) return domains
  return [domains[lead], ...domains.slice(0, lead), ...domains.slice(lead + 1)]
}

export type CertificateGroup = { state: Certificate; domains: DeploymentDomainRoute[] }

/**
 * The names grouped by their certificate's state, the worst first, each
 * group's names with the fewest days left first, so its first is the one the
 * group's reading speaks for.
 */
export function certificateGroups(domains: DeploymentDomainRoute[]): CertificateGroup[] {
  return SEVERITY.flatMap((state) => {
    const held = domains
      .filter((domain) => domain.certificate === state)
      .sort((a, b) => daysLeft(a) - daysLeft(b))
    return held.length > 0 ? [{ state, domains: held }] : []
  })
}

function daysLeft(domain: DeploymentDomainRoute) {
  return typeof domain.certificateDaysLeft === "number"
    ? domain.certificateDaysLeft
    : Number.MAX_SAFE_INTEGER
}

/**
 * A group as the line's lead says it: the single certificate's own word
 * (`CERTIFICATE_LABEL`), or how many share the state.
 */
export function groupLabel(state: Certificate, count: number): string {
  if (count === 1) return SINGLE[state]
  return `${count} ${MANY[state]}`
}

/** A group after the lead, short: "6 valid", "1 HTTP only". */
export function groupShort(state: Certificate, count: number): string {
  return `${count} ${SHORT[state]}`
}

const SINGLE: Record<Certificate, string> = {
  valid: "Certificate valid",
  expiring: "Certificate expiring",
  expired: "Certificate expired",
  missing: "No certificate",
  "not requested": "HTTP only",
  unavailable: "Not observed",
}

const MANY: Record<Certificate, string> = {
  valid: "certificates valid",
  expiring: "certificates expiring",
  expired: "certificates expired",
  missing: "with no certificate",
  "not requested": "HTTP only",
  unavailable: "not observed",
}

const SHORT: Record<Certificate, string> = {
  valid: "valid",
  expiring: "expiring",
  expired: "expired",
  missing: "with no certificate",
  "not requested": "HTTP only",
  unavailable: "not observed",
}

/** The one value every name shares, or undefined where they differ or none has it. */
export function shared<T>(values: (T | undefined)[]): T | undefined {
  const first = values[0]
  if (first === undefined) return undefined
  return values.every((value) => value === first) ? first : undefined
}

/** How many of the names ask a visitor for a password, as the line says it. */
export function passwordWords(domains: DeploymentDomainRoute[]): string | undefined {
  const asked = domains.filter((domain) => domain.protected).length
  if (asked === 0) return undefined
  return asked === domains.length ? "Password" : `${asked} with a password`
}
