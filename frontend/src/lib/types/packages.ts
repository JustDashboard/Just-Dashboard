/*
 * The host's software: pending system updates, installed packages, search,
 * a package's details, man pages and what it gives you.
 */

// --- System updates ---

export type UpdatePackage = {
  name: string
  current: string
  candidate: string
  origin?: string
  security: boolean
  arch?: string
}

export type UpdateReport = {
  available: boolean
  manager?: string
  packages: UpdatePackage[]
  securityCount: number
  /**
   * Whether this manager can tell a security update from any other. Alpine
   * and Arch publish no advisory data, so a zero count there means "cannot
   * tell", not "none outstanding".
   */
  securityFiltering: boolean
  rebootRequired: boolean
  rebootPackages?: string[]
  lastChecked: string
  error?: string
}

// --- The host's software ---
//
// Mirrors internal/updates' catalogue half. The upgrade report above answers
// "how far behind is this machine"; these answer "what is on it", "what else
// is there" and — the one nothing in this class offers — "now that it is
// installed, what do I type".

export type InstalledPackage = {
  name: string
  version: string
  arch?: string
  summary?: string
  /** Installed size in bytes, or absent where the manager reports none. */
  size?: number
  section?: string
  /** Somebody asked for this, as opposed to it arriving as a dependency. */
  explicit: boolean
  /** The package database says the system does not work without it. */
  essential?: boolean
  /** The pending version, joined on from the upgrade report by the server. */
  upgradable?: string
  security?: boolean
}

export type PackageInventory = {
  available: boolean
  manager?: string
  packages: InstalledPackage[]
  explicitCount: number
  totalSize?: number
  upgradeCount: number
  securityCount: number
  canInstall: boolean
  /**
   * Whether "and delete its configuration" means anything here. RPM keeps a
   * modified config file whatever it is asked, so the switch is hidden there
   * rather than shown doing nothing.
   */
  canPurge: boolean
  /**
   * When the repository index was last refreshed. Everything on the page is
   * read from that index, so a three-month-old one is a catalogue missing
   * every package added since — absent where the manager cannot say.
   */
  indexAge?: string
  canRefresh: boolean
  readAt: string
  error?: string
}

export type PackageSearchResult = {
  name: string
  version?: string
  summary?: string
  repository?: string
  installed: boolean
  installedVersion?: string
}

export type PackageDetail = {
  name: string
  version?: string
  installedVersion?: string
  installed: boolean
  summary?: string
  description?: string
  homepage?: string
  license?: string
  section?: string
  repository?: string
  arch?: string
  maintainer?: string
  size?: number
  dependencies?: string[]
  essential?: boolean
  /** Why the dashboard will not remove it; absent when it will. */
  protected?: string
  upgradable?: string
}

export type ManPage = {
  name: string
  /** The manual volume: 1 is a command, 5 a file format, 8 a root-only tool. */
  section: string
  path: string
}

/** What a package gives you and how to reach it, read from its own file list. */
export type PackageUsage = {
  package: string
  commands?: string[]
  manPages?: ManPage[]
  services?: string[]
  configFiles?: string[]
  docs?: string[]
  manual?: string
  manualFor?: string
  /** A page exists but `man` is not installed here, so it could not be read. */
  manUnavailable?: string
  truncated?: boolean
  /** No commands, pages or units — a library other packages use. */
  empty: boolean
}
