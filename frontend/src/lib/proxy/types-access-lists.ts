import type { ProxyReload, ProxyValidation } from "@/lib/types"

/**
 * What an access list says, as the page edits it. `deny` is checked first
 * and `allow` second, and a non-empty `allow` ends with `deny all`.
 * `satisfy` is "any" only where there is both an allow list and a password
 * file: then either one lets a visitor in.
 */
export type AccessListSpec = {
  allow: string[]
  deny: string[]
  /** A password file's name, from Password files. */
  authFile?: string
  /** The login prompt, where there is a password file. */
  realm?: string
  satisfy: "all" | "any"
}

/** A site whose file includes a list, directly or through a snippet. */
export type AccessListUse = {
  site: string
  path: string
  enabled: boolean
}

/** GET /proxy/access-lists/: one list, where it is and who includes it. */
export type AccessList = AccessListSpec & {
  name: string
  path: string
  /** The directive a site takes the list in with. */
  include: string
  /** The password file it names is gone: nginx refuses every login. */
  authFileMissing?: boolean
  /** Why saving from the form would change what the file does. */
  handWritten?: string
  usedBy: AccessListUse[]
  modified: string
}

export type AccessLists = {
  /** Where the lists live, jd-access under the nginx directory. */
  dir: string
  /** The address the dashboard sees this browser at. */
  clientAddress: string
  lists: AccessList[]
}

/**
 * PUT /proxy/access-lists/{name}: the list as written, the test it passed
 * with every site that includes it, and how the reload after it went.
 */
export type AccessListSaved = {
  list: AccessList
  validation: ProxyValidation
  reloaded: boolean
  reloadError?: string
  reload?: ProxyReload | null
}
