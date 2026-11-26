/*
 * The API's types live under lib/types/, one file per area of the dashboard,
 * so an area changes its own file without editing everyone else's. They are
 * re-exported here so every import of @/lib/types keeps working. A new type
 * goes in the file of the area that owns it; core.ts is only for what every
 * area shares, and stays small.
 */
export type * from "./types/accounts"
export type * from "./types/backups"
export type * from "./types/core"
export type * from "./types/dashboard"
export type * from "./types/database"
export type * from "./types/deploy"
export type * from "./types/docker"
export type * from "./types/files"
export type * from "./types/git"
export type * from "./types/logs"
export type * from "./types/network"
export type * from "./types/packages"
export type * from "./types/processes"
export type * from "./types/security"
export type * from "./types/system"
export type * from "./types/terminal"

/*
 * The proxy pages' types live under lib/proxy/, one file per area of those
 * pages, so the areas change them without editing this file together. They
 * are re-exported here so every import of @/lib/types keeps working.
 */
export type * from "./proxy/types-engine"
export type * from "./proxy/types-sites"
export type * from "./proxy/types-siteform"
export type * from "./proxy/types-streams"
export type * from "./proxy/types-ports"
export type * from "./proxy/types-ports-history"
export type * from "./proxy/types-certs"
export type * from "./proxy/types-cert-tools"
export type * from "./proxy/types-tls"
export type * from "./proxy/types-insights"
export type * from "./proxy/types-alerts"
