"use client"

import { useState, useSyncExternalStore } from "react"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { Job } from "@/lib/types"
import { phaseReading, type PackageHandoff } from "@/lib/network-traffic"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { ProductLogos, hasProductLogo } from "@/components/product-logo"
import { Button } from "@/components/ui/button"

/**
 * The packages installed from a hand-off in this page session. A section that
 * rendered the hand-off is replaced by its installed reading as soon as its
 * own poll sees the package, so what followed the install is kept here and
 * shown by `InstallFollowUp` in the section that took its place.
 */
const recent = new Set<string>()
const listeners = new Set<() => void>()

function announce() {
  for (const listener of listeners) listener()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function markInstalled(pkg: string) {
  recent.add(pkg)
  announce()
}

function dismissInstalled(pkg: string) {
  recent.delete(pkg)
  announce()
}

function useRecentInstall(pkg: string) {
  return useSyncExternalStore(
    subscribe,
    () => recent.has(pkg),
    () => false,
  )
}

/**
 * What a section shows where the tool it reads is not installed: what the
 * tool would do here, drawn as the product it is, and the package to install
 * — through the Packages page's own install route, whose job streams into a
 * console under the button so the reader watches it go in rather than
 * waiting on a spinner. When the job succeeds the section reads again, and
 * what followed the install — configured, active, verified — is read as
 * phases rather than assumed: a package on disk is not a working service.
 *
 * The button is an administrator's: installing a package runs its
 * maintainer's scripts as root (the install route's own capability), so
 * anybody else is told the package's name and nothing to press.
 */
export function InstallHandoff({
  pkg,
  products,
  icon,
  title,
  description,
  onInstalled,
}: {
  pkg: string
  /** The tool drawn as itself, where the logo collection has it. */
  products: string[]
  /** The glyph for a tool no logo names; used when `products` draws nothing. */
  icon?: React.ComponentType<{ className?: string }>
  title: string
  description: React.ReactNode
  onInstalled: () => void
}) {
  const { can } = useAuth()
  const [installed, setInstalled] = useState(false)
  const console_ = useJobConsole({
    onSuccess: () => {
      setInstalled(true)
      markInstalled(pkg)
      onInstalled()
    },
  })
  const [starting, setStarting] = useState(false)
  const install = async () => {
    setStarting(true)
    try {
      console_.attach(await post<Job>("/packages/install", { packages: [pkg] }))
    } catch (err) {
      notify.error(`Could not start installing ${pkg}`, err)
    } finally {
      setStarting(false)
    }
  }
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <EmptyState
        title={title}
        icon={icon}
        mark={products.some(hasProductLogo) ? <ProductLogos ids={products} /> : undefined}
        description={description}
        action={
          can("system.admin") ? (
            <Button
              size="sm"
              onClick={() => void install()}
              pending={starting || console_.running}
              disabled={starting || console_.running}
            >
              Install {pkg}
            </Button>
          ) : (
            <span className="text-hint text-muted-foreground">
              An administrator can install <span className="font-mono">{pkg}</span>.
            </span>
          )
        }
      />
      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onDismiss={console_.dismiss}
        onCancel={console_.cancel}
      />
      {installed && <HandoffPhases pkg={pkg} />}
    </div>
  )
}

/**
 * The phases after an install, in the section that replaced the hand-off,
 * until every phase that applies is done or the reader puts it away.
 */
export function InstallFollowUp({ pkg }: { pkg: string }) {
  const shown = useRecentInstall(pkg)
  if (!shown) return null
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <HandoffPhases pkg={pkg} />
      <span>
        <Button size="xs" variant="ghost" onClick={() => dismissInstalled(pkg)}>
          Dismiss
        </Button>
      </span>
    </div>
  )
}

/** One package's phases, read again every few seconds while it is not working. */
export function HandoffPhases({ pkg }: { pkg: string }) {
  const handoff = usePoll<PackageHandoff>(
    (signal) => get(`/network/handoffs/${encodeURIComponent(pkg)}`, undefined, signal),
    5000,
    [pkg],
  )
  const h = handoff.data
  if (!h) {
    return handoff.error ? <ErrorState error={handoff.error} onRetry={handoff.refresh} /> : null
  }
  const pending = h.phases.find((p) => p.status !== "done" && p.status !== "not_applicable")
  return (
    <Notice
      tone={h.working ? "success" : "warning"}
      title={
        h.working
          ? `${pkg} is installed, running and verified`
          : `${pkg} is installed; it is not a working service yet`
      }
    >
      <ul className="mt-1 flex flex-col gap-1" aria-label={`After installing ${pkg}`}>
        {h.phases.map((p) => {
          const reading = phaseReading(p)
          return (
            <li key={p.key} className="flex flex-wrap items-baseline gap-x-2">
              <Status tone={reading.tone} label={`${reading.label}: ${reading.word}`} />
              <span>{p.detail}</span>
            </li>
          )
        })}
      </ul>
      {pending && <p className="mt-1">Next: {pending.detail}.</p>}
    </Notice>
  )
}
