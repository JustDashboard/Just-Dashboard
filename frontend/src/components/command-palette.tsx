"use client"

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { Logout } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { NAV, PERSONAL_NAV } from "@/components/app-sidebar"
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

function Palette({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const router = useRouter()
  const { can, logout } = useAuth()

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
            const items = group.items.filter((item) => !item.capability || can(item.capability))
            if (items.length === 0) return null
            return (
              <CommandGroup key={group.label} heading={group.label}>
                {items.flatMap((item) => {
                  const rows = [
                    <CommandItem
                      key={item.href}
                      value={`${group.label} ${item.title}`}
                      onSelect={() => run(() => router.push(item.href))}
                    >
                      <item.icon className="size-4" />
                      {item.title}
                    </CommandItem>,
                  ]
                  // A nested feature's pages are reachable here even when the
                  // sidebar is collapsed to the icon rail and hides them.
                  for (const child of item.children ?? []) {
                    if (child.href === item.href) continue
                    // A child can be privileged where its parent is not.
                    if (child.capability && !can(child.capability)) continue
                    rows.push(
                      <CommandItem
                        key={child.href}
                        value={`${group.label} ${item.title} ${child.title}`}
                        onSelect={() => run(() => router.push(child.href))}
                      >
                        <child.icon className="size-4" />
                        <span className="text-muted-foreground">{item.title}</span>
                        {child.title}
                      </CommandItem>,
                    )
                  }
                  return rows
                })}
              </CommandGroup>
            )
          })}

          <CommandSeparator />
          <CommandGroup heading="Account">
            {PERSONAL_NAV.filter((item) => !item.capability || can(item.capability)).map(
              (item) => (
                <CommandItem
                  key={item.href}
                  value={`account ${item.title}`}
                  onSelect={() => run(() => router.push(item.href))}
                >
                  <item.icon className="size-4" />
                  {item.title}
                </CommandItem>
              ),
            )}
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
