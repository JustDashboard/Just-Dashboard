"use client"

import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import type { BlueprintDetail, SmartStackCatalogueEntry, SmartStackState } from "@/lib/types"

/**
 * The reads a stack project makes, shared by its page, the project's rail and
 * a run's page.
 *
 * Whether a project is a stack is the server's to say: `GET /deploy/{id}/stack`
 * answers for one and is a 404 (`stack_not_found`) for anything else. It is
 * only asked for a project made from a template that names a stack, so an
 * ordinary project's pages do not each make a request that is refused.
 */

/**
 * The stack a template is a preset of, read from its definition. A project's
 * source names its template as `id@version`; the project's own pages already
 * hold the definition and do not need this.
 */
export function useTemplateStack(sourceTemplate: string | undefined): string | undefined {
  const id = (sourceTemplate ?? "").split("@")[0]
  const definition = usePoll(
    (signal) => get<BlueprintDetail>(`/deploy/blueprints/${id}`, undefined, signal),
    0,
    [id],
    { enabled: Boolean(id) },
  )
  return definition.data?.id === id ? definition.data.stack : undefined
}

/**
 * A stack project's state. Every reader of one project shares one request at
 * the fastest cadence any of them asks for, so the rail's entry and the page
 * cannot disagree about whether there is a stack.
 */
export function useStackState(projectId: number, enabled: boolean, intervalMs = 0) {
  return usePoll(
    (signal) => get<SmartStackState>(`/deploy/${projectId}/stack`, undefined, signal),
    intervalMs,
    [projectId],
    { enabled, key: `/deploy/${projectId}/stack` },
  )
}

/**
 * A stack service's title by its Compose name ("seerr" is "Seerr"), for a
 * domain row that says which app it routes to. The catalogue is read only
 * where some name routes to a service of its own; without it, or for a name
 * it does not list, the service's own name is said.
 */
export function useServiceTitles(enabled: boolean): (service: string) => string {
  const catalogue = usePoll(
    (signal) => get<SmartStackCatalogueEntry[]>("/deploy/stacks", undefined, signal),
    0,
    [],
    { enabled, key: "/deploy/stacks" },
  )
  const titles = new Map(
    (catalogue.data ?? []).flatMap((entry) =>
      entry.services.map((service) => [service.name, service.title] as const),
    ),
  )
  return (service) => titles.get(service) ?? service
}
