"use client"

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import {
  Archive,
  ArrowLeft,
  ArrowRight,
  Box,
  CheckCircle,
  Clock,
  CloudUpload,
  Database,
  GitBranch,
  Globe,
  Layers,
  Logout,
  Pencil,
  Plus,
  RefreshClockwise,
  Servers,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
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
import {
  localDestination,
  parseSearch,
  recentDestinations,
  rememberDestination,
  scopeQuery,
  searchItems,
  SEARCH_SCOPES,
  type RecentDestination,
  type SearchItem,
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
type PaletteItem = SearchItem & {
  icon?: React.ComponentType<{ className?: string }>
  run?: () => void
  keys?: string
  explicit?: boolean
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
    setHistory((previous) => rememberDestination(previous, destination))
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
        className="flex min-h-0 flex-col"
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

function pagesUnder(
  entry: NavEntry,
  can: (capability: Capability) => boolean,
  trail: string[] = [],
): { page: NavItem; trail: string[] }[] {
  if (entry.capability && !can(entry.capability)) return []
  const children: NavEntry[] = entry.children ?? []
  return [
    ...(entry.href === undefined ? [] : [{ page: entry, trail }]),
    ...children
      .filter((child) => child.href !== entry.href)
      .flatMap((child) => pagesUnder(child, can, [...trail, entry.title])),
  ]
}

const ICONS: Partial<Record<SearchKind, PaletteItem["icon"]>> = {
  recent: Clock,
  project: CloudUpload,
  site: Globe,
  database: Database,
  container: Box,
  stack: Layers,
  repo: GitBranch,
  service: Servers,
  app: Servers,
  backup: Archive,
  board: Pencil,
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

  const pages: PaletteItem[] = [
    ...NAV.flatMap((group) =>
      group.items.flatMap((entry) =>
        pagesUnder(entry, can).map(({ page, trail }) => ({
          id: `page:${page.href}`,
          kind: "page" as const,
          title: page.title,
          detail: trail.join(" · ") || group.label,
          keywords: [group.label, ...trail, page.href],
          href: page.href,
          icon: page.icon,
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
    })),
    ...(can("system.admin")
      ? [
          {
            id: "page:new-project",
            kind: "page" as const,
            title: "New project",
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
  const allowedRecent = recent.filter((entry) => {
    const path = entry.href.split("?")[0]
    const owner = allPages
      .filter(
        ({ page }) => page.href === path || (page.href !== "/" && path.startsWith(`${page.href}/`)),
      )
      .sort((a, b) => b.page.href.length - a.page.href.length)[0]?.page
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
    keywords: ["logout"],
    explicit: true,
    run: () => void logout(),
  })
  const items: PaletteItem[] = [
    ...allowedRecent.map((entry, i) => ({
      ...entry,
      id: `recent:${entry.href}`,
      kind: "recent" as const,
      keywords: i === 0 ? ["previous", "back", "last page"] : [],
    })),
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
    })),
    ...reads.flatMap((read) => read.items),
  ]
  const parsed = parseSearch(query)
  const searchable = items.filter((item) => {
    if (
      parsed.text &&
      parsed.scope === "all" &&
      item.kind === "recent" &&
      !/^(back|previous|last page)$/i.test(parsed.text)
    )
      return false
    return !item.explicit || !!parsed.text || parsed.scope === "command"
  })
  const result = searchItems(searchable, query)
  const grouped = new Map<SearchKind, PaletteItem[]>()
  for (const item of result.items) grouped.set(item.kind, [...(grouped.get(item.kind) ?? []), item])
  const visible = [...grouped.values()].flat()
  const active =
    visible.find((item) => selected.query === query && item.id === selected.id) ?? visible[0]
  const relevant = reads.filter(
    (read) => parsed.scope === "all" || read.source.kind === parsed.scope,
  )
  const loading = relevant.filter((read) => read.loading)
  const failed = relevant.filter((read) => read.error)

  const choose = (item: PaletteItem) => {
    if (item.href && localDestination(item.href)) {
      onNavigate({ href: item.href, title: item.title, detail: item.detail })
      close(false)
      router.push(item.href)
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

  return (
    <Command
      data-command-search
      label="Search dashboard"
      shouldFilter={false}
      loop
      vimBindings={false}
      value={active?.id ?? ""}
      onValueChange={(id) => setSelected({ query, id })}
      className="min-h-0 **:data-[slot=command-input-wrapper]:h-12 [&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-2 [&_[cmdk-group-heading]]:text-micro [&_[cmdk-group-heading]]:font-semibold [&_[cmdk-group-heading]]:tracking-[0.14em] [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group]]:px-2 [&_[cmdk-item]]:min-h-11 [&_[cmdk-item]]:gap-3 [&_[cmdk-item]]:rounded-md [&_[cmdk-item]]:px-2 [&_[cmdk-item]]:py-2 [&_[cmdk-item]]:text-body [&_[cmdk-item][data-selected=true]]:bg-accent"
      onKeyDown={(event) => {
        if (event.key === "Escape" && query) {
          event.preventDefault()
          event.stopPropagation()
          changeQuery("")
          input.current?.focus()
        }
      }}
    >
      <CommandInput
        ref={input}
        value={query}
        onValueChange={changeQuery}
        placeholder="Search pages, domains, containers…"
        aria-label="Search dashboard"
        className="h-12 text-body"
      />
      <div
        className="flex min-h-10 shrink-0 items-center justify-between gap-2 border-b px-4 py-1.5"
        onKeyDown={controlKeys}
      >
        <label className="flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
          Search in
          <select
            aria-label="Search scope"
            value={parsed.scope}
            onChange={(event) => {
              changeQuery(scopeQuery(query, event.target.value as SearchScope))
              input.current?.focus()
            }}
            className="min-h-11 min-w-0 rounded-sm bg-popover px-1 text-body text-foreground focus-ring sm:min-h-8"
          >
            {SEARCH_SCOPES.map(([scope, label]) => (
              <option key={scope} value={scope}>
                {label}
              </option>
            ))}
          </select>
        </label>
        <span className="shrink-0 text-hint text-muted-foreground">
          {parsed.text || parsed.scope !== "all"
            ? result.total > 60
              ? "60+ results"
              : plural(result.total, "result")
            : "Recent & pages"}
        </span>
      </div>
      <CommandList className="max-h-[min(55svh,28rem)] min-h-0" aria-label="Search results">
        {visible.length === 0 && (
          <div className="px-4 py-8 text-center text-body text-muted-foreground">
            {loading.length
              ? "Searching dashboard resources…"
              : failed.length
                ? "No matches in the available results."
                : "No matches. Try a name, domain or another scope."}
          </div>
        )}
        {[...grouped].map(([kind, entries]) => (
          <CommandGroup
            key={kind}
            heading={
              kind === "command" && entries.every((item) => !item.explicit)
                ? "This page"
                : GROUPS[kind]
            }
          >
            {entries.map((item) => {
              const previous = item.kind === "recent" && item.href === allowedRecent[0]?.href
              const Icon = item.icon ?? ICONS[item.kind]
              return (
                <CommandItem key={item.id} value={item.id} onSelect={() => choose(item)}>
                  {previous ? (
                    <ArrowLeft className="size-4" />
                  ) : (
                    Icon && <Icon className="size-4" />
                  )}
                  <span className="min-w-0 flex-1">
                    <span className="block truncate">
                      {previous ? `Back to ${item.title}` : item.title}
                    </span>
                    {item.detail && (
                      <span className="block truncate text-hint text-muted-foreground">
                        {item.detail}
                      </span>
                    )}
                  </span>
                  {previous ? (
                    <span className="text-hint text-muted-foreground">Previous</span>
                  ) : item.href === currentHref ? (
                    <span className="text-hint text-muted-foreground">Current</span>
                  ) : item.keys ? (
                    <kbd className="text-hint text-muted-foreground">{item.keys}</kbd>
                  ) : (
                    <ArrowRight className="size-3 text-muted-foreground" />
                  )}
                </CommandItem>
              )
            })}
          </CommandGroup>
        ))}
      </CommandList>
      <div
        className="shrink-0 space-y-1.5 border-t px-4 py-2.5 text-hint text-muted-foreground"
        onKeyDown={controlKeys}
      >
        <div className="flex items-center justify-between gap-2">
          <span>
            <kbd>↑↓</kbd> choose · <kbd>Enter</kbd> open · <kbd>Esc</kbd>{" "}
            {query ? "clear" : "close"}
          </span>
          <span className="hidden sm:inline">
            Try <kbd>domain:</kbd> or <kbd>db:</kbd>
          </span>
        </div>
        <div role="status" aria-live="polite" aria-atomic="true">
          {loading.length > 0
            ? `Loading ${loading.map((read) => read.source.label).join(", ")}…`
            : failed.length > 0
              ? `Unavailable: ${failed.map((read) => read.source.label).join(", ")}. Results are incomplete.`
              : parsed.text
                ? `${plural(result.total, "matching result")}${result.total > 60 ? ". Narrow your search to see more" : ""}.`
                : "Search live dashboard resources by name or address."}
        </div>
        {failed.length > 0 && (
          <button
            type="button"
            onClick={retry}
            className="min-h-11 rounded-sm text-body text-foreground underline underline-offset-4 focus-ring sm:min-h-8"
          >
            Retry unavailable sources
          </button>
        )}
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
