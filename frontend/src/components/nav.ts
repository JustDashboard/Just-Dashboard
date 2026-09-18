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
 * The sidebar draws it, the command palette searches it, and a page header's
 * eyebrow names the group it came from. It used to live in `app-sidebar.tsx`
 * beside the markup that rendered it; it is a file of its own now because the
 * rail is no longer the only thing that walks it — a section opens a panel of
 * its own pages, and the deployment section opens a third level for one
 * project, so the lists have readers that are not the rail.
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
 * The rail, top to bottom, in the order a day on the server runs: is the
 * machine well, what is running on it, the tools to work on it, what keeps it
 * safe, and the housekeeping. Deployments open the second group because
 * shipping something is the reason most visits happen; it used to sit fourth
 * in a group called "Operations" between Packages and Backups, where nobody
 * who did not already know looked for it.
 */
export const NAV: { label: string; items: NavItem[] }[] = [
  {
    label: "Server",
    items: [
      { title: "Overview", href: "/", icon: Home },
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
      {
        title: "Databases",
        href: "/databases",
        icon: Database,
        children: [
          { title: "Browse", href: "/databases", icon: GridSquare },
          { title: "Structure", href: "/databases/structure", icon: Table },
          { title: "Diagram", href: "/databases/diagram", icon: Layout },
          { title: "Query", href: "/databases/query", icon: CodeBracket },
          { title: "Find", href: "/databases/find", icon: MagnifyingGlass },
          { title: "Monitor", href: "/databases/monitor", icon: ChartActivity },
          { title: "Generate", href: "/databases/generate", icon: Sparkles },
          { title: "Connection", href: "/databases/connection", icon: Linked },
        ],
      },
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
    label: "System",
    items: [
      { title: "Packages", href: "/packages", icon: Puzzle },
      { title: "System users", href: "/system-users", icon: Users, capability: "system.admin" },
      { title: "Audit log", href: "/audit", icon: Notes, capability: "system.admin" },
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
export const ACCOUNT_SECTION: NavItem = {
  title: "Account",
  href: "/account",
  icon: UserSettings,
  children: PERSONAL_NAV,
}

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
 * The section whose panel the rail shows for this path — Docker's seven pages
 * for anything under `/docker`, the account's five for anything under
 * `/account`. `null` is a page that stands on its own, and the rail stays on
 * its top-level list.
 */
export function sectionFor(pathname: string): NavItem | null {
  for (const group of NAV) {
    for (const item of group.items) {
      if (item.children && navMatches(item.href, pathname)) return item
    }
  }
  if (navMatches(ACCOUNT_SECTION.href, pathname)) return ACCOUNT_SECTION
  return null
}

/**
 * The group, page and — for a nested feature like Docker — the parent it
 * belongs to, for anything that has to name the current location. A child's
 * exact path wins over the parent's prefix match, so `/docker/images` reads as
 * "Images", not "Docker".
 */
export function navLocation(
  pathname: string,
): { group?: string; parent?: string; title: string } | null {
  for (const group of NAV) {
    for (const item of group.items) {
      const child = item.children?.find((c) => c.href === pathname && c.href !== item.href)
      if (child) return { group: group.label, parent: item.title, title: child.title }
      if (navMatches(item.href, pathname)) return { group: group.label, title: item.title }
    }
  }
  const personal = PERSONAL_NAV.find((i) => i.href === pathname)
  if (personal) return { group: "Account", title: personal.title }
  return null
}
