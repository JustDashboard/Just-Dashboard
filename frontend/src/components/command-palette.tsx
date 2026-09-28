"use client"

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { CloudUpload, Logout, Plus } from "@/components/icons"
import { get } from "@/lib/api"
import type { Capability, DeploymentFleet } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { NAV, PERSONAL_NAV, type NavEntry, type NavItem } from "@/components/nav"
import { PaletteModal } from "@/components/modal"
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
 * One keystroke to any of the forty-five destinations in the nav.
 *
 * A server dashboard is navigated by someone who already knows where they are
 * going — they are here because something is wrong at 3am, not to browse. The
 * palette is the shortest path, and it reads the same `NAV` the sidebar does,
 * so a new page appears in both or in neither.
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
