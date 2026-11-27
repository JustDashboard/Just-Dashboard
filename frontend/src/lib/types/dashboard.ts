/*
 * The dashboard's own install: its version, changelog and updates, its
 * settings and the restarts that apply them, its certificate and tailnet
 * identity, the usage ping, its links to a just-dashboard.com account (with
 * the app domains under it) and to a Just Hub, and removing it.
 */

// ---------------------------------------------------------------------------
// The dashboard's own version, its changelog, and updating it in place.
//
// These mirror internal/selfupdate. The changelog is structured rather than
// markdown for the reason that package gives: the UI has to select the
// releases between the installed version and the newest, and paint a tag
// against each change — neither of which is a thing you can do to a paragraph.
// ---------------------------------------------------------------------------

export type ChangeKind = "added" | "changed" | "fixed" | "removed" | "security" | "deprecated"

export type ReleaseChange = {
  kind: ChangeKind
  text: string
  /** The sentence under the line, where the consequence is not obvious. */
  detail?: string
}

export type Release = {
  version: string
  /** A calendar day, YYYY-MM-DD. */
  date: string
  title: string
  summary?: string
  changes: ReleaseChange[]
  /** Needs the operator to do something by hand; never hidden behind a click. */
  breaking?: boolean
  breakingNote?: string
}

export type UpdateRunStatus = "pending" | "running" | "success" | "failed"
export type UpdatePhase = "queued" | "fetching" | "building" | "restarting" | "finished"

/** One upgrade attempt, as recorded on disk by the container that ran it. */
export type UpdateRun = {
  id: string
  status: UpdateRunStatus
  phase: UpdatePhase
  fromVersion: string
  toVersion: string
  ref: string
  dir: string
  compose: string
  image: string
  container: string
  health?: string
  /** Installed from the repository's tag `v<toVersion>` or not at all. */
  requireTag?: boolean
  fromCommit?: string
  toCommit?: string
  /** `toCommit` is the commit that tag names, not wherever the branch was. */
  tagged?: boolean
  /** Where the dashboard's database was copied before the update, if it was. */
  databaseCopy?: string
  /** The images tagged `:previous` before the build replaced them. */
  savedImages?: { current: string; previous: string }[]
  actor: string
  startedAt: string
  updatedAt: string
  finishedAt?: string
  error?: string
}

/**
 * Everything this build knows about its own past, newest first; needs no network. Served by
 * `GET /dashboard/update/history`, apart from the report that is polled.
 */
export type UpdateHistory = {
  history: Release[]
}

export type SelfUpdateReport = {
  version: string
  /**
   * The short commit this build was made from when it is not the release of
   * `version` (a patch branch, a fork's head). Absent for a release.
   */
  commit?: string
  latest?: string
  available: boolean
  /** The version on offer has a release tag; false is a fork's branch head. */
  tagged: boolean
  /** A newer version on the tracked branch with no tag yet. Not offered. */
  pending?: string
  /** Releases newer than the installed version, newest first. */
  releases: Release[]
  breaking: boolean
  check: {
    enabled: boolean
    checkedAt?: string
    error?: string
    source?: string
    repo: string
    ref: string
    /** Only tagged versions are offered and installed: the project's own repository. */
    tagsOnly: boolean
  }
  install: {
    supported: boolean
    reason?: string
    dir?: string
    compose?: string
    /** Uncommitted changes in the checkout, in `git status --porcelain` form. */
    dirty?: string[]
  }
  run?: UpdateRun
  log?: string
}

/**
 * The dashboard's own settings, as internal/selfcfg speaks them.
 *
 * Every field here is a line in the `.env` beside the compose file, and
 * changing one restarts the stack into it. That is why the shape is flat and
 * small: these are the settings somebody has a reason to change from a
 * browser, not every variable the backend reads.
 */
export type DashboardSettings = {
  site: string
  /** The interface to listen on, when that is not the same as `site`. */
  bind: string
  tls: "tailscale" | "internal" | "off"
  port: number
  frontendPort: number
  backendPort: number
  allowedCidrs: string
  terminalEnabled: boolean
  require2fa: boolean
  sessionTtl: string
  idleTtl: string
  updateCheck: boolean
}

/** One setting moving, for the confirmation dialog and the run record. */
export type DashboardConfigChange = {
  key: string
  label: string
  from: string
  to: string
}

export type ConfigRunStatus = "pending" | "running" | "success" | "failed" | "rolled_back"
export type ConfigPhase = "queued" | "applying" | "waiting" | "rollback" | "finished"

/**
 * One restart, as recorded on disk by the container that carried it out.
 *
 * It outlives the dashboard it restarts, which is the whole point: the browser
 * follows this record across the moment the API stops answering, and picks the
 * story up from whatever backend comes back.
 */
export type DashboardConfigRun = {
  id: string
  status: ConfigRunStatus
  phase: ConfigPhase
  action: "apply" | "restart" | "rebuild"
  changes?: DashboardConfigChange[]
  dir: string
  compose: string
  image: string
  envPath?: string
  backup?: string
  health?: string
  rollbackHealth?: string
  /** Where to go once this finishes — the address may have moved. */
  endpoint?: string
  container: string
  /** A caveat about a run that otherwise worked, e.g. a certificate that could not be issued. */
  note?: string
  actor: string
  startedAt: string
  updatedAt: string
  finishedAt?: string
  error?: string
}

/**
 * What this machine is on its tailnet, as the dashboard discovered it.
 *
 * The settings form fills the address, the listening interface and the
 * allowlist from this the moment somebody picks a Tailscale certificate. Those
 * three facts are one `tailscale status` away, and asking an operator to find
 * them by hand — then rejecting the form when they guessed — is the dashboard
 * refusing to do its own job.
 */
export type TailscaleIdentity = {
  available: boolean
  running: boolean
  state?: string
  /** MagicDNS name, without the trailing dot. */
  hostname?: string
  ip4?: string
  /** Whether the tailnet will issue certificates at all. */
  httpsEnabled: boolean
  /** Why it cannot be used, in a sentence meant for a person. */
  detail?: string
}

export type DashboardCertificate = {
  issued: boolean
  expires?: string
}

export type DashboardConfigReport = {
  /** False on an install with no compose stack to recreate. */
  supported: boolean
  reason?: string
  dir?: string
  compose?: string
  envPath?: string
  settings: DashboardSettings
  /** The URL this configuration implies. */
  endpoint: string
  tailscale: TailscaleIdentity
  /**
   * What is actually on disk for the configured address — not the same
   * question as which mode is set. A Tailscale install with no certificate yet
   * answers perfectly well on Caddy's internal CA, and saying "trusted"
   * because the setting says so would contradict the padlock.
   */
  certificate: DashboardCertificate
  /** Settings the file asks for that the running process is not doing. */
  drift?: DashboardConfigChange[]
  run?: DashboardConfigRun
  log?: string
}

/**
 * What a linked site may be shown and ask for. Mirrors internal/accountlink: the scope is
 * chosen here, enforced here, and the site only draws the controls the reported scope allows.
 */
export type AccountLinkScope = "observe" | "operate" | "maintain"

/**
 * The optional daily count of servers. Mirrors `GET /dashboard/usage-ping`. `enabled` is false when
 * `offBy` names a switch in `.env`; the bodies are the report's exact bytes.
 */
export type UsagePingStatus = {
  enabled: boolean
  offBy?: "JD_USAGE_PING" | "JD_ACCOUNT_LINK"
  choice: "undecided" | "on" | "off"
  siteUrl: string
  nextBody: string
  lastBody?: string
  lastSentAt?: string
  lastError?: string
}

/**
 * This server's link to a just-dashboard.com account. Mirrors the `/dashboard/link` routes;
 * optional fields are omitted by the backend when absent, never `null`.
 */
export type AccountLinkStatus = {
  /** JD_ACCOUNT_LINK. When false the page explains the switch and offers no action. */
  enabled: boolean
  /** JD_ACCOUNT_URL, e.g. "https://just-dashboard.com". */
  siteUrl: string
  state: "unlinked" | "pending" | "linked"
  /** This server's key fingerprint, "21fe:31df:a154:a261". Absent until the identity exists. */
  fingerprint?: string
  /** Present only while state is "pending". */
  request?: {
    userCode: string
    approveUrl: string
    expiresAt: string
    scope: AccountLinkScope
  }
  /** Present only while state is "linked". */
  link?: {
    serverId: string
    /** The site account's label, an e-mail address. */
    owner: string
    scope: AccountLinkScope
    linkedAt: string
    /** Dashboard username, or "cli". */
    linkedBy: string
    lastHeartbeatAt?: string
    /** The last failed exchange with the site; cleared by the next success. */
    lastError?: string
    lastCommand?: {
      id: string
      kind: string
      status: "succeeded" | "failed" | "refused"
      issuedBy: string
      at: string
    }
    siteKeys: { kid: string; fingerprint: string; notAfter?: string }[]
    updatePolicy?: { auto: "off" | "patch" | "all"; window: { start: string; end: string } }
    /** The exact JSON body of the last heartbeat sent, so the page shows what is shared. */
    lastSnapshot?: Record<string, unknown>
  }
  /** Why the most recent link request ended without a link, so the page can say so. */
  lastOutcome?: { result: "denied" | "expired" | "failed"; at: string; message?: string }
}

/**
 * A deployment domain under the server's just-dashboard.com handle, as `usedBy` lists it.
 * Mirrors `GET /dashboard/link/domain` (docs/internal/backend/app-domains.md).
 */
export type AppDomainUse = {
  projectId: number
  project: string
  environmentId: number
  /** The environment's kind, "production" or "staging", as `?env=` takes it. */
  environment: string
  hostname: string
}

/**
 * This server's address under the site's zone: `<app>.<handle>.<zone>` names for deployed apps,
 * through one wildcard record and one wildcard certificate. Mirrors `GET /dashboard/link/domain`.
 * Unlike the link's own status, absent values are `null`, never left out, and times are Unix
 * seconds.
 */
export type AppDomainStatus = {
  /** JD_APP_DOMAINS. False switches the feature off entirely. */
  enabled: boolean
  linked: boolean
  /** Whether the site offers addresses; null when unknown (not linked, or the site not reached). */
  available: boolean | null
  /** The site's zone, e.g. "just-dashboard.com". Null while unknown. */
  zone: string | null
  /** Why `available` is false or null, in words for the operator. */
  reason: string | null
  /** This server's public IPv4, which a claim publishes. */
  publicAddress: string | null
  /** New deployments are given the address by default. */
  autoAssign: boolean
  domain: {
    handle: string
    hostname: string
    wildcard: string
    address: string
    status: "active" | "suspended" | "detached"
    /** A suspension's or a detachment's stated reason. */
    reason: string | null
    updatedAt: number
  } | null
  certificate: {
    name: string
    state: "missing" | "issuing" | "valid" | "expiring" | "failed"
    expiresAt: number | null
    error: string | null
  } | null
  usedBy: AppDomainUse[]
  /**
   * Deployment names under a handle this server held and does not hold now: the stored one while
   * it is not held, and every handle it held before (changed or released). The backend leaves
   * them out of each release. Absent from a backend older than 0.9.1.
   */
  detached?: AppDomainUse[]
  /**
   * The handle the account chose for this server when it approved the link (or later on the
   * site), which the server claims by itself. Null when there is none, or once a domain is held.
   */
  offer: AppDomainOffer | null
}

/** An address the site offers this server, claimed automatically (AUTOCLAIM.md). */
export type AppDomainOffer = {
  handle: string
  hostname: string
  /** Why the last automatic claim failed, in words; null while it has not run or is under way. */
  error: string | null
}

/**
 * What a Just Hub may be shown and ask for: the account link's three scopes, chosen, stored and
 * enforced here (docs/internal/backend/hub-link-protocol.md §7.1).
 */
export type HubLinkScope = AccountLinkScope

/**
 * The `hub` field on `GET /auth/session` (hub-link-protocol §8.3). Present only for a fully
 * signed-in session on a server with a stored hub link; the shell draws every hub feature from
 * its presence and nothing at all without it.
 */
export type HubLinkSession = {
  linked: true
  /** The hub's name, its `owner.label`: "hub". */
  name: string
  /** The stored hub URL, for "Open Just Hub". */
  url: string
  reachable: boolean
  /**
   * Present, as true, only while the link is idle (§2.2, §2.4): the hub's address may now answer
   * on a machine this server refused, so the shell offers no link to it.
   */
  idle?: true
}

/** A server the hub lists, as its last heartbeat reply named it (hub-link-protocol §6.1). */
export type HubLinkServer = {
  serverId: string
  name: string
  /** The address a browser opens it at; absent for a server reached by a tunnel. */
  endpoint?: string
  state: "online" | "late" | "offline" | "updating"
  /** Its dashboard version, or "" before its first heartbeat. */
  version: string
  /** True on exactly one entry: this server. */
  self: boolean
  lastSeenAt?: string
}

/** A short message the hub wants every linked dashboard to show (hub-link-protocol §6.2). */
export type HubLinkNotice = {
  /** Stable while the notice stands, so a dismissal can name it. */
  id: string
  level: "info" | "warning" | "critical"
  title: string
  /** Plain text, never markup. */
  body: string
  /** On the hub's own origin; the backend drops anything else. */
  href?: string
  at: string
}

/**
 * The hub's tailnet machine, as tailscaled's WhoIs named it (hub-link-protocol §2.4): its
 * StableNodeID, MagicDNS name, and the login it belongs to, or its tags for a tagged machine.
 */
export type HubLinkMachine = {
  id: string
  name: string
  /** Absent for a tagged machine, which has `tags` instead. */
  owner?: string
  tags: string[]
}

/** `GET /dashboard/hub/check?url=` (hub-link-protocol §8.2): the URL rule, nothing started. */
export type HubLinkCheck = {
  /** The hub URL as Connect would store it. */
  hubUrl: string
  /** Absent when tailscaled could not be asked, and at the test origin. */
  machine?: HubLinkMachine
  testOrigin: boolean
}

/**
 * This server's link to a Just Hub. Mirrors `GET /dashboard/hub` and the 200 answers of the
 * routes beside it (hub-link-protocol §8.2). Optional fields are left out, never `null`; `admin`
 * is sent only to `system.admin`, and with the link switched off only `enabled` and `state`.
 */
export type HubLinkStatus = {
  /** JD_HUB_LINK. */
  enabled: boolean
  state: "unlinked" | "pending" | "linked"
  hubUrl?: string
  hubName?: string
  scope?: HubLinkScope
  /** The last heartbeat succeeded, none has failed since, and it is younger than three intervals. */
  reachable?: boolean
  lastHeartbeatAt?: string
  /** While linked: how often this server reports, the hub's interval clamped to 10–600 (§4.8). */
  heartbeatSeconds?: number
  /**
   * Present, as true, only while a stored link is idle, and to every reader; why is
   * `admin.link.lastError`. No page offers the hub's address as a link while it is set.
   */
  idle?: true
  fleet?: HubLinkServer[]
  notices?: HubLinkNotice[]
  /**
   * Hub sign-in (§9): `enabled` is JD_HUB_SIGNIN, `offered` and `users` what the hub's last reply
   * said, and `mine` the caller's own grant, when it has one for this link.
   */
  signIn?: {
    enabled: boolean
    offered: boolean
    users: string[]
    mine?: { hubUser: string; since: string }
  }
  admin?: {
    /** This server's hub link key, "21fe:31df:a154:a261". Absent until the identity exists. */
    fingerprint?: string
    /** JD_HUB_LINK_TEST_ORIGIN, which is for tests and never for production. */
    testOrigin?: string
    request?: {
      hubUrl: string
      userCode: string
      approveUrl: string
      expiresAt: string
      scope: HubLinkScope
      /** The hub's machine as Connect found it; absent when it was not checked. */
      hubMachine?: HubLinkMachine
    }
    link?: {
      serverId: string
      linkedAt: string
      /** Dashboard username, or "cli". */
      linkedBy: string
      /** The pinned hub machine (§2.4); absent until one is recorded. */
      hubMachine?: HubLinkMachine
      /** The last failed exchange with the hub; cleared by the next success. */
      lastError?: string
      lastCommand?: {
        id: string
        kind: string
        status: "succeeded" | "failed" | "refused"
        /** Who the hub says asked: a hub user, or "rollout". Recorded, not trusted. */
        issuedBy: string
        at: string
      }
      hubKeys: { kid: string; fingerprint: string; notAfter?: string }[]
      updatePolicy?: { auto: "off" | "patch" | "all"; window: { start: string; end: string } }
      /** The exact body of the last heartbeat sent, so the page shows what is shared. */
      lastSnapshot?: Record<string, unknown>
    }
    /** Why this server is not linked: the last request's end, or `removed` in the hub (§8.9). */
    lastOutcome?: {
      result: "denied" | "expired" | "failed" | "removed"
      at: string
      message?: string
    }
    grants?: { userId: number; username: string; hubUser: string; since: string }[]
  }
}

/**
 * Removing the dashboard from the server. Mirrors internal/uninstall.
 *
 * The phases are in the order they run: the first two in the backend while it
 * still serves this page, the rest in a sibling container that takes the stack
 * down — which is the moment this page loses the API it is polling.
 */
export type UninstallMode = "keep" | "wipe"

export type UninstallPhase = "queued" | "pausing" | "deployments" | "stack" | "files" | "finished"

export type UninstallRun = {
  id: string
  status: "pending" | "running" | "success" | "failed"
  /** Where it is; a failed run keeps the phase it failed in. */
  phase: UninstallPhase
  mode: UninstallMode
  deleteBackups: boolean
  dir: string
  compose: string
  project?: string
  image: string
  envPath: string
  dataDir: string
  backupDir?: string
  /** Entries of the data directory a wipe leaves, because a site still uses them. */
  keep?: string[]
  container: string
  /** What could not be removed. None of them stops the run. */
  warnings?: string[]
  actor: string
  startedAt: string
  updatedAt: string
  finishedAt?: string
  error?: string
}

export type UninstallReport = {
  /** False on an install with no compose stack to take down. */
  supported: boolean
  reason?: string
  run?: UninstallRun
  log?: string
}

export type UninstallItem = { id: string; name: string }

export type DirectoryUsage = {
  path: string
  files: number
  bytes: number
  /** Counting stopped at its time budget, so the figures are a floor. */
  partial?: boolean
}

/** What the deployments own on this host — what a wipe removes. */
export type DeploymentFootprint = {
  projects: number
  containers: UninstallItem[]
  routes: UninstallItem[]
  networks: UninstallItem[]
  volumes: UninstallItem[]
  paths: UninstallItem[]
  images: UninstallItem[]
  ingress?: UninstallItem
}

/** What the uninstall dialog shows before anything happens. */
export type UninstallPlan = {
  /** What a wipe asks to be typed: the server's name. */
  phrase: string
  dir: string
  envPath: string
  data: DirectoryUsage
  backups?: DirectoryUsage
  /** The data directory's nginx-cache, kept by a wipe because a site caches into it. */
  keptCache?: string
  deployments: DeploymentFootprint
  deploymentsError?: string
  /** Where the transcript survives the stack and, for a wipe, the data directory. */
  durableLog: string
}
