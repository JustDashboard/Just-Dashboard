"use client"

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react"
import { usePathname, useRouter } from "next/navigation"
import { CheckCircle, CloudUpload, Globe, Logout, Plus, RefreshClockwise } from "@/components/icons"
import { get, post } from "@/lib/api"
import { unusableReason } from "@/lib/db-connections"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useSessionState } from "@/lib/view-state"
import type {
  Capability,
  DbConnection,
  DbDriverInfo,
  DeploymentFleet,
  ProxyReloadResult,
  ProxyValidation,
  VHost,
} from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { NAV, PERSONAL_NAV, type NavEntry, type NavItem } from "@/components/nav"
import { PaletteModal } from "@/components/modal"
import type { ProxyStatus } from "@/components/proxy/proxy-context"
import { warningCount } from "@/components/proxy/config-test"
import { engineFor, sectionHref } from "@/components/database/engine"
import { EngineGlyph } from "@/components/database/kit/engine-mark"
import {
  KNOWN_DATABASES_KEY,
  knownDatabase,
  type KnownDatabase,
} from "@/components/database/shell/nav-groups"
import { databaseIdFrom } from "@/components/database/shell/routes"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command"

type PaletteValue = { open: () => void; close: () => void; toggle: () => void }

const PaletteContext = createContext<PaletteValue | null>(null)

/**
 * One keystroke to any destination in the nav.
 *
 * A server dashboard is navigated by someone who already knows where they are
 * going — they are here because something is wrong at 3am, not to browse. The
 * palette is the shortest path, and it reads the same `NAV` the sidebar does,
 * so a new page appears in both or in neither. The destinations the nav
 * cannot list ahead of time — a project, a proxy site, a database and the
 * pages of the one being looked at — are read when it opens.
 */
export function CommandPaletteProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useState(false)

  const value = useMemo<PaletteValue>(
    () => ({
      open: () => setOpen(true),
      close: () => setOpen(false),
      toggle: () => setOpen((o) => !o),
    }),
    [],
  )

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key.toLowerCase() !== "k" || !(event.metaKey || event.ctrlKey)) return
      event.preventDefault()
      setOpen((o) => !o)
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  return (
    <PaletteContext.Provider value={value}>
      {children}
      <Palette open={open} onOpenChange={setOpen} />
    </PaletteContext.Provider>
  )
}

export function useCommandPalette() {
  const ctx = useContext(PaletteContext)
  if (!ctx) throw new Error("useCommandPalette must be used inside CommandPaletteProvider")
  return ctx
}

/**
 * Every page under a rail entry, with the sections it sits inside, so a nested
 * feature's pages are reachable here even when the sidebar is collapsed to the
 * icon rail and hides them. A group is not a page and contributes only what it
 * holds; a section's landing page shares its href and is the section's own row.
 */
function pagesUnder(
  entry: NavEntry,
  can: (capability: Capability) => boolean,
  trail: string[] = [],
): { page: NavItem; trail: string[] }[] {
  // A child can be privileged where its parent is not.
  if (entry.capability && !can(entry.capability)) return []
  const children: NavEntry[] = entry.children ?? []
  return [
    ...(entry.href === undefined ? [] : [{ page: entry, trail }]),
    ...children
      .filter((child) => child.href !== entry.href)
      .flatMap((child) => pagesUnder(child, can, [...trail, entry.title])),
  ]
}

function Palette({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const router = useRouter()
  const { can, logout } = useAuth()
  // Projects are the one destination the nav cannot list ahead of time. Read
  // when the palette opens, not before: a closed palette has no business
  // polling the fleet.
  const fleet = usePoll(
    (signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal),
    0,
    [],
    { enabled: open },
  )
  const projects = fleet.data?.deployments ?? []
  // The proxy's commands and sites, for whoever can open the proxy pages at
  // all; the two commands only for an account that may run them.
  const proxyVisible = NAV.some((group) =>
    group.items.some((item) => pagesUnder(item, can).some(({ page }) => page.href === "/proxy")),
  )
  const proxyStatus = usePoll(
    (signal) => get<ProxyStatus>("/proxy/status", undefined, signal),
    0,
    [],
    { enabled: open && proxyVisible },
  )
  const nginx = !proxyStatus.error && proxyStatus.data?.nginx === true
  const vhosts = usePoll((signal) => get<VHost[]>("/proxy/vhosts", undefined, signal), 0, [], {
    enabled: open && proxyVisible,
  })
  const sites = vhosts.error ? [] : (vhosts.data ?? [])
  // Every saved database is a destination, and the one being looked at has
  // pages of its own that depend on its engine — the catalogue is read only
  // for that one, since it is what says which pages those are.
  const pathname = usePathname()
  const inside = databaseIdFrom(pathname)
  const connections = usePoll(
    (signal) => get<DbConnection[]>("/databases/", undefined, signal),
    0,
    [],
    { enabled: open },
  )
  const drivers = usePoll(
    (signal) => get<DbDriverInfo[]>("/databases/drivers", undefined, signal),
    0,
    [],
    { enabled: open && inside !== null },
  )
  // A saved row says how a server is dialled, not what answered; what the
  // tab has learned of each (MariaDB behind the `mysql` driver, and what it
  // can do) is laid over it, so the pages listed here are the rail's.
  const [known] = useSessionState<Record<string, KnownDatabase>>(KNOWN_DATABASES_KEY, {})
  const databases = (connections.data ?? []).map((conn) => {
    const learned = knownDatabase(known, conn.id)
    return learned?.driver === conn.driver
      ? { ...conn, flavor: learned.flavor, capabilities: learned.capabilities }
      : conn
  })
  const current = databases.find((conn) => conn.id === inside)
  // A connection that no longer opens has one page, where it is repaired or
  // forgotten; the rest of its engine's pages would each open on an error.
  const currentPages =
    current && !current.broken && !drivers.loading ? engineFor(current, drivers.data).sections : []

  const run = useCallback(
    (action: () => void) => {
      onOpenChange(false)
      action()
    },
    [onOpenChange],
  )

  return (
    <PaletteModal
      open={open}
      onOpenChange={onOpenChange}
      label="Command palette"
      description="Jump to a page or change the palette"
    >
      <Command className="[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-micro [&_[cmdk-group-heading]]:font-semibold [&_[cmdk-group-heading]]:tracking-[0.14em] [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group]]:px-2 [&_[cmdk-item]]:gap-2.5 [&_[cmdk-item]]:rounded-md [&_[cmdk-item]]:px-2 [&_[cmdk-item]]:py-2 [&_[cmdk-item]]:text-body">
        <CommandInput placeholder="Jump to a page…" />
        <CommandList className="max-h-[60svh]">
          <CommandEmpty>Nothing matches.</CommandEmpty>

          {NAV.map((group) => {
            const pages = group.items.flatMap((item) => pagesUnder(item, can))
            if (pages.length === 0) return null
            return (
              <CommandGroup key={group.label} heading={group.label}>
                {pages.map(({ page, trail }) => (
                  <CommandItem
                    key={page.href}
                    value={[group.label, ...trail, page.title].join(" ")}
                    onSelect={() => run(() => router.push(page.href))}
                  >
                    <page.icon className="size-4" />
                    {trail.length > 0 && (
                      <span className="text-muted-foreground">{trail[trail.length - 1]}</span>
                    )}
                    {page.title}
                  </CommandItem>
                ))}
              </CommandGroup>
            )
          })}

          {(projects.length > 0 || can("system.admin")) && (
            <CommandGroup heading="Projects">
              {projects.map((project) => (
                <CommandItem
                  key={project.id}
                  value={`project ${project.name} ${project.endpoint ?? ""}`}
                  onSelect={() => run(() => router.push(`/deploy/${project.id}`))}
                >
                  <CloudUpload className="size-4" />
                  {project.name}
                  {project.endpoint && (
                    <span className="truncate text-muted-foreground">
                      {project.endpoint.replace(/^https?:\/\//, "")}
                    </span>
                  )}
                </CommandItem>
              ))}
              {can("system.admin") && (
                <CommandItem
                  value="project new deploy create"
                  onSelect={() => run(() => router.push("/deploy/new"))}
                >
                  <Plus className="size-4" />
                  New project
                </CommandItem>
              )}
            </CommandGroup>
          )}

          {current && currentPages.length > 0 && (
            // The heading is set in small caps, which a name somebody typed
            // must not be (§8): the name is on each row instead.
            <CommandGroup heading="This database">
              {currentPages.map((page) => (
                <CommandItem
                  key={page.id}
                  value={`database ${current.name} ${page.title}`}
                  onSelect={() => run(() => router.push(sectionHref(current.id, page.id)))}
                >
                  <page.icon className="size-4" />
                  <span className="text-muted-foreground">{current.name}</span>
                  {page.title}
                </CommandItem>
              ))}
            </CommandGroup>
          )}

          {databases.length > 0 && (
            <CommandGroup heading="Databases">
              {databases.map((conn) => (
                <CommandItem
                  key={conn.id}
                  // The id keeps two connections of one name apart.
                  value={`database open ${conn.name} ${conn.driver} ${conn.id}`}
                  onSelect={() => run(() => router.push(sectionHref(conn.id)))}
                >
                  <EngineGlyph engine={engineFor(conn, drivers.data)} className="size-4" />
                  <span className="text-muted-foreground">Open</span>
                  {conn.name}
                  {unusableReason(conn) && (
                    <span className="text-muted-foreground" title={unusableReason(conn)}>
                      cannot be opened
                    </span>
                  )}
                </CommandItem>
              ))}
            </CommandGroup>
          )}

          {proxyVisible && ((nginx && can("system.admin")) || sites.length > 0) && (
            <CommandGroup heading="Proxy">
              {nginx && can("system.admin") && (
                <>
                  <CommandItem
                    value="proxy reload nginx"
                    onSelect={() => run(() => void reloadNginx())}
                  >
                    <RefreshClockwise className="size-4" />
                    Reload nginx
                  </CommandItem>
                  <CommandItem
                    value="proxy test nginx config"
                    onSelect={() => run(() => void testNginx())}
                  >
                    <CheckCircle className="size-4" />
                    Test nginx config
                  </CommandItem>
                </>
              )}
              {sites.map((site) => (
                <CommandItem
                  key={`${site.kind}:${site.name}:${site.path}`}
                  value={`site ${site.name} ${site.serverNames.join(" ")}`}
                  onSelect={() =>
                    run(() => router.push(`/proxy/sites?site=${encodeURIComponent(site.name)}`))
                  }
                >
                  <Globe className="size-4" />
                  <span className="text-muted-foreground">Site</span>
                  {site.name}
                </CommandItem>
              ))}
            </CommandGroup>
          )}

          <CommandSeparator />
          <CommandGroup heading="Account">
            {PERSONAL_NAV.filter((item) => !item.capability || can(item.capability)).map((item) => (
              <CommandItem
                key={item.href}
                value={`account ${item.title}`}
                onSelect={() => run(() => router.push(item.href))}
              >
                <item.icon className="size-4" />
                {item.title}
              </CommandItem>
            ))}
            <CommandItem value="sign out logout" onSelect={() => run(() => void logout())}>
              <Logout className="size-4" />
              Sign out
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </Command>
    </PaletteModal>
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
