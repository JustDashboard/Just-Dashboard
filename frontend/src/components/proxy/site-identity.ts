import type { SiteSpec } from "@/lib/types"

/**
 * The file name the server will accept for a domain: lowercase, starting with
 * a letter or a digit. Stripping the disallowed characters is not enough on
 * its own — `*.example.com` becomes `.example.com`, which the server refuses.
 */
export function fileNameFor(domain: string): string {
  return domain
    .toLowerCase()
    .replace(/[^a-z0-9._-]/g, "")
    .replace(/^[^a-z0-9]+/, "")
    .slice(0, 64)
}

/**
 * Why the server would refuse a file name, in the form's words, or nothing.
 * The rule is the server's siteNameRe; the server also refuses a name that
 * looks like a backup, and the preview says so.
 */
export function fileNameProblem(name: string): string | undefined {
  if (name === "") return "A site needs a file name."
  if (!/^[a-z0-9][a-z0-9._-]{0,63}$/.test(name)) {
    return "Lowercase letters, digits, dots, dashes and underscores, starting with a letter or a digit, up to 64."
  }
  return undefined
}

/**
 * Where certbot keeps a domain's certificate. A wildcard is issued for the
 * parent zone, so that is the directory — /etc/letsencrypt/live/example.com,
 * never live/*.example.com, which is not a directory name at all.
 */
export function lineageFor(domain: string): string {
  return domain.toLowerCase().replace(/^\*\./, "")
}

type Identity = Pick<SiteSpec, "name" | "certPath" | "keyPath">

/** The parts of a site's identity that no longer follow its first domain. */
export type IdentityFixed = { name: boolean; certPath: boolean; keyPath: boolean }

/** Nothing fixed: a new site's name and certificate follow what is typed. */
export const FOLLOW_DOMAINS: IdentityFixed = { name: false, certPath: false, keyPath: false }

/**
 * What a site loaded from its file keeps whatever its domains become: the
 * name of the file it lives in, and the certificate paths it already names.
 */
export function fixedFor(spec: Identity, existing: boolean): IdentityFixed {
  return { name: existing, certPath: Boolean(spec.certPath), keyPath: Boolean(spec.keyPath) }
}

/**
 * The name and certificate paths a site takes from its domains, for each part
 * that is not fixed. Worked out afresh from the whole list on every change:
 * keeping the first value instead named a site typed as app.example.com "a",
 * with its certificate in /etc/letsencrypt/live/a, one keystroke in.
 */
export function deriveIdentity(
  domains: string[],
  current: Identity,
  fixed: IdentityFixed,
): Identity {
  const first = domains[0]
  const lineage = first ? lineageFor(first) : ""
  return {
    name: fixed.name ? current.name : first ? fileNameFor(first) : "",
    certPath: fixed.certPath
      ? current.certPath
      : lineage
        ? `/etc/letsencrypt/live/${lineage}/fullchain.pem`
        : undefined,
    keyPath: fixed.keyPath
      ? current.keyPath
      : lineage
        ? `/etc/letsencrypt/live/${lineage}/privkey.pem`
        : undefined,
  }
}
