import {
  Archive,
  Connection,
  Bell,
  Box,
  ChartActivity,
  Clock,
  CloudUpload,
  Cpu,
  Database,
  DesktopDevice,
  FileText,
  FirewallCheck,
  FolderOpen,
  GitHubMark,
  Globe,
  GridMasonry,
  GridSquare,
  Home,
  Inspect,
  Key,
  Layers,
  Lifebuoy,
  Link,
  Rocket,
  LineChart,
  Linked,
  ListOrdered,
  LockClosed,
  Logs,
  Monitoring,
  NetworkDevice,
  Notes,
  Puzzle,
  Route,
  Router,
  Rss,
  SecureConnection,
  Servers,
  SettingsGear,
  SettingsSliders,
  Shield,
  ShieldCheck,
  ShieldOff,
  SignIn,
  Terminal,
  TerminalWindow,
  ServerMap,
  Topology,
  UserSettings,
  Users,
  Warning,
  Wrench,
  ArrowLeftRight,
  Plus,
} from "@/components/icons"
import type { Capability } from "@/lib/types"
import { sectionOf, type SectionKey } from "@/lib/sidebar-sections"
import { ExcalidrawGlyph } from "@/components/boards/glyphs"

/**
 * Every destination in the product, once.
 *
 * The sidebar draws it and the command palette searches it. It used to live
 * in `app-sidebar.tsx`
 * beside the markup that rendered it; it is a file of its own now because the
 * rail is no longer the only thing that walks it — a section opens a panel of
 * its own pages, a group opens a panel of sections, and the deployment section
 * opens a third level for one project, so the lists have readers that are not
 * the rail.
 */

export type NavChild = {
  title: string
  href: string
  icon: React.ComponentType<{ className?: string }>
  /** Hidden unless the signed-in role holds this capability. */
  capability?: Capability
}

export type NavItem = {
  title: string
  href: string
  icon: React.ComponentType<{ className?: string }>
  /** Hidden unless the signed-in role holds this capability. */
  capability?: Capability
  /**
   * A feature large enough to be several pages. The row opens a panel holding
   * exactly these, and the first of them is the section's own landing page —
   * named for what it shows ("Browse", "Live", "Version"), not "Overview",
   * wherever the section has a better word for it.
   */
  children?: NavChild[]
}

/**
 * Several entries that belong together, behind one row. Unlike a section it is
 * not a page: there is nothing that is "Monitoring" as such, only Metrics,
 * Processes and Logs, so pressing it opens its panel and leaves the page where
 * it is. An entry inside may itself be a section, which is the one way the
 * rail gets a third level outside a deployment.
 */
export type NavGroup = {
  title: string
  href?: undefined
  icon: React.ComponentType<{ className?: string }>
  capability?: Capability
  children: NavItem[]
}

/** Anything the rail can go into, and what its lists are made of. */
export type NavEntry = NavItem | NavGroup

/**
 * The rail, top to bottom, in the order a day on the server runs: is the
 * machine well, what is running on it, the tools to work on it, what keeps it
 * safe, the machinery under all of it, and the housekeeping. Deployments open
 * the second group because shipping something is the reason most visits
 * happen; it used to sit fourth in a group called "Operations" between Packages
 * and Backups, where nobody who did not already know looked for it.
 *
 * The top-level list is twelve rows, not seventeen. Metrics, Processes and Logs
 * are one question — how is the machine doing — and sit behind Monitoring; the
 * packages, system accounts and audit trail are the pages you open to change
 * the server rather than to use it, and sit behind Server configuration. The
 * network — its devices, routes, gateway, tunnels and resolver, and the proxy
 * in front of the sites — is a group of its own before Protection.
 */
export const NAV: { label: string; items: NavEntry[] }[] = [
  {
    label: "Server",
    items: [
      { title: "Overview", href: "/", icon: Home },
      // Everything on the server as one canvas, the server in the middle.
      // Beside the Overview rather than in a section: it is about every
      // section at once, and like the Overview it cannot be hidden.
      { title: "Map", href: "/map", icon: ServerMap },
      {
        title: "Monitoring",
        icon: Monitoring,
        children: [
          { title: "Metrics", href: "/metrics", icon: LineChart },
          // Beside the readings it watches. An administrator's, as the
          // proxy's Alerts are: its rules decide who is told about what.
          { title: "Alerts", href: "/metrics/alerts", icon: Bell, capability: "system.admin" },
          {
            title: "Processes",
            href: "/processes",
            icon: ListOrdered,
            children: [
              { title: "Live", href: "/processes", icon: ListOrdered },
              { title: "PM2", href: "/processes/pm2", icon: ChartActivity },
              { title: "Services", href: "/processes/services", icon: Servers },
              { title: "Scheduled", href: "/processes/scheduled", icon: Clock },
            ],
          },
          { title: "Logs", href: "/logs", icon: Logs },
        ],
      },
    ],
  },
  {
    label: "Apps",
    items: [
      {
        title: "Deployments",
        href: "/deploy",
        icon: Rocket,
        children: [
          { title: "Projects", href: "/deploy", icon: GridMasonry },
          // The catalogue's own templates: read by anyone who may read the
          // catalogue, added and removed by an administrator.
          { title: "Templates", href: "/deploy/templates", icon: Layers },
          // Reading the credential list needs system.admin — the backend seals
          // every credential route alike — so the row is drawn only where the
          // destination would not simply refuse it.
          {
            title: "Credentials",
            href: "/deploy/credentials",
            icon: Key,
            capability: "system.admin",
          },
          // Read masked by anyone who may read a project's variables; written
          // by an administrator, which the page itself says.
          { title: "Shared variables", href: "/deploy/variables", icon: Linked },
          // Read by anyone who may read a project's variables — names,
          // addresses and who reads each, never a credential; written by an
          // administrator.
          { title: "Secret stores", href: "/deploy/secret-stores", icon: LockClosed },
          { title: "Notifications", href: "/deploy/notifications", icon: Bell },
          { title: "Log drains", href: "/deploy/drains", icon: Logs },
        ],
      },
      {
        title: "Databases",
        href: "/databases",
        icon: Database,
        // The pages about every database at once. One database's own pages
        // are not here: which of them it has depends on its engine, so its
        // layout hands the rail that panel, a level below this one
        // (`components/database/shell/nav-scope.tsx`).
        children: [
          { title: "Control center", href: "/databases", icon: Layers },
          { title: "Map", href: "/databases/map", icon: Route },
          {
            title: "Add a database",
            href: "/databases/new",
            icon: Plus,
            capability: "system.admin",
          },
        ],
      },
      {
        title: "Docker",
        href: "/docker",
        icon: Box,
        children: [
          { title: "Overview", href: "/docker", icon: GridSquare },
          { title: "Containers", href: "/docker/containers", icon: Box },
          { title: "Stacks", href: "/docker/stacks", icon: GridMasonry },
          { title: "Images", href: "/docker/images", icon: Layers },
          { title: "Volumes", href: "/docker/volumes", icon: Database },
          { title: "Networks", href: "/docker/networks", icon: NetworkDevice },
          { title: "Events", href: "/docker/events", icon: Rss },
        ],
      },
    ],
  },
  {
    label: "Workspace",
    items: [
      { title: "Excalidraw", href: "/excalidraw", icon: ExcalidrawGlyph },
      { title: "Terminal", href: "/terminal", icon: Terminal, capability: "terminal" },
      { title: "Files", href: "/files", icon: FolderOpen },
      { title: "Git", href: "/git", icon: GitHubMark },
      {
        title: "Remote Desktop",
        href: "/remote-desktop",
        icon: TerminalWindow,
        capability: "system.admin",
      },
    ],
  },
  {
    label: "Network",
    items: [
      {
        // The server as a router: its devices, routes, firewall, gateway,
        // tunnels, resolver and traffic. These were four pages of Security
        // until 0.7.1; Security kept what is about who may get in, and the
        // network became a section that can change the network as well as
        // read it.
        title: "Network",
        href: "/network",
        icon: Topology,
        children: [
          { title: "Overview", href: "/network", icon: GridSquare },
          { title: "Drift", href: "/network/drift", icon: Monitoring },
          {
            title: "Address planning",
            href: "/network/ipam",
            icon: NetworkDevice,
            capability: "system.admin",
          },
          {
            title: "External checks",
            href: "/network/external",
            icon: Globe,
            capability: "system.admin",
          },
          { title: "Interfaces", href: "/network/interfaces", icon: NetworkDevice },
          { title: "Routing", href: "/network/routing", icon: Route },
          { title: "Egress groups", href: "/network/egress", icon: Lifebuoy },
          { title: "Firewall", href: "/network/firewall", icon: FirewallCheck },
          { title: "Gateway", href: "/network/gateway", icon: ArrowLeftRight },
          { title: "Protection", href: "/network/protection", icon: ShieldCheck },
          { title: "VPN", href: "/network/vpn", icon: LockClosed },
          { title: "DNS", href: "/network/dns", icon: Globe },
          { title: "Traffic", href: "/network/traffic", icon: ChartActivity },
          {
            title: "Socket history",
            href: "/network/flows",
            icon: ChartActivity,
            capability: "system.admin",
          },
          { title: "Connections", href: "/network/connections", icon: Connection },
          {
            title: "Connection path",
            href: "/network/investigate",
            icon: Inspect,
            capability: "system.admin",
          },
          { title: "Tools", href: "/network/tools", icon: Wrench },
          {
            title: "Packet captures",
            href: "/network/captures",
            icon: Inspect,
            capability: "system.admin",
          },
          {
            title: "Saved runs",
            href: "/network/runs",
            icon: Notes,
            capability: "system.admin",
          },
        ],
      },
      // The reverse proxy, its certificates, the TCP/UDP streams and the load
      // balancing in a site's upstreams are the gateway's other half, so the
      // section sits beside Network rather than under Server configuration.
      {
        title: "Proxy & TLS",
        href: "/proxy",
        icon: Globe,
        children: [
          { title: "Overview", href: "/proxy", icon: GridSquare },
          { title: "Sites", href: "/proxy/sites", icon: Globe },
          { title: "Certificates", href: "/proxy/certificates", icon: LockClosed },
          { title: "TLS report", href: "/proxy/tls", icon: Inspect },
          { title: "Streams", href: "/proxy/streams", icon: ArrowLeftRight },
          { title: "Ports", href: "/proxy/ports", icon: Router },
          { title: "Tunnel", href: "/proxy/tunnel", icon: CloudUpload },
          { title: "Traffic", href: "/proxy/traffic", icon: ChartActivity },
          { title: "Alerts", href: "/proxy/alerts", icon: Bell, capability: "system.admin" },
          { title: "Configuration", href: "/proxy/config", icon: FileText },
        ],
      },
    ],
  },
  {
    label: "Protection",
    items: [
      {
        title: "Security",
        href: "/security",
        icon: Shield,
        children: [
          { title: "Overview", href: "/security", icon: GridSquare },
          { title: "SSH", href: "/security/ssh", icon: SecureConnection },
          { title: "Intrusion", href: "/security/intrusion", icon: ShieldOff },
          { title: "Logins", href: "/security/logins", icon: SignIn },
        ],
      },
      { title: "Backups", href: "/backups", icon: Archive },
    ],
  },
  {
    label: "Advanced",
    items: [
      {
        title: "Server configuration",
        icon: SettingsSliders,
        children: [
          { title: "Packages", href: "/packages", icon: Puzzle },
          {
            title: "System users",
            href: "/system-users",
            icon: Users,
            capability: "system.admin",
          },
          { title: "Audit log", href: "/audit", icon: Notes, capability: "system.admin" },
        ],
      },
    ],
  },
  {
    label: "System",
    items: [
      // "Settings", not "Dashboard": inside a product called Dashboard, an
      // entry called Dashboard said nothing about where it went.
      {
        title: "Settings",
        href: "/dashboard",
        icon: SettingsGear,
        children: [
          { title: "Version", href: "/dashboard", icon: CloudUpload },
          {
            title: "Configuration",
            href: "/dashboard/configuration",
            icon: SettingsSliders,
            capability: "system.admin",
          },
          // "Connect", not the site's domain: the domain said where the link goes but not
          // what the page does, and it wore Version's cloud beside it.
          {
            title: "Connect",
            href: "/dashboard/link",
            icon: Link,
            capability: "system.admin",
          },
        ],
      },
    ],
  },
]

/**
 * The account's own pages. They live in the footer menu and the palette rather
 * than in a nav group, because none of them is a place on the server — it is
 * the same identity menu on every page. Every entry is a leaf, the first one
 * being the section's root, so the menu, the palette and the breadcrumb each
 * point at exactly one page.
 */
export const PERSONAL_NAV: NavChild[] = [
  { title: "Profile", href: "/account", icon: UserSettings },
  { title: "Security", href: "/account/security", icon: Shield },
  { title: "Sessions", href: "/account/sessions", icon: DesktopDevice },
  { title: "API keys", href: "/account/keys", icon: Key },
  { title: "Workspace", href: "/account/workspace", icon: GridSquare },
  { title: "Users", href: "/account/users", icon: Users, capability: "system.admin" },
]

/**
 * The account as a section of the rail, for the one thing the footer menu
 * cannot do: say where you are once you are inside it. Opening any of its
 * pages drills the rail in exactly as Docker's row does, so the six pages are
 * a list you can walk rather than a menu you have to reopen.
 */
export const ACCOUNT_SECTION = {
  title: "Account",
  href: "/account",
  icon: UserSettings,
  children: PERSONAL_NAV,
} satisfies NavItem

/**
 * One deployment's pages, in the order a project is read: what it is, what has
 * shipped, what it is saying, what it is running on, and a way in. The href is
 * a suffix because the project is the prefix — `/deploy/12` plus this.
 *
 * `game` entries belong to a game server and nothing else; the rail learns the
 * profile only once the project's own read lands, so it draws the rest of the
 * list immediately and these two when they are earned.
 */
export type ProjectNavEntry = {
  title: string
  /** Appended to `/deploy/<id>`. Empty is the project's own overview. */
  path: string
  icon: React.ComponentType<{ className?: string }>
  /** Only for a game server. */
  game?: boolean
}

export const PROJECT_NAV: ProjectNavEntry[] = [
  { title: "Overview", path: "", icon: GridSquare },
  { title: "Deployments", path: "/deployments", icon: Layers },
  { title: "Logs", path: "/logs", icon: Logs },
  { title: "Runtime", path: "/runtime", icon: Servers },
  { title: "Console", path: "/console", icon: Terminal },
  { title: "Players", path: "/players", icon: Users, game: true },
  { title: "Server settings", path: "/game-settings", icon: SettingsSliders, game: true },
]

/**
 * The settings destinations, in the order a project is configured: what it
 * is, how it builds, how it runs, what it is given, where it answers, what it
 * keeps, what it depends on, what happens without anybody pressing Deploy,
 * and the things that cannot be undone.
 */
export const PROJECT_SETTINGS_NAV: (ProjectNavEntry & { key: string })[] = [
  { key: "general", title: "General", path: "/settings/general", icon: SettingsGear },
  { key: "build", title: "Build", path: "/settings/build", icon: Box },
  { key: "runtime", title: "Runtime", path: "/settings/runtime", icon: Cpu },
  { key: "variables", title: "Variables", path: "/settings/variables", icon: Key },
  { key: "domains", title: "Domains", path: "/settings/domains", icon: Globe },
  { key: "storage", title: "Storage", path: "/settings/storage", icon: Archive },
  { key: "databases", title: "Databases & backups", path: "/settings/databases", icon: Database },
  { key: "automation", title: "Automation", path: "/settings/automation", icon: Rss },
  { key: "danger", title: "Danger zone", path: "/settings/danger", icon: Warning },
]

/** Whether a nav entry owns the given path. */
export function navMatches(href: string, pathname: string) {
  return href === "/" ? pathname === "/" : pathname === href || pathname.startsWith(`${href}/`)
}

/**
 * Whether the path is this entry's page or one inside it. A group has no path
 * of its own — Metrics, Processes and Logs share no prefix — so it owns
 * whatever its entries do.
 */
export function navOwns(entry: NavEntry, pathname: string): boolean {
  return entry.href === undefined
    ? entry.children.some((child) => navOwns(child, pathname))
    : navMatches(entry.href, pathname)
}

/**
 * The section an entry is the root of, if it is one an account can hide.
 * Only the section's own row counts: Docker's Images is a page *inside* a
 * section, drawn whenever Docker's panel is, and never hidden on its own.
 */
function hideableKey(href: string): SectionKey | null {
  const key = sectionOf(href)
  return key && href === `/${key}` ? key : null
}

/**
 * Whether the rail draws an entry for this role and an account keeping
 * `hidden` out of its sidebar.
 *
 * A hidden section is still drawn while you are inside it — a deployment's
 * domain links into the proxy, and once there the rail has to be able to say
 * where you are — and a group goes when nothing in it is left to draw, which
 * is why the role is asked here rather than beside the call: Server
 * configuration with Packages hidden is empty to an account that cannot see
 * System users or the audit log.
 */
export function shownFor(
  entry: { href?: string; capability?: Capability; children?: NavEntry[] },
  hidden: ReadonlySet<string>,
  pathname: string,
  can: (capability: Capability) => boolean,
): boolean {
  if (entry.capability && !can(entry.capability)) return false
  if (entry.href === undefined) {
    return (entry.children ?? []).some((child) => shownFor(child, hidden, pathname, can))
  }
  const key = hideableKey(entry.href)
  return !key || !hidden.has(key) || navMatches(entry.href, pathname)
}

/**
 * The rail's sections an account can hide, under the rail's own group names
 * and in its order, with the entry that draws each — what the first-run
 * question and the account's Workspace page offer a switch for.
 */
export function hideableSections(
  can: (capability: Capability) => boolean,
): { label: string; sections: { key: SectionKey; entry: NavItem }[] }[] {
  const walk = (entries: NavEntry[]): { key: SectionKey; entry: NavItem }[] =>
    entries.flatMap((entry) => {
      if (entry.capability && !can(entry.capability)) return []
      if (entry.href === undefined) return walk(entry.children)
      const key = hideableKey(entry.href)
      return key ? [{ key, entry }] : []
    })
  return NAV.map((group) => ({ label: group.label, sections: walk(group.items) })).filter(
    (group) => group.sections.length > 0,
  )
}

/** The rail entry that is a section's root: its name and glyph wherever it is named. */
export function sectionEntry(key: SectionKey): NavItem | undefined {
  const find = (entries: NavEntry[]): NavItem | undefined => {
    for (const entry of entries) {
      if (entry.href === `/${key}`) return entry
      const inner = entry.href === undefined ? find(entry.children) : undefined
      if (inner) return inner
    }
    return undefined
  }
  return find(NAV.flatMap((group) => group.items))
}

function chainIn(entries: NavEntry[], pathname: string): NavEntry[] {
  for (const entry of entries) {
    if (entry.children && navOwns(entry, pathname)) {
      return [entry, ...chainIn(entry.children, pathname)]
    }
  }
  return []
}

/**
 * The sections whose panels the rail shows for this path, outermost first —
 * Docker alone for anything under `/docker`, Monitoring then Processes for
 * `/processes/pm2`, the account for anything under `/account`. Empty is a page
 * that stands on its own, and the rail stays on its top-level list.
 */
export function sectionsFor(pathname: string): NavEntry[] {
  for (const group of NAV) {
    const chain = chainIn(group.items, pathname)
    if (chain.length > 0) return chain
  }
  if (navMatches(ACCOUNT_SECTION.href, pathname)) return [ACCOUNT_SECTION]
  return []
}

function locate(
  entries: NavEntry[],
  pathname: string,
  nested: boolean,
): { parents: string[]; title: string } | null {
  for (const entry of entries) {
    // Inside a section a page is where you are or it is not; a section, a
    // group, or a row of the top-level list also owns the pages under it.
    const here = entry.children || !nested ? navOwns(entry, pathname) : entry.href === pathname
    if (!here) continue
    if (entry.children) {
      const inner = locate(
        entry.children.filter((child) => child.href !== entry.href),
        pathname,
        true,
      )
      if (inner) return { parents: [entry.title, ...inner.parents], title: inner.title }
    }
    return { parents: [], title: entry.title }
  }
  return null
}

/**
 * The group, the sections it sits inside, and the page, for anything that has
 * to name the current location. A child's exact path wins over the section's
 * prefix match, so `/docker/images` reads as "Images", not "Docker", and a
 * section's own landing page — which shares its href — reads as the section.
 */
export function navLocation(
  pathname: string,
): { group?: string; parents: string[]; title: string } | null {
  for (const group of NAV) {
    const found = locate(group.items, pathname, false)
    if (found) return { group: group.label, ...found }
  }
  const personal = PERSONAL_NAV.find((i) => i.href === pathname)
  if (personal) return { group: "Account", parents: [], title: personal.title }
  return null
}

export const APP_NAME = "Just Dashboard"

/**
 * What the browser's tab says on a page: "Files · Just Dashboard". An address
 * the nav has no row for takes the section it sits in, and one under no
 * section takes the product's name alone.
 */
export function tabTitle(pathname: string): string {
  const page = navLocation(pathname)?.title ?? sectionsFor(pathname)[0]?.title
  return page ? `${page} · ${APP_NAME}` : APP_NAME
}
