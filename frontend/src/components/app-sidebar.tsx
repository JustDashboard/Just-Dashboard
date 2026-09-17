"use client"

import { useState } from "react"
import Link from "next/link"
import { usePathname } from "next/navigation"
import {
  Archive,
  ArrowLeftRight,
  ArrowUpDown,
  Box,
  ChartActivity,
  ChevronRight,
  CloudUpload,
  CodeBracket,
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
  Logout,
  Logs,
  MagnifyingGlass,
  NetworkDevice,
  Notes,
  Puzzle,
  Route,
  Router,
  Rss,
  SecureConnection,
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
  Wrench,
  Servers,
  Clock,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useCommandPalette } from "@/components/command-palette"
import { Logo, LogoMark } from "@/components/logo"
import { UpdateNotice } from "@/components/update/update-notice"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { UserAvatar, displayNameOf } from "@/components/account/user-avatar"
import type { Capability } from "@/lib/types"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarRail,
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

type NavChild = {
  title: string
  href: string
  icon: React.ComponentType<{ className?: string }>
  /** Hidden unless the signed-in role holds this capability. */
  capability?: Capability
}

type NavItem = {
  title: string
  href: string
  icon: React.ComponentType<{ className?: string }>
  /** Hidden unless the signed-in role holds this capability. */
  capability?: Capability
  /** A feature large enough to be several pages — the row expands to show them. */
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
      { title: "Deployments", href: "/deploy", icon: CloudUpload },
      {
        title: "Docker",
        href: "/docker",
        icon: Box,
        children: [
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

/** Whether a nav entry owns the given path. */
export function navMatches(href: string, pathname: string) {
  return href === "/" ? pathname === "/" : pathname === href || pathname.startsWith(`${href}/`)
}

/**
 * The group, page and — for a nested feature like Docker — the parent it
 * belongs to, for the breadcrumb in the top bar. A child's exact path wins
 * over the parent's prefix match, so `/docker/images` reads as "Images", not
 * "Docker".
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

export function AppSidebar() {
  const pathname = usePathname()
  const { can } = useAuth()
  const palette = useCommandPalette()
  const { state } = useSidebar()
  const collapsed = state === "collapsed"

  const isActive = (href: string) => navMatches(href, pathname)

  return (
    <Sidebar collapsible="icon" className="border-r border-sidebar-border">
      {/* Collapsed, the header's padding has to be the nav's, not its own.
          The rail is 3rem and a rail button is 2rem: at p-3 the header's
          content box is only 1.5rem, so the search button — which keeps its
          full 2rem — overflowed half a rem to the right and sat visibly off
          the line every icon below it is on. */}
      <SidebarHeader className="gap-3 p-3 group-data-[collapsible=icon]:p-2">
        {/* The wordmark alone: no tile, and no "Control panel" strapline under
            it. The strapline named the product category to somebody already
            inside the product, and the tile spent a third of the header's
            width saying nothing the name did not.

            The collapse control sits on this line rather than in a bar across
            every page: it is the rail's own switch, and the rail is the only
            thing it affects. Collapsed there is no room beside the mark, so
            the two stack — the trigger stays reachable in the icon rail, which
            is the state you most need it in. */}
        <div className="flex min-w-0 items-center gap-2 group-data-[collapsible=icon]:flex-col">
          <Link
            href="/"
            className="flex h-8 min-w-0 flex-1 items-center rounded-lg focus-ring group-data-[collapsible=icon]:w-8 group-data-[collapsible=icon]:flex-none group-data-[collapsible=icon]:justify-center"
          >
            <Logo className="group-data-[collapsible=icon]:hidden" />
            <LogoMark className="hidden group-data-[collapsible=icon]:block" />
          </Link>
          <SidebarTrigger className="size-8 shrink-0 rounded-lg text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground" />
        </div>

        {/* The palette is the fastest route to any of fifteen pages, so it gets
            a permanent affordance rather than only a shortcut nobody
            discovers. Collapsed, it keeps its place in the rail as an icon. */}
        <button
          type="button"
          onClick={palette.open}
          // A row in the rail, not a box in it: the rail is a list of places
          // and the palette is the fastest way to any of them, so it is drawn
          // like the entries under it rather than as an input sitting above
          // them.
          className="flex h-8 w-full min-w-0 items-center gap-2 rounded-md px-2 text-left text-body text-muted-foreground focus-ring transition-colors group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:p-0 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
        >
          <MagnifyingGlass className="size-3.5 shrink-0" />
          <span className="flex-1 truncate group-data-[collapsible=icon]:hidden">Search</span>
          <kbd className="pointer-events-none rounded-sm border border-sidebar-border bg-sidebar px-1 font-mono text-micro group-data-[collapsible=icon]:hidden">
            ⌘K
          </kbd>
        </button>
      </SidebarHeader>

      <SidebarContent className="gap-0 px-2">
        {NAV.map((group) => {
          const items = group.items.filter((item) => !item.capability || can(item.capability))
          if (items.length === 0) return null
          return (
            <SidebarGroup key={group.label} className="px-0 py-1.5">
              <SidebarGroupLabel className="eyebrow h-6 px-2">{group.label}</SidebarGroupLabel>
              <SidebarGroupContent>
                <SidebarMenu className="gap-0.5">
                  {items.map((item) =>
                    item.children ? (
                      <NavParent key={item.href} item={item} pathname={pathname} />
                    ) : (
                      <SidebarMenuItem key={item.href}>
                        <SidebarMenuButton
                          asChild
                          isActive={isActive(item.href)}
                          tooltip={item.title}
                          className="h-8 text-body"
                        >
                          <Link href={item.href}>
                            <item.icon className="size-4" />
                            <span>{item.title}</span>
                          </Link>
                        </SidebarMenuButton>
                      </SidebarMenuItem>
                    ),
                  )}
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
          )
        })}
      </SidebarContent>

      <SidebarFooter className="gap-2 p-2">
        {/* Above the account card, not below it and not in the nav. It is the
            one thing in this shell that is about the dashboard rather than
            about the server, and it has to be seen without being looked for —
            a page for it would be a page nobody visits, which is how a
            self-hosted panel ends up eighteen months behind. It renders
            nothing when there is nothing to say. */}
        <UpdateNotice collapsed={collapsed} />
        <UserCard collapsed={collapsed} />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}

/**
 * A nav row for a feature that spans several pages — Docker, Databases, Proxy,
 * Security. The row itself is *only* a disclosure toggle; it navigates nowhere.
 * The feature's landing page is the first child, "Overview", so every
 * destination in the section is a leaf in the list and "where am I" always
 * points at exactly one row.
 *
 * Collapsed to the icon rail there is no room for a child list, so the row
 * falls back to a plain link to the Overview with the section's tooltip.
 *
 * Open state is a single `useState` seeded from the route and forced back open
 * whenever navigation lands anywhere in the section, so the active child is
 * never hidden. Leaving the section keeps the last state rather than snapping
 * shut, now that the collapse is animated.
 */
function NavParent({ item, pathname }: { item: NavItem; pathname: string }) {
  const { state } = useSidebar()
  const { can } = useAuth()
  const inSection = navMatches(item.href, pathname)
  const [open, setOpen] = useState(inSection)

  const [seenPath, setSeenPath] = useState(pathname)
  if (pathname !== seenPath) {
    setSeenPath(pathname)
    if (inSection) setOpen(true)
  }

  if (state === "collapsed") {
    return (
      <SidebarMenuItem>
        <SidebarMenuButton
          asChild
          isActive={inSection}
          tooltip={item.title}
          className="h-8 text-body"
        >
          <Link href={item.href}>
            <item.icon className="size-4" />
            <span>{item.title}</span>
          </Link>
        </SidebarMenuButton>
      </SidebarMenuItem>
    )
  }

  const children: NavChild[] = [
    { title: "Overview", href: item.href, icon: GridSquare },
    // A sub-page can be privileged even where its parent is not: the
    // dashboard's own settings describe this install's perimeter, which is not
    // something a read-only operator needs on screen.
    ...(item.children ?? []).filter((child) => !child.capability || can(child.capability)),
  ]

  return (
    <Collapsible asChild open={open} onOpenChange={setOpen}>
      <SidebarMenuItem>
        <CollapsibleTrigger asChild>
          {/* No `tooltip` prop: its content is hidden unless the rail is
              collapsed, and this branch only renders when it is not — passing
              it would wrap the button in <Tooltip>, which swallows the
              trigger's click. The chevron rotates off the button's own
              `data-state`, which the trigger always carries. */}
          <SidebarMenuButton
            aria-label={`${open ? "Collapse" : "Expand"} ${item.title}`}
            className={cn(
              "h-8 text-body [&>svg:last-child]:transition-transform [&>svg:last-child]:duration-200",
              "data-[state=open]:[&>svg:last-child]:rotate-90",
              // Somewhere in this section: quiet accent tint and a
              // primary-coloured icon, so the row reads as "you are in here"
              // without competing with the solid pill on the active child.
              inSection && "bg-sidebar-accent/60 [&>svg:first-child]:text-primary",
            )}
          >
            <item.icon className="size-4" />
            <span className="flex-1 truncate">{item.title}</span>
            {/* No explicit colour — inherits the row's, so it follows the
                hover and in-section states instead of staying one flat grey. */}
            <ChevronRight className="size-4 shrink-0 opacity-70" />
          </SidebarMenuButton>
        </CollapsibleTrigger>
        <CollapsibleContent className="overflow-hidden data-[state=closed]:animate-collapsible-up data-[state=open]:animate-collapsible-down motion-reduce:animate-none">
          <SidebarMenuSub className="mr-0 gap-0.5">
            {children.map((child) => (
              <SidebarMenuSubItem key={child.href}>
                <SidebarMenuSubButton
                  asChild
                  isActive={pathname === child.href}
                  className="transition-colors"
                >
                  <Link href={child.href}>
                    <child.icon className="size-4" />
                    <span>{child.title}</span>
                  </Link>
                </SidebarMenuSubButton>
              </SidebarMenuSubItem>
            ))}
          </SidebarMenuSub>
        </CollapsibleContent>
      </SidebarMenuItem>
    </Collapsible>
  )
}

/**
 * Who is signed in, and everything that belongs to them rather than to the
 * server: the account pages and signing out.
 *
 * A card at the foot of the rail rather than five more nav rows, because none
 * of it is a place in the product — it is the same identity menu on every
 * page, and mixing it into the nav made the nav look longer than it is.
 *
 * The menu opens with the same picture and name as the card, larger, with the
 * sign-in name and role under them: the one place in the product that says
 * plainly which account this is. What used to be a caption there — "two-factor
 * not enrolled" — is now a `Status` on the Security row, where it is a reading
 * beside the page that changes it.
 */
function UserCard({ collapsed }: { collapsed: boolean }) {
  const pathname = usePathname()
  const { status, logout, can } = useAuth()
  const user = status?.user
  const name = user ? displayNameOf(user) : "not signed in"
  const twoFactor = Boolean(user?.totpEnabled)
  const entries = PERSONAL_NAV.filter((item) => !item.capability || can(item.capability))

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={`Account menu for ${name}`}
          className="flex w-full min-w-0 items-center gap-2.5 rounded-md p-1.5 text-left focus-ring transition-colors group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:p-0 hover:bg-sidebar-accent data-[state=open]:bg-sidebar-accent"
        >
          {user ? (
            <UserAvatar user={user} size="sm" />
          ) : (
            <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-plot-brand text-hint font-semibold text-brand">
              ?
            </span>
          )}
          <span className="grid min-w-0 flex-1 group-data-[collapsible=icon]:hidden">
            <span className="truncate text-body leading-tight font-medium">{name}</span>
            <span className="truncate text-hint leading-tight text-muted-foreground capitalize">
              {user?.role ?? "—"}
            </span>
          </span>
          <ArrowUpDown className="size-3.5 shrink-0 text-muted-foreground group-data-[collapsible=icon]:hidden" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        side={collapsed ? "right" : "top"}
        align="start"
        className="w-64 p-0"
        sideOffset={8}
      >
        {user && (
          <div className="flex min-w-0 items-center gap-3 px-3 py-3">
            <UserAvatar user={user} size="md" />
            <div className="min-w-0 flex-1">
              <p className="truncate text-body leading-tight font-medium">{name}</p>
              <p className="mt-0.5 flex min-w-0 items-center gap-1.5 text-hint leading-tight text-muted-foreground">
                <span className="truncate">@{user.username}</span>
                <Tag>{user.role}</Tag>
              </p>
            </div>
          </div>
        )}
        <DropdownMenuSeparator className="my-0" />
        <div className="p-1">
          {entries.map((item) => (
            <DropdownMenuItem key={item.href} asChild>
              <Link
                href={item.href}
                data-active={item.href === pathname || undefined}
                className="data-[active]:bg-accent"
              >
                <item.icon className="size-4" />
                <span className="flex-1">{item.title}</span>
                {item.href === "/account/security" && (
                  <Status
                    tone={twoFactor ? "running" : "notice"}
                    label={twoFactor ? "2FA on" : "2FA off"}
                    className="text-hint"
                  />
                )}
              </Link>
            </DropdownMenuItem>
          ))}
        </div>
        <DropdownMenuSeparator className="my-0" />
        <div className="p-1">
          <DropdownMenuItem variant="destructive" onSelect={() => logout()}>
            <Logout className="size-4" />
            Sign out
          </DropdownMenuItem>
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
