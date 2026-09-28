"use client"

import { useState } from "react"
import Link from "next/link"
import { usePathname } from "next/navigation"
import { ArrowUpDown, ChevronLeft, ChevronRight, Logout, MagnifyingGlass } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useCommandPalette } from "@/components/command-palette"
import { Logo, LogoMark } from "@/components/logo"
import { UpdateNotice } from "@/components/update/update-notice"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { UserAvatar, displayNameOf } from "@/components/account/user-avatar"
import {
  NAV,
  PERSONAL_NAV,
  PROJECT_NAV,
  PROJECT_SETTINGS_NAV,
  navMatches,
  navOwns,
  sectionsFor,
  type NavEntry,
} from "@/components/nav"
import { useNavScopeValue, type NavScope, type NavScopeEntry } from "@/components/nav-scope"
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
  SidebarRail,
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar"
import { Skeleton } from "@/components/ui/skeleton"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

/**
 * The rail, and everything it can become.
 *
 * It used to be one list with four of its rows able to unfold into a second
 * list underneath. Six sections unfolded to between two and eight pages each,
 * so a rail with Docker and Security open was thirty rows deep and the thing
 * you were actually looking at sat somewhere in the middle of it; and because
 * the rail could not be trusted to show a section's pages, every one of those
 * sections also carried a strip of tabs across the top of its pages saying the
 * same thing a second time.
 *
 * Now the rail goes *in*. Opening Docker replaces the list of everything with
 * the list of Docker, under Docker's own name and a way back. One list at a
 * time, always the list for where you are, and the tab strips are gone — a
 * page is a page, and the only thing that says which one you are on is the
 * rail.
 *
 * Which panel is showing is read off the route, not remembered, so a link
 * pasted into a browser opens with the rail already inside the right section.
 * The one piece of state is a look elsewhere without leaving the page you are
 * on — the step back out, or a group opened from the list — and it is dropped
 * the moment you navigate.
 */

/** One drawn row: a page to go to, or a section or group to go into. */
type Row = NavScopeEntry | SectionRow

/**
 * A row the rail goes into. A section's row is still a link to its landing
 * page; a group's has no page to link to, so it only opens the panel.
 */
type SectionRow = Omit<NavScopeEntry, "href"> & { href?: string; section: NavEntry }

function rowsOf(entries: NavEntry[]): Row[] {
  return entries.map((entry) =>
    entry.href === undefined || entry.children ? { ...entry, section: entry } : entry,
  )
}

type Panel = {
  key: string
  /** Absent on the top-level list, and on a panel whose subject has not loaded. */
  title?: string
  /**
   * The title is a name the reader gave something — a project, a connection —
   * rather than one of this product's words, so it is printed as written.
   */
  named?: boolean
  mark?: React.ReactNode
  /** The top-level list is never titled and has nothing to go back to. */
  root?: boolean
  groups: { label?: string; pending?: boolean; items: Row[] }[]
}

const ROOT: Panel = {
  key: "root",
  root: true,
  groups: NAV.map((group) => ({ label: group.label, items: rowsOf(group.items) })),
}

function fromSection(section: NavEntry): Panel {
  return {
    key: `section:${section.href ?? section.title}`,
    title: section.title,
    groups: [{ items: rowsOf(section.children ?? []) }],
  }
}

function fromScope(scope: NavScope): Panel {
  return {
    key: `scope:${scope.path}`,
    title: scope.title,
    named: true,
    mark: scope.mark,
    groups: scope.groups,
  }
}

/**
 * A project's pages drawn from the route alone, for the paint before the
 * project's own read lands and registers the real panel. Same rows in the same
 * order, so what arrives is the name and the two game pages rather than a
 * different shape of list — a rail that reflows on every project you open is
 * worse than one that takes a moment to say which project it is.
 */
function projectPlaceholder(id: number): Panel {
  const base = `/deploy/${id}`
  return {
    key: `scope:${base}`,
    named: true,
    groups: [
      {
        items: PROJECT_NAV.filter((entry) => !entry.game).map((entry) => ({
          title: entry.title,
          href: `${base}${entry.path}`,
          icon: entry.icon,
        })),
      },
      {
        label: "Settings",
        items: PROJECT_SETTINGS_NAV.map((entry) => ({
          title: entry.title,
          href: `${base}${entry.path}`,
          icon: entry.icon,
        })),
      },
    ],
  }
}

/** The project a path belongs to, or `null`. A run is one of its Deployments. */
function projectIdFrom(pathname: string): number | null {
  const match = /^\/deploy\/(\d+)(?:\/.*)?$/.exec(pathname)
  return match ? Number(match[1]) : null
}

/**
 * Every panel between the top-level list and where you are, outermost first.
 * The last of them is what the rail draws unless you are looking elsewhere.
 */
function levelsFor(pathname: string, scope: NavScope | null): Panel[] {
  const levels: Panel[] = [ROOT, ...sectionsFor(pathname).map(fromSection)]

  const live = scope && navMatches(scope.path, pathname) ? scope : null
  if (live?.replaces) levels[levels.length - 1] = fromScope(live)
  else if (live) levels.push(fromScope(live))
  else {
    const project = projectIdFrom(pathname)
    if (project !== null) levels.push(projectPlaceholder(project))
  }
  return levels
}

export function AppSidebar() {
  const pathname = usePathname()
  const { can } = useAuth()
  const palette = useCommandPalette()
  const { state } = useSidebar()
  const collapsed = state === "collapsed"
  const scope = useNavScopeValue()

  const levels = levelsFor(pathname, scope)
  const chain = sectionsFor(pathname)

  // Looking elsewhere — back out a level, or into a group — is the one thing
  // here the route does not decide, and it survives exactly until the next
  // navigation: you go looking for something, and finding it puts the rail
  // wherever that something lives.
  const [view, setView] = useState<Panel[] | null>(null)
  const [seenPath, setSeenPath] = useState(pathname)
  if (pathname !== seenPath) {
    setSeenPath(pathname)
    setView(null)
  }

  const stack = view ?? levels
  const depth = stack.length - 1
  const panel = stack[depth]
  const parent = depth > 0 ? stack[depth - 1] : null
  const onRoute = stack.every((level, index) => level.key === levels[index]?.key)

  // Pressing the section you stepped out of means "put it back", not "throw
  // away the page I was on and open the section's front door". Any other
  // section's row is a link and goes to its landing page; a group has none,
  // so its panel opens over the page you are on.
  function enter(section: NavEntry) {
    if (onRoute && chain[depth] === section) return () => setView(null)
    if (section.href === undefined) return () => setView([...stack, fromSection(section)])
    return undefined
  }

  // Which way the panel came from. Going in arrives from the right, coming
  // back out from the left, which is the only part of this that says a level
  // was crossed rather than a page changed.
  const [lastDepth, setLastDepth] = useState(depth)
  const [inward, setInward] = useState(true)
  if (depth !== lastDepth) {
    setInward(depth > lastDepth)
    setLastDepth(depth)
  }

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

        {/* The palette is the fastest route to any of fifty pages, and the one
            thing that still sees them all at once now that the rail shows one
            section at a time, so it gets a permanent affordance rather than
            only a shortcut nobody discovers. Collapsed, it keeps its place in
            the rail as an icon. */}
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

      {/* `overflow-x-hidden` is what makes the slide a slide: the panel arrives
          from ten pixels outside the rail, and without it those ten pixels are
          a horizontal scrollbar for the length of the animation. */}
      <SidebarContent className="gap-0 overflow-x-hidden px-2">
        {/* The rail is the product's navigation and had no landmark: a screen
            reader met fifty links with nothing saying what list they were. One
            name for every panel, since the panel that is showing says its own
            name in the heading below. */}
        <nav
          aria-label="Sidebar"
          key={panel.key}
          className={cn("animate-drill", inward ? "[--drill-from:10px]" : "[--drill-from:-10px]")}
        >
          {parent && (
            <PanelHead panel={panel} parent={parent} onBack={() => setView(stack.slice(0, -1))} />
          )}
          {panel.groups.map((group, index) => {
            const items = group.items.filter((item) => !item.capability || can(item.capability))
            if (items.length === 0) return null
            return (
              <SidebarGroup key={group.label ?? index} className="px-0 py-1.5">
                {group.label && (
                  <SidebarGroupLabel className="eyebrow h-6 gap-1.5 px-2">
                    {group.label}
                    {group.pending && (
                      <span
                        role="img"
                        aria-label="Changes pending"
                        className="size-1.5 shrink-0 rounded-full bg-warning"
                      />
                    )}
                  </SidebarGroupLabel>
                )}
                <SidebarGroupContent>
                  <SidebarMenu className="gap-0.5">
                    {items.map((item) =>
                      "section" in item ? (
                        <NavRow
                          key={item.href ?? item.title}
                          item={item}
                          active={navOwns(item.section, pathname)}
                          enter={enter(item.section)}
                        />
                      ) : (
                        <NavRow
                          key={item.href}
                          item={item}
                          // A section's own landing page is the first row in
                          // its panel and shares the section's href, so a
                          // prefix match would light it up on every page of the
                          // section. Inside a panel a row is where you are or
                          // it is not.
                          active={
                            panel.root ? navMatches(item.href, pathname) : isCurrent(item, pathname)
                          }
                        />
                      ),
                    )}
                  </SidebarMenu>
                </SidebarGroupContent>
              </SidebarGroup>
            )
          })}
        </nav>
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
 * Whether a row inside a section panel is the page being looked at.
 *
 * An exact match, with one exception: a row whose href carries the section's
 * own state in the query string (the databases panel puts the connection
 * there) is the same page whatever that state says.
 */
function isCurrent(item: NavScopeEntry, pathname: string) {
  const path = item.href.split("?")[0]
  // A run is opened from its project's Deployments, and is one of them.
  const run = /^\/deploy\/(\d+)\/runs\//.exec(pathname)
  if (run) return path === `/deploy/${run[1]}/deployments`
  return path === pathname
}

/**
 * What a section panel says above its pages: the way back out, and what you
 * are inside.
 *
 * The back control names the level above rather than saying "Back", because
 * the rail has several — the top-level list, a group, a section, one
 * deployment — and "back to Deployments" and "back to everything" are
 * different answers.
 * Collapsed to the icon rail the name has nowhere to go, so it becomes the
 * button's tooltip and its accessible name.
 */
function PanelHead({ panel, parent, onBack }: { panel: Panel; parent: Panel; onBack: () => void }) {
  const { state } = useSidebar()
  const collapsed = state === "collapsed"
  const label = parent.title ?? "All pages"

  const back = (
    <button
      type="button"
      onClick={onBack}
      aria-label={`Back to ${label}`}
      // The same height, inset, icon size and type as the rows under it: it
      // is a row in the list, one that goes out instead of in. Muted rather
      // than the rows' foreground so it does not compete with the page you
      // are on for "where am I".
      className="flex h-8 w-full min-w-0 items-center gap-2 rounded-md px-2 text-left text-body text-muted-foreground focus-ring transition-colors group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
    >
      <ChevronLeft className="size-4 shrink-0" />
      <span className="truncate group-data-[collapsible=icon]:hidden">{label}</span>
    </button>
  )

  return (
    <div className="flex flex-col gap-0.5 pt-1.5 group-data-[collapsible=icon]:items-center">
      {collapsed ? (
        // Expanded the button says where it goes, so a tooltip repeating it is
        // noise; in the icon rail the words are gone and it is the only label.
        <Tooltip>
          <TooltipTrigger asChild>{back}</TooltipTrigger>
          <TooltipContent side="right">
            Back to {label}
            {panel.title ? ` · ${panel.title}` : ""}
          </TooltipContent>
        </Tooltip>
      ) : (
        back
      )}

      {/* Where you are, as a heading rather than a link: the panel under it is
          the section, so a control here would only ever go where you already
          are. A section is drawn as an eyebrow — the rail's label voice, the
          one "Settings" above a group speaks in — and not as icon-plus-name in
          the row slot, which is exactly what a row looks like and so read as a
          button that did nothing when pressed. A project or a connection is a
          name somebody typed, and small caps turned "api-production" into
          API-PRODUCTION (§4), so it is printed as written, after its own mark
          at the line's height rather than in a row's icon slot. Collapsed, the
          icon rail is too narrow for it and the section is named by the back
          button's tooltip instead. */}
      <div className="min-w-0 px-2 pt-3 pb-1.5 group-data-[collapsible=icon]:hidden">
        {!panel.title ? (
          <Skeleton className="h-2.5 w-24" />
        ) : panel.named ? (
          <p className="flex min-w-0 items-center gap-2 text-title leading-tight font-semibold text-foreground">
            {panel.mark && (
              <span aria-hidden="true" className="flex shrink-0">
                {panel.mark}
              </span>
            )}
            <span className="truncate">{panel.title}</span>
          </p>
        ) : (
          <p className="eyebrow truncate leading-tight">{panel.title}</p>
        )}
      </div>
    </div>
  )
}

/**
 * A row in whichever panel is showing. A page, or a section or group, which is
 * the same row with a chevron saying the rail goes in rather than the page
 * changes.
 */
function NavRow({
  item,
  active,
  enter,
}: {
  item: Row
  active: boolean
  /** Opens the row's panel instead of following its link; see `enter`. */
  enter?: () => void
}) {
  const Icon = item.icon
  const content = (
    <>
      <Icon className="size-4" />
      <span className="flex-1 truncate">{item.title}</span>
      {item.pending && (
        <span
          role="img"
          aria-label="Changes pending"
          className="size-1.5 shrink-0 rounded-full bg-warning"
        />
      )}
      {"section" in item && (
        // No explicit colour — inherits the row's, so it follows the hover
        // and active states instead of staying one flat grey.
        <ChevronRight className="size-4 shrink-0 opacity-70" />
      )}
    </>
  )
  return (
    <SidebarMenuItem>
      {item.href === undefined ? (
        <SidebarMenuButton
          type="button"
          isActive={active}
          tooltip={item.title}
          className="h-8 text-body"
          onClick={enter}
        >
          {content}
        </SidebarMenuButton>
      ) : (
        <SidebarMenuButton asChild isActive={active} tooltip={item.title} className="h-8 text-body">
          <Link
            href={item.href}
            onClick={
              enter &&
              ((event) => {
                event.preventDefault()
                enter()
              })
            }
          >
            {content}
          </Link>
        </SidebarMenuButton>
      )}
    </SidebarMenuItem>
  )
}

/**
 * Who is signed in, and everything that belongs to them rather than to the
 * server: the account pages and signing out.
 *
 * A card at the foot of the rail rather than five more nav rows, because none
 * of it is a place in the product — it is the same identity menu on every
 * page, and mixing it into the nav made the nav look longer than it is. Once
 * you are inside one of those pages the rail does drill into them, the way it
 * does for any other section, so the menu is how you get in and not how you
 * move around.
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
