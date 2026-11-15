import type { Certificate } from "@/lib/types"

type Named = Pick<Certificate, "name" | "path">

/**
 * The views of the Certificates page, in the order its tab strip draws them:
 * the certificates themselves first, then what issues and renews them, then
 * the names watched elsewhere. The first is the bare address.
 */
export const PAGE_TABS = [
  "certificates",
  "renewal",
  "dns",
  "accounts",
  "authority",
  "requests",
  "watched",
] as const
export type PageTab = (typeof PAGE_TABS)[number]

/** The views of one certificate's page; the first is the bare address. */
export const CERTIFICATE_TABS = ["overview", "key", "chain", "files", "timeline"] as const
export type CertificateTab = (typeof CERTIFICATE_TABS)[number]

/** A view named in an address, or the first one when it names none this page has. */
export function pageTab(asked: string | null | undefined): PageTab {
  return (PAGE_TABS as readonly string[]).includes(asked ?? "") ? (asked as PageTab) : PAGE_TABS[0]
}

export function certificateTab(asked: string | null | undefined): CertificateTab {
  return (CERTIFICATE_TABS as readonly string[]).includes(asked ?? "")
    ? (asked as CertificateTab)
    : CERTIFICATE_TABS[0]
}

/** The Certificates page on one of its views. */
export function certificatesHref(tab?: PageTab): string {
  return tab && tab !== PAGE_TABS[0] ? `/proxy/certificates?tab=${tab}` : "/proxy/certificates"
}

/** What the address calls a certificate that has no name of its own. */
const UNNAMED = "certificate"

/**
 * A certificate's own page. It is addressed by its name, the word an operator
 * knows it by, and the file is added only where the name does not say which
 * one: certbot's lineage and a site's copy of the same certificate can share
 * one, and a file the inventory could not name has none.
 */
export function certificateHref(cert: Named, all: Named[] | undefined, tab?: string): string {
  const shared = !cert.name || (all ?? []).filter((c) => c.name === cert.name).length > 1
  const params = new URLSearchParams()
  if (shared) params.set("path", cert.path)
  if (tab && tab !== CERTIFICATE_TABS[0]) params.set("tab", tab)
  const query = params.toString()
  return `/proxy/certificates/${encodeURIComponent(cert.name || UNNAMED)}${query ? `?${query}` : ""}`
}

/**
 * A certificate's own page, from a place that holds the one certificate and
 * not the inventory it would take to know whether its name is shared: its
 * file goes with its name, which names one certificate whatever the rest are.
 */
export function certificateFileHref(cert: Named): string {
  return `/proxy/certificates/${encodeURIComponent(cert.name || UNNAMED)}?path=${encodeURIComponent(cert.path)}`
}

/**
 * The certificate an address names: by its file where the address carries
 * one, which is unique, and otherwise the first certificate of that name.
 */
export function findCertificate<T extends Named>(
  all: T[] | undefined,
  name: string,
  path?: string | null,
): T | undefined {
  if (!all) return undefined
  if (path) return all.find((cert) => cert.path === path)
  return all.find((cert) => (cert.name || UNNAMED) === name)
}

/**
 * Where an old address lands. The certificate opened in a sheet over the
 * list at `?cert=<file>` until 0.8.7, and the overview, the TLS report, the
 * server map and the coverage list still link that way: each lands on the
 * certificate's page, and a file that is no longer listed on the list.
 */
export function sheetRedirect(all: Named[] | undefined, file: string | null): string | undefined {
  if (!file || !all) return undefined
  const cert = all.find((c) => c.path === file)
  return cert ? certificateHref(cert, all) : certificatesHref()
}
