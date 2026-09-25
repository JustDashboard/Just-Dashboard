import {
  Archive,
  Bell,
  Box,
  ChartActivity,
  Clock,
  CloudUpload,
  CodeBracket,
  Cpu,
  Database,
  DesktopDevice,
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
  Layout,
  LineChart,
  Linked,
  ListOrdered,
  LockClosed,
  Logs,
  MagnifyingGlass,
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
  ShieldOff,
  SignIn,
  Sparkles,
  Table,
  Terminal,
  UserSettings,
  Users,
  Warning,
  Wrench,
  ArrowLeftRight,
} from "@/components/icons"
import type { Capability } from "@/lib/types"

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
 * proxy, packages, system accounts and audit trail are the pages you open to
 * change the server rather than to use it, and sit behind Server configuration.
 */
export const NAV: { label: string; items: NavEntry[] }[] = [
  {
    label: "Server",
    items: [
      { title: "Overview", href: "/", icon: Home },
      {
        title: "Monitoring",
        icon: Monitoring,
        children: [
          { title: "Metrics", href: "/metrics", icon: LineChart },
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
        icon: CloudUpload,
        children: [
          { title: "Projects", href: "/deploy", icon: GridMasonry },
          // Reading the credential list needs system.admin — the backend seals
          // every credential route alike — so the row is drawn only where the
          // destination would not simply refuse it.
          {
            title: "Credentials",
            href: "/deploy/credentials",
            icon: Key,
            capability: "system.admin",
          },
          { title: "Notifications", href: "/deploy/notifications", icon: Bell },
        ],
      },
      {
        title: "Databases",
        href: "/databases",
        icon: Database,
        children: [
          { title: "Overview", href: "/databases", icon: Home },
          { title: "Browse", href: "/databases/browse", icon: GridSquare },
          { title: "Structure", href: "/databases/structure", icon: Table },
          { title: "Diagram", href: "/databases/diagram", icon: Layout },
          { title: "Query", href: "/databases/query", icon: CodeBracket },
          { title: "Find", href: "/databases/find", icon: MagnifyingGlass },
          { title: "Monitor", href: "/databases/monitor", icon: ChartActivity },
          { title: "Advisor", href: "/databases/advisor", icon: Shield },
          { title: "Topology", href: "/databases/topology", icon: Route },
          { title: "Server", href: "/databases/server", icon: Servers },
          { title: "Backups", href: "/databases/backups", icon: Archive },
          { title: "Logs", href: "/databases/logs", icon: Logs },
          { title: "Generate", href: "/databases/generate", icon: Sparkles },
          { title: "Connection", href: "/databases/connection", icon: Linked },
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
      { title: "Terminal", href: "/terminal", icon: Terminal, capability: "terminal" },
      { title: "Files", href: "/files", icon: FolderOpen },
      { title: "Git", href: "/git", icon: GitHubMark },
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
          { title: "Firewall", href: "/security/firewall", icon: FirewallCheck },
          { title: "SSH", href: "/security/ssh", icon: SecureConnection },
          { title: "Intrusion", href: "/security/intrusion", icon: ShieldOff },
          { title: "Connections", href: "/security/connections", icon: NetworkDevice },
          { title: "Logins", href: "/security/logins", icon: SignIn },
          { title: "Network", href: "/security/network", icon: Route },
          { title: "Tools", href: "/security/tools", icon: Wrench },
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
            ],
          },
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
  { title: "Users", href: "/account/users", icon: Users, capability: "system.admin" },
]

/**
 * The account as a section of the rail, for the one thing the footer menu
 * cannot do: say where you are once you are inside it. Opening any of its
 * pages drills the rail in exactly as Docker's row does, so the five pages are
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
