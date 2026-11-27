"use client"

import { useEffect, useState, type CSSProperties } from "react"
import Link from "next/link"
import { usePathname } from "next/navigation"
import { ChevronLeft, ChevronRight, EyeOff, MagnifyingGlass } from "@/components/icons"
import { cn } from "@/lib/utils"
import { notify } from "@/lib/toast"
import { useSessionState } from "@/lib/view-state"
import { useProxyStatusRead } from "@/components/proxy/proxy-context"
import { sectionOf, type SectionKey } from "@/lib/sidebar-sections"
import { useAuth } from "@/hooks/use-auth"
import { useHiddenSections } from "@/hooks/use-hidden-sections"
import { useCommandPalette } from "@/components/shell/command-palette"
import { useMotionPaused } from "@/components/shell/motion-pause"
import { KEYCAP, useLauncherKeys } from "@/components/keycaps"
import { Logo, LogoMark } from "@/components/logo"
import { UpdateNoticeSlot } from "@/components/update/update-notice-slot"
import { HubNotice } from "@/components/hub-link/hub-notice"
import { ServerSwitcher } from "@/components/hub-link/server-switcher"
import { useShellHub } from "@/hooks/use-hub-link"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { UserAvatar, displayNameOf } from "@/components/user-avatar"
import {
  NAV,
  PERSONAL_NAV,
  PROJECT_NAV,
  PROJECT_SETTINGS_NAV,
  groupedChildren,
  navCurrent,
  navMatches,
  navOwns,
  sectionEntry,
  sectionsFor,
  shownFor,
  type NavEntry,
} from "@/components/shell/nav"
import { useNavScopeValue, type NavScope, type NavScopeEntry } from "@/components/shell/nav-scope"
import { engineFor, sectionHref } from "@/components/database/engine"
import { EngineGlyph } from "@/components/database/kit/engine-mark"
import {
  KNOWN_DATABASES_KEY,
  databaseNavGroups,
  knownDatabase,
  type KnownDatabase,
} from "@/components/database/shell/nav-groups"
import { databaseIdFrom } from "@/components/database/shell/routes"
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
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { heldHref, useHeldSessions } from "@/components/remote-desktop/held-sessions"
import { AskAboutPage } from "@/components/assistant/ask-about"
import { REMOTE_DESKTOP_HREF } from "@/lib/remote-desktop"

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
  /** A section's own glyph, drawn before its name where a named scope draws its mark. */
  icon?: NavEntry["icon"]
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
    icon: section.icon,
    // A section whose pages name a heading is drawn under them; one that
    // names none is the single unlabelled list it always was.
    groups: groupedChildren(section.children ?? []).map((group) => ({
      label: group.label,
      items: rowsOf(group.items),
    })),
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
        items: PROJECT_NAV.filter((entry) => !entry.game && !entry.stack).map((entry) => ({
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
 * A database's pages drawn from the route, for the paint before its layout
 * has read the connection and registered the real panel.
 *
 * Which pages a database has depends on what it is, and the route does not
 * say. The tab remembers: the name from the last list of connections, and
 * what the server said it is and can do from the last time its pages were
 * open. So a database opened before in this tab is drawn at once as itself —
 * its name, its mark, its own pages in its own words — and the panel that
 * registers is the one already on screen.
 *
 * Anything less gets no panel from here. An id the list did not hold may be
 * a database made a moment ago, or an address that names nothing; one the
 * list holds and nobody has opened is known only by its driver, and a
 * MariaDB server drawn with the pages of MySQL would lose two of them when
 * it registered. The section's own panel standing a moment longer is better
 * than either.
 */
function databasePlaceholder(id: number, known: KnownDatabase): Panel {
  const engine = engineFor(known)
  return {
    key: `scope:${sectionHref(id)}`,
    named: true,
    title: known.name,
    mark: <EngineGlyph engine={engine} className="size-4" />,
    groups: databaseNavGroups(engine, (section) => sectionHref(id, section)),
  }
}

/**
 * Every panel between the top-level list and where you are, outermost first.
 * The last of them is what the rail draws unless you are looking elsewhere.
 */
function levelsFor(
  pathname: string,
  scope: NavScope | null,
  knownDatabases: Record<string, KnownDatabase>,
): Panel[] {
  const levels: Panel[] = [ROOT, ...sectionsFor(pathname).map(fromSection)]

  const live = scope && navMatches(scope.path, pathname) ? scope : null
  if (live?.replaces) levels[levels.length - 1] = fromScope(live)
  else if (live) levels.push(fromScope(live))
  else {
    const project = projectIdFrom(pathname)
    const database = databaseIdFrom(pathname)
    if (project !== null) levels.push(projectPlaceholder(project))
    else if (database !== null) {
      const known = knownDatabase(knownDatabases, database)
      if (known?.flavor) levels.push(databasePlaceholder(database, known))
    }
  }
  return levels
}

export function AppSidebar() {
  const pathname = usePathname()
  const { can } = useAuth()
  const palette = useCommandPalette()
  const { state, setOpenMobile } = useSidebar()
  const collapsed = state === "collapsed"
  const scope = useNavScopeValue()
  const { hidden } = useHiddenSections()
  const hub = useShellHub()
  const here = sectionOf(pathname)

  const [knownDatabases] = useSessionState<Record<string, KnownDatabase>>(KNOWN_DATABASES_KEY, {})

  const levels = levelsFor(pathname, scope, knownDatabases)
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
  const hrefs = panel.groups.flatMap((group) =>
    group.items.flatMap((item) => (item.href === undefined ? [] : [item.href])),
  )

  // Whether this server has nginx is asked only while the panel on show holds
  // a page that reads it (Proxy & TLS's Streams and Configuration), and the
  // answer is the one the section's layout and the palette share. The last
  // answer is kept for the tab, so a reload inside the section draws the list
  // it will settle on rather than two rows that leave a moment later.
  const asksNginx = panel.groups.some((group) =>
    group.items.some((item) => "needs" in item && item.needs === "nginx"),
  )
  const nginx = useProxyStatusRead(0, asksNginx && can("read")).data?.value.nginx
  const [lastNginx, setLastNginx] = useSessionState<boolean | null>("shell.proxy.nginx", null)
  useEffect(() => {
    if (nginx !== undefined && nginx !== lastNginx) setLastNginx(nginx)
  }, [nginx, lastNginx, setLastNginx])
  const noNginx = asksNginx && (nginx ?? lastNginx) === false

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
    // No edge of its own from `md` up: the page's sheet has one, and the rail
    // steps back onto the window's ground beside it.
    <Sidebar collapsible="icon" className="group-data-[side=left]:border-r-0">
      {/* Collapsed, the header's padding has to be the nav's, not its own.
          The rail is 3rem and a rail button is 2rem: at p-3 the header's
          content box is only 1.5rem, so the search button — which keeps its
          full 2rem — overflowed half a rem to the right and sat visibly off
          the line every icon below it is on. */}
      {/* The product's name, its version and search are the page's banner:
          outside any landmark, the version beside the wordmark was text a
          screen reader met between regions on every page. */}
      {/* Just Hub's head: 12px in from the rail's edge, as the list under it
          is, and everything in it starts 8px further in again, so the
          wordmark, the search glass, the panel's name, the group labels and
          every row's glyph stand on one line down the rail. */}
      <SidebarHeader
        role="banner"
        className="gap-3 px-3 pt-3 pb-2 group-data-[collapsible=icon]:p-2"
      >
        {/* The wordmark alone: no tile, and no "Control panel" strapline under
            it. The strapline named the product category to somebody already
            inside the product, and the tile spent a third of the header's
            width saying nothing the name did not.

            The collapse control sits on this line rather than in a bar across
            every page: it is the rail's own switch, and the rail is the only
            thing it affects. Collapsed there is no room beside the mark, so
            the two stack — the trigger stays reachable in the icon rail, which
            is the state you most need it in. */}
        <div className="flex h-9 min-w-0 items-center gap-2 group-data-[collapsible=icon]:h-auto group-data-[collapsible=icon]:flex-col">
          {/* On a server linked to a Just Hub the row names this server and
              opens the others; unlinked it is the wordmark, untouched. */}
          {hub ? (
            <ServerSwitcher hub={hub} />
          ) : (
            // A finger's 44px under a coarse pointer, pulled back by its own
            // margin as the trigger beside it is. Pressed on the Overview the
            // address does not change, so it puts the drawer away itself, as
            // a row does.
            <Link
              href="/"
              onClick={() => setOpenMobile(false)}
              className="flex h-8 min-w-0 flex-1 items-center rounded-md px-2 focus-ring group-data-[collapsible=icon]:w-8 group-data-[collapsible=icon]:flex-none group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0 pointer-coarse:-my-1.5 pointer-coarse:h-11"
            >
              <Logo className="group-data-[collapsible=icon]:hidden" />
              <LogoMark className="hidden group-data-[collapsible=icon]:block" />
            </Link>
          )}
          {/* A finger's box in the drawer, pulled back by its own margin so
              the glyph and the row keep the place a 32px control gave them. */}
          <SidebarTrigger className="-my-1.5 -mr-1.5 size-11 shrink-0 rounded-lg text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground md:hidden" />
        </div>

        {/* Ask stands beside Search because it is the other thing a person may
            want from any page; the phone's strip carries its own. */}
        <div className="flex min-w-0 items-center gap-1.5">
          <SearchButton onOpen={palette.open} />
          <AskAboutPage className="h-8 group-data-[collapsible=icon]:hidden max-md:hidden" />
        </div>
      </SidebarHeader>

      {/* `overflow-x-hidden` is what makes the slide a slide: the panel arrives
          from ten pixels outside the rail, and without it those ten pixels are
          a horizontal scrollbar for the length of the animation. */}
      <SidebarContent className="gap-0 overflow-x-hidden px-3 pb-4">
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
          {onRoute && here && hidden.has(here) && <HiddenHere section={here} />}
          {panel.groups.map((group, index) => {
            const items = group.items.filter((item) =>
              shownFor(
                "section" in item ? item.section : item,
                hidden,
                pathname,
                can,
                Boolean(hub),
                !noNginx,
              ),
            )
            if (items.length === 0) return null
            return (
              // A labelled group stands 20px below the one before it, as the
              // Hub's do, so the gap says where one shelf ends before the label
              // says what the next one is. A panel's unlabelled first shelf (a
              // section's landing page) sits closer under the panel's name.
              <SidebarGroup
                key={group.label ?? index}
                className={cn("p-0", group.label ? "pt-5" : "pt-3")}
              >
                {group.label && (
                  <SidebarGroupLabel className="gap-1.5">
                    {group.label}
                    {group.pending && (
                      <span
                        role="img"
                        aria-label="Changes pending"
                        className="mt-1 size-1.5 shrink-0 rounded-full bg-warning"
                      />
                    )}
                  </SidebarGroupLabel>
                )}
                <SidebarGroupContent>
                  <SidebarMenu className="gap-px">
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
                          // plain prefix match would light it up on every page
                          // of the section: `navCurrent` is the rule that
                          // lights one row and only one.
                          active={
                            panel.root
                              ? navMatches(item.href, pathname)
                              : navCurrent(item.href, hrefs, pathname)
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

      <SidebarFooter className="gap-2 px-3 pt-2 pb-3 group-data-[collapsible=icon]:p-2">
        {/* Above the account card, not below it and not in the nav. It is the
            one thing in this shell that is about the dashboard rather than
            about the server, and it has to be seen without being looked for —
            a page for it would be a page nobody visits, which is how a
            self-hosted panel ends up eighteen months behind. It renders
            nothing when there is nothing to say, and its code is fetched
            only when there is. */}
        <UpdateNoticeSlot collapsed={collapsed} />
        {/* What a linked hub has to say, nearest the account card, and
            nothing at all without one. */}
        <HubNotice collapsed={collapsed} />
        {/* A hairline, not a box, between what the rail lists and who is
            using it: the account is a row like the ones above it. */}
        <div aria-hidden className="mx-2 h-px bg-hairline group-data-[collapsible=icon]:hidden" />
        <UserCard collapsed={collapsed} />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}

/**
 * The palette's permanent affordance. It is the fastest route to any of fifty
 * pages and every container, site and database on the box, and the one thing
 * that still sees them all at once now that the rail shows one section at a
 * time — so it is drawn as the search field it opens rather than as one more
 * grey row: Just Hub's 32px control face with the lit top edge of `--edge`,
 * the glass and "Search…" in the muted ink and the shortcut in caps set into
 * it, which is what the eye looks for when it wants to type. The glass is not
 * the mark's ice: in the rail that colour says which page you are on.
 * Collapsed, it keeps its place in the icon rail as the glass alone.
 */
function SearchButton({ onOpen }: { onOpen: () => void }) {
  const keys = useLauncherKeys()
  return (
    // The well is the box and the button is the word in it, because of the
    // keycaps. A control is named by the words it shows (WCAG 2.5.3), and
    // inside the button the caps were words it showed: "Search… Ctrl K" on
    // screen under the name "Search". Beside the button they are the
    // shortcut's picture and nothing else; `aria-keyshortcuts` is how the
    // shortcut is said. The button's `::after` covers the whole well, so a
    // press anywhere on it, the caps included, is a press on Search, and the
    // well draws the ring for the button inside it.
    <div className="group/search relative flex h-11 w-full min-w-0 items-center gap-2 rounded-md border border-transparent border-t-edge bg-control pr-1.5 text-body text-muted-foreground focus-ring-within transition-colors duration-fast group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:border-transparent group-data-[collapsible=icon]:bg-transparent group-data-[collapsible=icon]:p-0 hover:bg-control-hover hover:text-secondary-foreground md:h-8">
      <button
        type="button"
        onClick={onOpen}
        aria-label="Search"
        aria-keyshortcuts={keys.replace("⌘", "Meta")}
        // Stretched over the well's own border, so the button's box is the
        // well's height: 44px under a finger, not 42.
        // 7px of its own after the well's 1px border: the glass lands on the
        // rows' glyph line, 8px in from the well's edge.
        className="-my-px flex min-w-0 flex-1 items-center gap-2.5 self-stretch pl-1.75 text-left outline-hidden group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:pl-0 after:absolute after:-inset-px after:rounded-md"
      >
        <MagnifyingGlass className="size-4 shrink-0" />
        <span className="flex-1 truncate group-data-[collapsible=icon]:hidden">Search…</span>
      </button>
      <span aria-hidden="true" className="flex gap-0.5 group-data-[collapsible=icon]:hidden">
        {keys.split("+").map((key) => (
          <kbd key={key} className={cn(KEYCAP, "group-hover/search:text-foreground")}>
            {key}
          </kbd>
        ))}
      </span>
    </div>
  )
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
      // The same height, inset, glyph, weight and hover as the rows under it:
      // it is a row in the list, one that goes out instead of in. Muted rather
      // than the rows' secondary ink so it does not compete with the page you
      // are on for "where am I".
      className="flex h-11 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body font-medium text-muted-foreground focus-ring transition-colors duration-fast group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0 hover:bg-control hover:text-foreground md:h-8"
    >
      <ChevronLeft className="size-4 shrink-0" />
      <span className="truncate group-data-[collapsible=icon]:hidden">{label}</span>
    </button>
  )
  const Icon = panel.icon

  return (
    <div className="flex flex-col gap-3 pt-5 group-data-[collapsible=icon]:items-center">
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
          are. It is drawn as Just Hub draws its Settings: the name in Title
          semibold and the primary ink, a step above the rows, after the
          section's glyph in the muted ink, on the rows' glyph line. Until
          0.9.1 a section's name was an eyebrow, the voice of the group labels
          under it, so "NETWORK" over "SETUP" and "ACTIVITY" did not say which
          was the panel and which its shelves, and at 11px it read as a
          caption rather than as where you are. The size is what keeps it from
          reading as one more row to press: 16px semibold beside 14px rows.
          A project or a connection is a name somebody typed and is printed as
          written, after its own mark (§4). Collapsed, the icon rail is too
          narrow for it and the section is named by the back button's tooltip
          instead. */}
      <div className="flex h-5.5 min-w-0 items-center gap-2.5 px-2 text-title font-semibold text-foreground group-data-[collapsible=icon]:hidden">
        {!panel.title ? (
          <Skeleton className="h-3.5 w-28" />
        ) : (
          <>
            {panel.mark ? (
              <span aria-hidden="true" className="flex shrink-0">
                {panel.mark}
              </span>
            ) : (
              Icon && <Icon className="size-4 shrink-0 text-muted-foreground" />
            )}
            <span className="truncate">{panel.title}</span>
          </>
        )}
      </div>
    </div>
  )
}

/**
 * The one thing the rail says about a section the account keeps out of it,
 * while you are inside that section anyway.
 *
 * A link brought you here — a domain's Manage on a deployment, a container's
 * row on a database — and the rail drew the section so it could say where
 * you are. Without this line it would look as though the answer to the
 * first-run question had been ignored; with it, the reader learns that hiding
 * is about the sidebar and not about access, and can bring the section back
 * in one press if they turn out to use it after all.
 */
function HiddenHere({ section }: { section: SectionKey }) {
  const { hidden, save } = useHiddenSections()
  const [busy, setBusy] = useState(false)
  const title = sectionEntry(section)?.title ?? section

  const show = async () => {
    setBusy(true)
    try {
      await save([...hidden].filter((key) => key !== section))
      notify.success(`${title} is back in your sidebar`)
    } catch (err) {
      notify.error(`Could not show ${title}`, err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div
      data-slot="hidden-section-note"
      className="mt-3 flex min-w-0 items-start gap-2 rounded-md border border-hairline px-2 py-1.5 text-hint leading-snug text-muted-foreground group-data-[collapsible=icon]:hidden"
    >
      <EyeOff aria-hidden className="mt-px size-3.5 shrink-0" />
      <p className="min-w-0 flex-1">
        {title} is hidden from your sidebar.{" "}
        <button
          type="button"
          onClick={show}
          disabled={busy}
          className="rounded-sm font-medium text-foreground underline-offset-2 focus-ring hover:underline disabled:opacity-50"
        >
          Show it
        </button>
      </p>
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
  const href = useNavHref(item.href)
  const { setOpenMobile } = useSidebar()
  // A row the rail goes into is lit while you are somewhere inside it, which
  // is not the page its link opens: it is the current one of the list, and
  // only a page's own row is the current page.
  const current = active ? ("section" in item ? "true" : "page") : undefined
  const content = (
    <>
      {/* The row's own glyph: muted at rest, the mark's ice on the current
          row (`ui/sidebar.tsx` colours the first glyph only). */}
      <Icon />
      <span className="flex-1 truncate">{item.title}</span>
      {item.pending && (
        <span
          role="img"
          aria-label="Changes pending"
          className="size-1.5 shrink-0 rounded-full bg-warning"
        />
      )}
      {"section" in item && (
        // Faint, as the Hub's: it says the rail goes in, and the row's label
        // is what it is. It steps up to the muted ink with the row's hover,
        // colour only (motion.md: nothing translates on hover).
        <ChevronRight className="-mr-0.5 text-faint transition-colors duration-fast group-hover/menu-button:text-muted-foreground" />
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
          aria-current={current}
          onClick={enter}
        >
          {content}
        </SidebarMenuButton>
      ) : (
        <SidebarMenuButton asChild isActive={active} tooltip={item.title} aria-current={current}>
          <Link
            href={href ?? item.href}
            // A press that opens a page puts the phone's drawer away at once,
            // the page you are already on included: the address does not
            // change for that one, so nothing else would close it. A press
            // that only drills into a panel leaves the drawer open.
            onClick={(event) => {
              if (!enter) return setOpenMobile(false)
              event.preventDefault()
              enter()
            }}
          >
            {content}
          </Link>
        </SidebarMenuButton>
      )}
    </SidebarMenuItem>
  )
}

/**
 * Where a row leads. Remote Desktop leads back into the session still
 * connected, when there is one: the overview is a step back from it, and a
 * press on the rail that dropped the reader on the overview had them hunting
 * for the computer they were just using.
 */
function useNavHref(href: string | undefined) {
  const { latest } = useHeldSessions()
  return href === REMOTE_DESKTOP_HREF && latest ? heldHref(latest) : href
}

/**
 * Who is signed in, and everything that belongs to them rather than to the
 * server: the account pages and signing out.
 *
 * A card at the foot of the rail rather than six more nav rows, because none
 * of it is a place in the product — it is the same identity menu on every
 * page, and mixing it into the nav made the nav look longer than it is. Once
 * you are inside one of those pages the rail does drill into them, the way it
 * does for any other section, so the menu is how you get in and not how you
 * move around.
 *
 * The menu opens with the same picture and name as the card, with the sign-in
 * name beneath and role at the edge: the one place in the product that says
 * plainly which account this is. What used to be a caption there — "two-factor
 * not enrolled" — is now a `Status` on the Security row, where it is a reading
 * beside the page that changes it.
 *
 * Above the card the menu is exactly the card's width, so the two read as one
 * object that opened rather than a wider panel overhanging the rail. It grows
 * out of the card, and its rows `rise` one after another starting from the row
 * nearest the card, which is what says the list came from the card rather than
 * appearing over it.
 */
function UserCard({ collapsed }: { collapsed: boolean }) {
  const pathname = usePathname()
  const { status, logout, can } = useAuth()
  const user = status?.user
  const name = user ? displayNameOf(user) : "not signed in"
  const twoFactor = Boolean(user?.totpEnabled)
  const entries = PERSONAL_NAV.filter((item) => !item.capability || can(item.capability))
  const [motionPaused, setMotionPaused] = useMotionPaused()
  // Reduced motion collapses durations but not delays, so the stagger is motion-safe only.
  const stagger = "animate-rise motion-safe:[animation-delay:var(--rise-at)]"
  const lastRow = entries.length + 2
  const riseAt = (row: number) => ({ "--rise-at": `${(lastRow - row) * 18}ms` }) as CSSProperties

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          // The name starts with the words the card shows, so someone who
          // says what they see ("click Operator") reaches it; what the card
          // opens comes after them.
          aria-label={[name, user?.role, "account menu"].filter(Boolean).join(", ")}
          // Just Hub's account row: no frame of its own, the rows' hover, and
          // the face, the name and the role on the rows' glyph line. 48px, so
          // it is a finger's box on a phone as well.
          className="group/account flex h-12 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left focus-ring transition-colors duration-fast group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:p-0 hover:bg-control active:bg-control-active data-[state=open]:bg-control"
        >
          {user ? (
            <UserAvatar user={user} size="sm" />
          ) : (
            <span
              data-slot="user-avatar"
              className="flex size-7 shrink-0 items-center justify-center rounded-full bg-plot-brand text-xs font-semibold text-brand"
            >
              ?
            </span>
          )}
          {/* The space between the two lines is for whoever reads them as
              one run of text: without it the card says "Operatoradmin". */}
          <span className="grid min-w-0 flex-1 group-data-[collapsible=icon]:hidden">
            <span className="truncate text-body font-medium text-foreground">{name}</span>{" "}
            <span className="truncate text-hint text-muted-foreground capitalize">
              {user?.role ?? "—"}
            </span>
          </span>
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        side={collapsed ? "right" : "top"}
        align={collapsed ? "end" : "start"}
        aria-label="Account"
        aria-labelledby={undefined}
        className={cn(
          "max-w-[calc(100vw-1rem)] rounded-lg p-1 data-[state=closed]:duration-100 data-[state=open]:duration-200 data-[state=open]:ease-[cubic-bezier(0.16,1,0.3,1)]",
          collapsed ? "w-60" : "w-(--radix-dropdown-menu-trigger-width)",
        )}
        sideOffset={6}
        collisionPadding={8}
      >
        {user && (
          <div
            style={riseAt(0)}
            className={cn("flex min-w-0 items-center gap-2.5 px-2 py-1.5", stagger)}
          >
            <UserAvatar user={user} size="sm" />
            <div className="min-w-0 flex-1">
              <p className="truncate text-body leading-tight font-semibold">{name}</p>
              <p className="mt-0.5 truncate text-hint leading-tight text-muted-foreground">
                @{user.username}
              </p>
            </div>
            <Tag className="shrink-0">{user.role}</Tag>
          </div>
        )}
        <DropdownMenuSeparator className="mx-2 bg-hairline" />
        {entries.map((item, index) => (
          <DropdownMenuItem
            key={item.href}
            asChild
            style={riseAt(index + 1)}
            className={cn(
              "min-h-11 gap-2.5 rounded-md px-2 font-normal focus-ring-inset sm:min-h-8",
              stagger,
            )}
          >
            <Link
              href={item.href}
              data-active={item.href === pathname || undefined}
              aria-current={item.href === pathname ? "page" : undefined}
              className="data-[active]:bg-accent data-[active]:font-medium"
            >
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
        <DropdownMenuSeparator className="mx-2 bg-hairline" />
        {/* This browser's, not the account's, so it stands apart from the
            account's pages: the same request as the system's reduced-motion
            setting, for this product alone. The menu stays open on a press,
            so the mark is seen to change. */}
        <DropdownMenuCheckboxItem
          checked={motionPaused}
          onCheckedChange={setMotionPaused}
          onSelect={(event) => event.preventDefault()}
          style={riseAt(entries.length + 1)}
          className={cn("gap-2.5 rounded-md pl-2 font-normal focus-ring-inset", stagger)}
        >
          Pause animations
        </DropdownMenuCheckboxItem>
        <DropdownMenuItem
          onSelect={() => logout()}
          style={riseAt(entries.length + 2)}
          className={cn(
            "min-h-11 gap-2.5 rounded-md px-2 font-normal focus-ring-inset sm:min-h-8",
            stagger,
          )}
        >
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
