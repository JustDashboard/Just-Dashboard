"use client"

import { useSyncExternalStore } from "react"
import { cn } from "@/lib/utils"
import { InfoTip } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Switch } from "@/components/ui/switch"

/**
 * Whether a managed network change waits for this session to come back and
 * confirm it, and whether the host can hold one at all.
 *
 * The answer lived inside the shell's notice (`change-confirmation.tsx`),
 * which drew its switch as the first line of all nineteen Network pages, above
 * each page's own title, the read-only ones included. It is a default almost
 * nobody changes, so it is one row on the Network overview now, and the notice
 * is drawn only when there is a change to say something about. The two share
 * this store; it is kept for the tab's life and no longer, so a reload arms
 * the guard again, as it always did.
 */
type Guard = { enabled: boolean; available: boolean }

let guard: Guard = { enabled: true, available: false }
const listeners = new Set<() => void>()

function set(next: Partial<Guard>) {
  const merged = { ...guard, ...next }
  if (merged.enabled === guard.enabled && merged.available === guard.available) return
  guard = merged
  for (const listener of listeners) listener()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

const SERVER: Guard = { enabled: true, available: false }

export function useReconnectGuard() {
  return useSyncExternalStore(
    subscribe,
    () => guard,
    () => SERVER,
  )
}

/** The shell's notice says whether the host can hold a change for confirmation. */
export function setReconnectGuardAvailable(available: boolean) {
  set({ available })
}

export function setReconnectGuardEnabled(enabled: boolean) {
  set({ enabled })
}

/**
 * The guard's one control, on the Network overview: on is the expected answer,
 * so what it does is behind the tip rather than a sentence on every page.
 * Nothing is drawn where the host cannot hold a change, or for a reader who
 * cannot apply one: the shell's notice knows both and says so through the
 * store.
 */
export function ReconnectGuardSwitch({ className }: { className?: string }) {
  const { enabled, available } = useReconnectGuard()
  if (!available) return null
  return (
    <div className={cn("flex min-h-9 min-w-0 items-center gap-2", className)}>
      <label className="flex min-w-0 items-center gap-2.5 text-body">
        <Switch
          aria-label="Require network reconnection confirmation"
          checked={enabled}
          onCheckedChange={setReconnectGuardEnabled}
        />
        <span className="truncate">Confirm managed changes after reconnecting</span>
      </label>
      <InfoTip label="What confirming after reconnecting does">
        A managed network change is applied for 90 seconds first. If this session comes back through
        it and confirms, it is kept; if not, the host restores the settings it had before. Off, a
        change is saved as it is applied.
      </InfoTip>
    </div>
  )
}

/**
 * The guard's place on the Network overview: last on the page, under what the
 * network is doing, because it is a term of how changes are made rather than
 * a reading of the network.
 */
export function ReconnectGuardPanel() {
  const { available } = useReconnectGuard()
  if (!available) return null
  return (
    <Panel plain aria-label="Managed changes">
      <PanelHeader title="Managed changes" />
      <PanelBody>
        <ReconnectGuardSwitch />
      </PanelBody>
    </Panel>
  )
}
