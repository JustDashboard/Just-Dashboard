"use client"

import { useSyncExternalStore } from "react"
import { get } from "@/lib/api"
import type { DeploymentFleet, DeploymentSummary } from "@/lib/types"

/**
 * The deployment fleet by project id, read once for every mark on the page.
 *
 * A database's readers, a site's upstream, a backup's source and an activity
 * row know a project only by its id, and a list of them would otherwise ask
 * for the fleet once per row. One shared read is asked again only when a mark
 * mounts after it is a minute old: what it answers — the website and whether
 * a release serves it — changes with a deploy, not from second to second.
 */
const FRESH_MS = 60_000
const EMPTY = new Map<number, DeploymentSummary>()

let projects = EMPTY
let readAt = 0
let reading = false
const listeners = new Set<() => void>()

function read() {
  if (reading || Date.now() - readAt < FRESH_MS) return
  reading = true
  get<DeploymentFleet>("/deploy/", { view: "fleet" })
    .then((fleet) => {
      projects = new Map(fleet.deployments.map((deployment) => [deployment.id, deployment]))
      listeners.forEach((listener) => listener())
    })
    // A reader without Deploy's read is refused, and every mark keeps the
    // product or glyph it draws without the fleet.
    .catch(() => undefined)
    .finally(() => {
      readAt = Date.now()
      reading = false
    })
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  read()
  return () => {
    listeners.delete(listener)
  }
}

const idle = () => () => {}

/** The fleet's summaries by project id; `enabled` false asks for nothing. */
export function useProjects(enabled = true): ReadonlyMap<number, DeploymentSummary> {
  return useSyncExternalStore(
    enabled ? subscribe : idle,
    () => projects,
    () => EMPTY,
  )
}
