"use client"

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { CheckCircle, Logout, MagnifyingGlass, Plus, RefreshClockwise } from "@/components/icons"
import { get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useMemoryState, useSessionState } from "@/lib/view-state"
import { useWorkspaceCommands } from "@/components/workspace/commands"
import type { Capability, DbDriverInfo, ProxyReloadResult, ProxyValidation } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { NAV, PERSONAL_NAV, navLocation, type NavEntry, type NavItem } from "@/components/nav"
import { PaletteModal } from "@/components/modal"
import type { ProxyStatus } from "@/components/proxy/proxy-context"
import { warningCount } from "@/components/proxy/config-test"
import { engineFor, sectionHref } from "@/components/database/engine"
import {
  KNOWN_DATABASES_KEY,
  knownDatabase,
  type KnownDatabase,
} from "@/components/database/shell/nav-groups"
import { databaseIdFrom } from "@/components/database/shell/routes"
import { useSearchInventory } from "@/components/command-search/use-inventory"
import { COMMAND_ICONS, KINDS, PAGE_PRODUCTS, ResultMark } from "@/components/command-search/marks"
import { KEYCAP, Keycaps } from "@/components/command-search/keycaps"
import {
  Preview,
  visited,
  type Destination,
  type PreviewItem,
} from "@/components/command-search/preview"
import { Status } from "@/components/status-dot"
import { Spinner } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { useArrivals } from "@/hooks/use-arrivals"
import {
  highlight,
  localDestination,
  parseSearch,
  recentDestinations,
  rememberDestination,
  scopeQuery,
  searchItems,
  SEARCH_SCOPES,
  type RecentDestination,
  type SearchKind,
  type SearchScope,
} from "@/components/command-search/model"
import {
  Command,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"

type PaletteValue = { open: () => void; close: () => void; toggle: () => void }
const PaletteContext = createContext<PaletteValue | null>(null)
type PaletteItem = PreviewItem & {
  run?: () => void
  explicit?: boolean
}

/** What the page's own commands do, for the preview: their labels say only what they are called. */
const COMMAND_HINTS: Record<string, (page: string) => string> = {
  find: (page) => `Moves the cursor to the filter on ${page}.`,
  refresh: (page) => `Reads ${page} from the server again.`,
  help: (page) => `Lists every keyboard shortcut on ${page}.`,
}

function locationName(pathname: string, search: URLSearchParams): RecentDestination {
  const location = navLocation(pathname)
  const selection = ["site", "repo", "unit", "app"].map((key) => search.get(key)).find(Boolean)
  const title = selection ? selection.split("/").at(-1) || selection : location?.title || "Overview"
  return {
    href: `${pathname}${search.size ? `?${search}` : ""}`,
    title,
    detail: location?.parents.join(" · ") || location?.group,
  }
}

export function CommandPaletteProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState("")
  const pathname = usePathname()
  const search = useSearchParams()
  const { status } = useAuth()
  const [history, setHistory] = useMemoryState<RecentDestination[]>(
    `command-search.${status?.user?.id}.recent`,
    [],
  )
  const pending = useRef<RecentDestination | null>(null)
  const returnFocus = useRef<HTMLElement | null>(null)
  const captureFocus = useCallback(() => {
    const active = document.activeElement
    if (active instanceof HTMLElement && !active.closest("[data-command-search]")) {
      returnFocus.current = active
    }
  }, [])
  const href = `${pathname}${search.size ? `?${search}` : ""}`

  useEffect(() => {
    const destination =
      pending.current?.href === href
        ? pending.current
        : locationName(pathname, new URLSearchParams(search.toString()))
    pending.current = null
    setHistory((previous) => rememberDestination(previous, { ...destination, at: Date.now() }))
  }, [href, pathname, search, setHistory])

  const value = useMemo<PaletteValue>(
    () => ({
      open: () => {
        captureFocus()
        setQuery("")
        setOpen(true)
      },
      close: () => {
        setQuery("")
        setOpen(false)
      },
      toggle: () => {
        captureFocus()
        setQuery("")
        setOpen((o) => !o)
      },
    }),
    [captureFocus],
  )

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (
        event.isComposing ||
        event.altKey ||
        event.shiftKey ||
        event.key.toLowerCase() !== "k" ||
        !(event.metaKey || event.ctrlKey)
      )
        return
      event.preventDefault()
      event.stopPropagation()
      if (event.repeat) return
      captureFocus()
      setQuery("")
      setOpen((o) => !o)
    }
    // The published launcher shortcut must reach us before Monaco's chord or a PTY keystroke.
    window.addEventListener("keydown", onKey, true)
    return () => window.removeEventListener("keydown", onKey, true)
  }, [captureFocus])

  return (
    <PaletteContext.Provider value={value}>
      {children}
      <PaletteModal
        className="flex min-h-0 flex-col lg:max-w-4xl"
        open={open}
        onOpenChange={(value) => {
          setQuery("")
          setOpen(value)
        }}
        onEscapeKeyDown={(event) => {
          if (query) {
            event.preventDefault()
            setQuery("")
          }
        }}
        onCloseAutoFocus={(event) => {
          // There is no Radix DialogTrigger for a global keyboard shortcut.
          event.preventDefault()
          if (returnFocus.current?.isConnected) returnFocus.current.focus({ preventScroll: true })
        }}
        label="Command palette"
        description="Search dashboard pages and resources. Use arrows to choose, Enter to open and Escape to close."
      >
        {open && (
          <Palette
            query={query}
            setQuery={setQuery}
            currentHref={href}
            recent={recentDestinations(history, href)}
            close={(restore = true) => {
              if (!restore) returnFocus.current = null
              setQuery("")
              setOpen(false)
            }}
            onNavigate={(destination) => {
              returnFocus.current = null
              pending.current = destination
            }}
          />
        )}
      </PaletteModal>
    </PaletteContext.Provider>
  )
}

export function useCommandPalette() {
  const ctx = useContext(PaletteContext)
  if (!ctx) throw new Error("useCommandPalette must be used inside CommandPaletteProvider")
  return ctx
}

/**
 * Every page under a rail entry the role can open, with the trail of sections
 * above it.
 *
 * A section's landing page is listed once, under the section's name — but the
 * rail calls the same page something else inside the section (Deployments'
 * "Projects", Databases' "Control center", Processes' "Live"), and a search
 * for the word on screen found nothing. Those names are the page's aliases.
 */
function pagesUnder(
  entry: NavEntry,
  can: (capability: Capability) => boolean,
  trail: string[] = [],
  parent?: NavEntry,
): { page: NavItem; trail: string[]; aliases: string[]; parent?: NavEntry }[] {
  if (entry.capability && !can(entry.capability)) return []
  const children: NavEntry[] = entry.children ?? []
  const aliases = children.filter((child) => child.href === entry.href).map((child) => child.title)
  return [
    ...(entry.href === undefined ? [] : [{ page: entry, trail, aliases, parent }]),
    ...children
      .filter((child) => child.href !== entry.href)
      .flatMap((child) => pagesUnder(child, can, [...trail, entry.title], entry)),
  ]
}

const GROUPS = Object.fromEntries(SEARCH_SCOPES) as Record<SearchScope, string>

function Palette({
  query,
  setQuery,
  currentHref,
  recent,
  close,
  onNavigate,
}: {
  query: string
  setQuery: (query: string) => void
  currentHref: string
  recent: RecentDestination[]
  close: (restore?: boolean) => void
  onNavigate: (destination: RecentDestination) => void
}) {
  const { current: workspace } = useWorkspaceCommands()
  const router = useRouter()
  const pathname = usePathname()
  const { can, logout } = useAuth()
  const [selected, setSelected] = useState({ query: "", id: "" })
  const input = useRef<HTMLInputElement>(null)
  const scopes = useRef<HTMLDivElement>(null)
  const { reads, retry } = useSearchInventory(can("read"))
  const inside = databaseIdFrom(pathname)
  const drivers = usePoll(
    (signal) => get<DbDriverInfo[]>("/databases/drivers", undefined, signal),
    0,
    [],
    { enabled: inside !== null && can("read") },
  )
  const proxyStatus = usePoll(
    (signal) => get<ProxyStatus>("/proxy/status", undefined, signal),
    0,
    [],
    { enabled: can("system.admin") },
  )
  const [known] = useSessionState<Record<string, KnownDatabase>>(KNOWN_DATABASES_KEY, {})
  const databaseItems = reads.find((read) => read.source.kind === "database")?.items ?? []
  const current = databaseItems.find((item) => item.id === `database:${inside}`)
  const learned = inside === null ? undefined : knownDatabase(known, inside)
  const currentPages =
    current?.connection && !current.connection.broken && !drivers.loading
      ? engineFor(
          {
            ...current.connection,
            ...(learned?.driver === current.connection.driver ? learned : {}),
          },
          drivers.data,
        ).sections
      : []

  const shown = (entries: NavEntry[]) =>
    entries
      .filter((entry) => !entry.capability || can(entry.capability))
      .map(({ href, title, icon }) => ({ href, title, icon }))
  const pages: PaletteItem[] = [
    ...NAV.flatMap((group) =>
      group.items.flatMap((entry) =>
        pagesUnder(entry, can).map(({ page, trail, aliases, parent }) => ({
          id: `page:${page.href}`,
          kind: "page" as const,
          title: page.title,
          detail: trail.join(" · ") || group.label,
          keywords: [group.label, ...trail, ...aliases, page.href],
          href: page.href,
          icon: page.icon,
          product: PAGE_PRODUCTS[page.href],
          neighbours: parent
            ? { label: parent.title, pages: shown(parent.children ?? []) }
            : { label: group.label, pages: shown(group.items) },
        })),
      ),
    ),
    ...PERSONAL_NAV.filter((item) => !item.capability || can(item.capability)).map((page) => ({
      id: `page:${page.href}`,
      kind: "page" as const,
      title: page.title,
      detail: "Account",
      href: page.href,
      icon: page.icon,
      keywords: ["account", page.href],
      neighbours: { label: "Account", pages: shown(PERSONAL_NAV) },
    })),
    ...(can("system.admin")
      ? [
          {
            id: "page:new-project",
            kind: "page" as const,
            title: "New project",
            detail: "Deployments",
            href: "/deploy/new",
            icon: Plus,
            keywords: ["create", "deploy"],
          },
        ]
      : []),
  ]
  const allPages = [
    ...NAV.flatMap((group) => group.items.flatMap((entry) => pagesUnder(entry, () => true))),
    ...PERSONAL_NAV.map((page) => ({ page, trail: [] })),
  ]
  /** The rail entry a destination is under: its glyph is the recent row's mark. */
  const ownerOf = (href: string) => {
    const path = href.split("?")[0]
    return allPages
      .filter(
        ({ page }) => page.href === path || (page.href !== "/" && path.startsWith(`${page.href}/`)),
      )
      .sort((a, b) => b.page.href.length - a.page.href.length)[0]?.page
  }
  const allowedRecent = recent.filter((entry) => {
    const owner = ownerOf(entry.href)
    return can("read") && (!owner || pages.some((page) => page.href === owner.href))
  })
  const commands: PaletteItem[] = (workspace?.commands ?? [])
    .filter((command) => !command.disabled)
    .map((command) => ({
      id: `command:${command.id}`,
      kind: "command",
      title: command.label,
      detail: workspace?.name,
      keywords: ["this page", workspace?.name ?? ""],
      icon: COMMAND_ICONS[command.id],
      hint: workspace ? COMMAND_HINTS[command.id]?.(workspace.name) : undefined,
      keys: command.keys,
      run: () => requestAnimationFrame(() => requestAnimationFrame(command.run)),
    }))
  if (proxyStatus.data?.nginx && can("system.admin")) {
    commands.push(
      {
        id: "command:reload-nginx",
        kind: "command",
        title: "Reload nginx",
        detail: "Proxy",
        icon: RefreshClockwise,
        hint: "Tests the config first and refuses a broken one, as the proxy overview's Reload does.",
        keywords: ["proxy"],
        explicit: true,
        run: () => void reloadNginx(),
      },
      {
        id: "command:test-nginx",
        kind: "command",
        title: "Test nginx config",
        detail: "Proxy",
        icon: CheckCircle,
        hint: "Runs nginx's own config test and reports what it found. Nothing is reloaded.",
        keywords: ["proxy"],
        explicit: true,
        run: () => void testNginx(),
      },
    )
  }
  commands.push({
    id: "command:logout",
    kind: "command",
    title: "Sign out",
    detail: "Account",
    icon: Logout,
    hint: "Ends this session in this browser.",
    keywords: ["logout"],
    explicit: true,
    run: () => void logout(),
  })
  const resources = reads.flatMap((read) => read.items)
  const byHref = new Map(resources.map((item) => [item.href, item]))
  const linked = new Map<string, PaletteItem[]>()
  for (const item of resources) {
    for (const link of item.links ?? []) linked.set(link, [...(linked.get(link) ?? []), item])
  }
  const items: PaletteItem[] = [
    ...allowedRecent.map((entry, i) => {
      const owner = ownerOf(entry.href)
      // A place opened from search is still that resource: it keeps the
      // readings its row had, so going back shows what is there now.
      const resource = byHref.get(entry.href)
      return {
        ...entry,
        id: `recent:${entry.href}`,
        kind: "recent" as const,
        keywords: i === 0 ? ["previous", "back", "last page"] : [],
        icon: owner?.icon,
        product: entry.product ?? resource?.product ?? (owner && PAGE_PRODUCTS[owner.href]),
        state: resource?.state,
        facts: resource?.facts,
        links: resource?.links,
        previous: i === 0,
      }
    }),
    ...commands,
    ...pages,
    ...currentPages.map((page) => ({
      id: `page:database:${page.id}`,
      kind: "page" as const,
      title: page.title,
      detail: current?.title,
      keywords: ["database", current?.title ?? ""],
      href: sectionHref(inside!, page.id),
      icon: page.icon,
      product: current?.product,
    })),
    ...resources,
  ]
  const parsed = parseSearch(query)
  const searchableIn = (scope: SearchScope) =>
    items.filter((item) => {
      if (
        parsed.text &&
        scope === "all" &&
        item.kind === "recent" &&
        !/^(back|previous|last page)$/i.test(parsed.text)
      )
        return false
      return !item.explicit || !!parsed.text || scope === "command"
    })
  const result = searchItems(searchableIn(parsed.scope), query)
  // What each scope would find for the same words, beside its name: the reader
  // sees that the three hits are a container, a stack and a repository before
  // choosing where to look.
  const counts: Partial<Record<SearchScope, number>> = parsed.text
    ? Object.fromEntries(
        SEARCH_SCOPES.map(([scope]) => [
          scope,
          searchItems(searchableIn(scope), scopeQuery(query, scope)).total,
        ]),
      )
    : {}
  const grouped = new Map<SearchKind, PaletteItem[]>()
  for (const item of result.items) grouped.set(item.kind, [...(grouped.get(item.kind) ?? []), item])
  const visible = [...grouped.values()].flat()
  const active =
    visible.find((item) => selected.query === query && item.id === selected.id) ?? visible[0]
  const arrived = useArrivals(visible.map((item) => item.id))
  const relevant = reads.filter(
    (read) => parsed.scope === "all" || read.source.kind === parsed.scope,
  )
  const loading = relevant.filter((read) => read.loading)
  const failed = relevant.filter((read) => read.error)

  useEffect(() => {
    scopes.current
      ?.querySelector("[aria-pressed=true]")
      ?.scrollIntoView({ block: "nearest", inline: "nearest" })
  }, [parsed.scope])

  const open = (destination: Destination) => {
    if (!localDestination(destination.href)) return
    onNavigate(destination)
    close(false)
    router.push(destination.href)
  }
  const choose = (item: PaletteItem) => {
    if (item.href && localDestination(item.href)) {
      open({ href: item.href, title: item.title, detail: item.detail, product: item.product })
    } else if (item.run) {
      close(false)
      item.run()
    }
  }
  const changeQuery = (value: string) => {
    setQuery(value)
    setSelected({ query: value, id: "" })
  }
  const controlKeys = (event: React.KeyboardEvent) => {
    if (
      event.key === "Escape" ||
      ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k")
    )
      return
    event.stopPropagation()
  }
  // One stop in the tab order for fourteen scopes: Tab from the input lands on
  // the current one and the arrows walk the rest, as in any toolbar.
  const roveScopes = (event: React.KeyboardEvent<HTMLDivElement>) => {
    controlKeys(event)
    const chips = [...event.currentTarget.querySelectorAll("button")]
    const at = chips.findIndex((chip) => chip === document.activeElement)
    const next = { ArrowRight: at + 1, ArrowLeft: at - 1, Home: 0, End: chips.length - 1 }[
      event.key
    ]
    if (at === -1 || next === undefined) return
    event.preventDefault()
    chips[(next + chips.length) % chips.length].focus()
  }
  const chooseScope = (scope: SearchScope) => {
    changeQuery(scopeQuery(query, scope))
    input.current?.focus()
  }

  return (
    <Command
      data-command-search
      label="Search dashboard"
      shouldFilter={false}
      loop
      vimBindings={false}
      value={active?.id ?? ""}
      onValueChange={(id) => setSelected({ query, id })}
      className="min-h-0 **:data-[slot=command-input-wrapper]:h-14 **:data-[slot=command-input-wrapper]:gap-3 **:data-[slot=command-input-wrapper]:border-hairline **:data-[slot=command-input-wrapper]:px-4 [&_[data-slot=command-input-wrapper]>svg]:size-5 [&_[data-slot=command-input-wrapper]>svg]:text-brand [&_[data-slot=command-input-wrapper]>svg]:opacity-100"
      onKeyDown={(event) => {
        if (event.key === "Escape" && query) {
          event.preventDefault()
          event.stopPropagation()
          changeQuery("")
          input.current?.focus()
        }
      }}
    >
      <div className="relative">
        <CommandInput
          ref={input}
          value={query}
          onValueChange={changeQuery}
          placeholder="Search pages, domains, containers…"
          aria-label="Search dashboard"
          className="h-14 text-title"
        />
        {/* Ten inventories arrive one by one over a second or two. A sweep
            along the input's own edge says the list is still growing without
            a spinner competing with the results for the eye. */}
        {loading.length > 0 && (
          <span aria-hidden="true" className="absolute inset-x-0 bottom-0 h-px overflow-hidden">
            <span className="absolute inset-y-0 left-0 w-1/3 animate-sweep bg-brand" />
          </span>
        )}
      </div>
      {/* The scopes as the Files search's underlined strip of words rather
          than chips: fourteen chips each led by a glyph in its own hue were a
          second, louder list above the one being searched. A scope still
          says how many of the results are its kind before it is pressed, and
          typing `db:` presses the same one. */}
      <div
        ref={scopes}
        role="group"
        aria-label="Search scope"
        onKeyDown={roveScopes}
        className="flex shrink-0 [scrollbar-width:none] items-stretch overflow-x-auto border-b border-hairline [mask-image:linear-gradient(to_right,black_calc(100%-2rem),transparent)] pr-8 pl-2 [&::-webkit-scrollbar]:hidden"
      >
        {SEARCH_SCOPES.map(([scope, label]) => {
          const current = scope === parsed.scope
          const count = counts[scope]
          return (
            <button
              key={scope}
              type="button"
              aria-pressed={current}
              tabIndex={current ? 0 : -1}
              onClick={() => chooseScope(scope)}
              className={cn(
                tabClasses(current, "h-10"),
                "px-2.5",
                count === 0 && !current && "opacity-45",
              )}
            >
              {label}
              {!!count && (
                <span className="numeric text-hint text-muted-foreground">
                  {count > 60 ? "60+" : count}
                </span>
              )}
            </button>
          )
        })}
      </div>
      {/* The list beside what is selected in it. The pair holds one height
          while the words change, so the footer does not ride up and down
          under the reader's eye as the results grow and shrink. */}
      <div
        className={cn(
          "grid min-h-0 sm:h-[min(56svh,28rem)]",
          active && "lg:grid-cols-[minmax(0,1fr)_19rem]",
        )}
      >
        <CommandList
          className="max-h-[min(58svh,30rem)] min-h-0 scroll-py-2 py-1.5 sm:max-h-none"
          aria-label="Search results"
        >
          {visible.length === 0 && (
            <div className="flex flex-col items-center gap-3 px-6 py-12 text-center">
              {loading.length ? (
                <Spinner className="size-5 text-muted-foreground" />
              ) : (
                <MagnifyingGlass className="size-5 text-muted-foreground" />
              )}
              <p className="text-body text-muted-foreground">
                {loading.length
                  ? "Searching dashboard resources…"
                  : failed.length
                    ? "No matches in the available results."
                    : "No matches. Try a name, domain or another scope."}
              </p>
              {!loading.length && parsed.scope !== "all" && parsed.text && (
                <button
                  type="button"
                  onClick={() => chooseScope("all")}
                  onKeyDown={controlKeys}
                  className="min-h-11 rounded-sm text-body text-foreground underline underline-offset-4 focus-ring sm:min-h-8"
                >
                  Search everything for “{parsed.text}”
                </button>
              )}
            </div>
          )}
          {[...grouped].map(([kind, entries]) => (
            <CommandGroup
              key={kind}
              className="px-2 py-0 [&_[cmdk-group-heading]]:flex [&_[cmdk-group-heading]]:items-baseline [&_[cmdk-group-heading]]:justify-between [&_[cmdk-group-heading]]:px-2.5 [&_[cmdk-group-heading]]:pt-3 [&_[cmdk-group-heading]]:pb-1 [&_[cmdk-group-heading]]:text-hint"
              heading={
                <>
                  {kind === "command" && entries.every((item) => !item.explicit)
                    ? "This page"
                    : GROUPS[kind]}
                  {parsed.text && <span className="numeric">{entries.length}</span>}
                </>
              }
            >
              {entries.map((item) => (
                <CommandItem
                  key={item.id}
                  value={item.id}
                  onSelect={() => choose(item)}
                  className={cn(
                    "group/result min-h-11 gap-3 rounded-md px-2.5 py-1 text-body data-[selected=true]:bg-accent sm:min-h-9",
                    "before:absolute before:inset-y-2 before:left-0 before:w-0.5 before:rounded-full before:bg-brand before:opacity-0 before:transition-opacity data-[selected=true]:before:opacity-100",
                    arrived.has(item.id) && "animate-rise",
                  )}
                >
                  <ResultMark product={item.product} icon={item.icon ?? KINDS[item.kind].icon} />
                  <span className="flex min-w-0 flex-1 items-baseline gap-2">
                    <span className="max-w-[75%] shrink-0 truncate text-foreground">
                      {item.previous && "Back to "}
                      {highlight(item.title, query).map((run, i) =>
                        run.hit ? (
                          <mark key={i} className="bg-transparent font-medium text-signal">
                            {run.text}
                          </mark>
                        ) : (
                          run.text
                        ),
                      )}
                    </span>
                    {item.detail && (
                      <span className="min-w-0 truncate text-hint text-muted-foreground">
                        {item.detail}
                      </span>
                    )}
                  </span>
                  {item.previous ? (
                    <span className="text-hint text-muted-foreground">Previous</span>
                  ) : item.at ? (
                    <span className="numeric text-hint text-muted-foreground">
                      {visited(item.at)}
                    </span>
                  ) : item.state ? (
                    <Status state={item.state} label={item.state} className="text-hint" />
                  ) : item.href === currentHref ? (
                    <span className="text-hint text-muted-foreground">Current</span>
                  ) : (
                    item.keys && <Keycaps keys={item.keys} className="max-sm:hidden" />
                  )}
                  <kbd
                    aria-hidden="true"
                    className={cn(
                      KEYCAP,
                      "hidden sm:group-data-[selected=true]/result:inline-flex lg:group-data-[selected=true]/result:hidden",
                    )}
                  >
                    ↵
                  </kbd>
                </CommandItem>
              ))}
            </CommandGroup>
          ))}
        </CommandList>
        {active && (
          <Preview
            item={{
              ...active,
              current: active.href === currentHref,
              connected: [
                ...new Set((active.links ?? []).flatMap((link) => linked.get(link) ?? [])),
              ]
                .filter((other) => other.href !== active.href)
                .slice(0, 6),
            }}
            onOpen={open}
          />
        )}
      </div>
      <div
        className="flex shrink-0 flex-wrap items-center justify-between gap-x-4 gap-y-1.5 border-t border-hairline px-4 py-2.5 text-hint text-muted-foreground"
        onKeyDown={controlKeys}
      >
        <span className="hidden items-center gap-3 sm:flex">
          <span className="inline-flex items-center gap-1.5">
            <kbd className={KEYCAP}>↑</kbd>
            <kbd className={KEYCAP}>↓</kbd>
            choose
          </span>
          <span className="inline-flex items-center gap-1.5">
            <kbd className={KEYCAP}>↵</kbd>
            open
          </span>
          <span className="inline-flex items-center gap-1.5">
            <kbd className={KEYCAP}>Tab</kbd>
            scope
          </span>
          <span className="inline-flex items-center gap-1.5">
            <kbd className={KEYCAP}>Esc</kbd>
            {query ? "clear" : "close"}
          </span>
        </span>
        <span className="flex min-w-0 items-center gap-3">
          <span role="status" aria-live="polite" aria-atomic="true">
            {loading.length > 0 ? (
              <TextShimmer>{`Loading ${loading.map((read) => read.source.label).join(", ")}…`}</TextShimmer>
            ) : failed.length > 0 ? (
              `Unavailable: ${failed.map((read) => read.source.label).join(", ")}. Results are incomplete.`
            ) : parsed.text || parsed.scope !== "all" ? (
              `${plural(result.total, "matching result")}${result.total > 60 ? ". Narrow your search to see more" : ""}.`
            ) : (
              "Pages, commands and live resources."
            )}
          </span>
          {failed.length > 0 && (
            <button
              type="button"
              onClick={retry}
              className="min-h-11 shrink-0 rounded-sm text-body text-foreground underline underline-offset-4 focus-ring sm:min-h-8"
            >
              Retry unavailable sources
            </button>
          )}
        </span>
      </div>
    </Command>
  )
}

/**
 * The palette's reload goes through the same route as the overview's
 * Reload, which tests the config first and refuses a broken one.
 */
async function reloadNginx() {
  try {
    const res = await post<ProxyReloadResult>("/proxy/reload", { kind: "nginx" })
    const warnings = warningCount(res.validation)
    notify.success(
      "nginx reloaded",
      warnings > 0
        ? { description: `Its config test has ${plural(warnings, "warning")}.` }
        : undefined,
    )
  } catch (err) {
    notify.error("nginx did not reload", err)
  }
}

async function testNginx() {
  try {
    const validation = await post<ProxyValidation>("/proxy/test", { kind: "nginx" })
    const warnings = warningCount(validation)
    if (!validation.valid) {
      notify.error("nginx's config test failed", undefined, {
        description: "Needs attention on the proxy overview names the lines it failed on.",
      })
    } else {
      notify.success(
        "nginx's config test passed",
        warnings > 0 ? { description: `With ${plural(warnings, "warning")}.` } : undefined,
      )
    }
  } catch (err) {
    notify.error("Couldn't test nginx's config", err)
  }
}
