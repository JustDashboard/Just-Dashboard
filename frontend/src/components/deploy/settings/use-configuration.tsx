"use client"

import { useCallback, useEffect, useRef } from "react"
import { get, put } from "@/lib/api"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentConfiguration, DeploymentEnvironmentConfiguration } from "@/lib/types"
import { StatGrid } from "@/components/stat-tile"
import { ErrorState } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"

type ConfigurationChanges = Partial<
  Pick<DeploymentConfiguration, "build" | "runtime" | "dependencies" | "checks" | "domains">
>

/**
 * The environment's desired configuration, and the one way to change it.
 *
 * Every settings section reads the same document and writes it back with
 * the revision it read, so a save made from a stale tab is refused rather
 * than silently overwriting somebody else's. `save` takes only the parts a
 * form owns and fills the rest from the copy on screen.
 */
export function useConfiguration(projectId: number, environmentId: number) {
  const state = usePoll(
    (signal) =>
      get<DeploymentEnvironmentConfiguration>(
        `/deploy/${projectId}/environments/${environmentId}/configuration`,
        undefined,
        signal,
      ),
    0,
    [projectId, environmentId],
    { enabled: projectId > 0 && environmentId > 0 },
  )
  const current = state.data
  const refresh = state.refresh
  const save = useCallback(
    async (changes: ConfigurationChanges) => {
      if (!current) throw new Error("The configuration has not loaded yet")
      await put(`/deploy/${projectId}/environments/${environmentId}/configuration`, {
        revision: current.revision,
        build: changes.build ?? current.build,
        runtime: changes.runtime ?? current.runtime,
        dependencies: changes.dependencies ?? current.dependencies,
        checks: changes.checks ?? current.checks,
        domains: changes.domains ?? current.domains,
      })
      refresh()
    },
    [current, projectId, environmentId, refresh],
  )
  return {
    configuration: current,
    loading: state.loading && !current,
    error: state.error,
    refresh,
    save,
  }
}

/**
 * The three states a settings section passes through before it has a form
 * to show. Rendered by the section, so the rail stays put while it loads.
 *
 * `readings` says the page opens on a row of figures, so the skeleton draws
 * one: a placeholder that is not the shape of what replaces it is a jump.
 */
export function ConfigurationState({
  state,
  readings,
  children,
}: {
  state: ReturnType<typeof useConfiguration>
  readings?: boolean
  children: (configuration: DeploymentEnvironmentConfiguration) => React.ReactNode
}) {
  if (state.loading) return <SettingsSkeleton readings={readings} />
  if (state.error && !state.configuration)
    return <ErrorState error={state.error} onRetry={state.refresh} />
  if (!state.configuration) return null
  return <>{children(state.configuration)}</>
}

/**
 * A settings page before its configuration lands: the figures, then two
 * sections with their heads in the rail and three fields beside each.
 *
 * It was a framed table — a header strip over five rows — which is the one
 * shape no settings page has, so the moment the data arrived the whole
 * screen was replaced rather than filled in. This is the silhouette of the
 * page it stands in for, unframed like the page, and the content that
 * replaces it rises once (§11) instead of jumping.
 */
function SettingsSkeleton({ readings }: { readings?: boolean }) {
  return (
    <div className="min-w-0 space-y-8">
      {readings && (
        <StatGrid columns={4} dense>
          {[0, 1, 2, 3].map((cell) => (
            // Stamped as a tile so the grid gives it a tile's padding and
            // rules, and the figures land exactly where the bars were.
            <div
              key={cell}
              data-slot="stat-tile"
              className="flex min-w-0 flex-col gap-2.5 px-5 py-4"
            >
              <Skeleton className="h-2.5 w-16" />
              <Skeleton className="h-6 w-24" />
            </div>
          ))}
        </StatGrid>
      )}
      <div className="divide-y divide-hairline">
        {[0, 1].map((row) => (
          <div
            key={row}
            className="grid min-w-0 gap-x-12 gap-y-4 py-8 first:pt-0 last:pb-0 xl:grid-cols-[15rem_minmax(0,1fr)]"
          >
            <div className="space-y-2">
              <Skeleton className="h-4 w-28" />
              <Skeleton className="h-2.5 w-44" />
              <Skeleton className="h-2.5 w-32" />
            </div>
            <div className="max-w-3xl min-w-0 space-y-3">
              <Skeleton className="h-9 w-full" />
              <Skeleton className="h-9 w-full" />
              <Skeleton className="h-9 w-2/3" />
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

/**
 * Whether a field is one the server leaves out. The plan is decoded into Go
 * structs with no pointer fields, so a missing field *is* its zero value, and
 * the `omitempty` ones are written back missing: a switch turned on and off
 * again, or a path typed and cleared, is what was saved, not an edit. Read as
 * one, it put "Unsaved changes" and a brand Save on a form nobody could make
 * clean — saving it changed nothing the server returned, so not even a save
 * cleared it.
 */
function omitted(value: unknown) {
  return (
    value === undefined ||
    value === null ||
    value === false ||
    value === 0 ||
    value === "" ||
    (Array.isArray(value) && value.length === 0)
  )
}

/** A field as the server would write it back: absent when it is empty. */
function asSaved(value: unknown) {
  return omitted(value) ? undefined : value
}

/**
 * A value as one string whatever order its keys were written in, so two
 * reads of the same saved plan compare equal. A missing key, one set to
 * `undefined` — as it is once the draft has been through session storage —
 * and one holding an empty value are the same thing here (`omitted`). Only an
 * object's fields are dropped: a list keeps every position it has.
 */
function canonical(value: unknown): string {
  return (
    JSON.stringify(value, (_, inner: unknown) =>
      isRecord(inner)
        ? Object.fromEntries(
            Object.entries(inner)
              .filter(([, field]) => !omitted(field))
              .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)),
          )
        : inner,
    ) ?? ""
  )
}

/**
 * A short fingerprint of a saved value: djb2 over its canonical form, as
 * eight hex digits so no digest is a prefix of another.
 */
export function settingDigest(value: unknown): string {
  const text = canonical(value)
  let hash = 5381
  for (let i = 0; i < text.length; i++) hash = ((hash << 5) + hash + text.charCodeAt(i)) >>> 0
  return hash.toString(16).padStart(8, "0")
}

/**
 * How many edits a draft holds against what is saved: one per field of an
 * object that differs, one per position of a list — an edited health check
 * and an added one are two — and one for anything else that differs.
 */
export function draftChanges(value: unknown, saved: unknown): number {
  if (Array.isArray(value) && Array.isArray(saved)) {
    const length = Math.max(value.length, saved.length)
    let count = 0
    for (let i = 0; i < length; i++) if (canonical(value[i]) !== canonical(saved[i])) count++
    return count
  }
  if (isRecord(value) && isRecord(saved)) {
    const keys = new Set([...Object.keys(value), ...Object.keys(saved)])
    return [...keys].filter(
      (key) => canonical(asSaved(value[key])) !== canonical(asSaved(saved[key])),
    ).length
  }
  return canonical(value) === canonical(saved) ? 0 : 1
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value)
}

/**
 * A settings section's unsaved edits, kept for the tab and tied to the
 * section's own saved value.
 *
 * The drafts used to be keyed on the configuration's revision, which every
 * save on the page bumps — so saving Health checks restarted the Runtime
 * form beside it from the server's copy, and an operator's unsaved port and
 * memory limit were gone without a word. Keyed on a digest of *this*
 * section's saved value instead, a draft restarts only when that value
 * moves: its own save landing, or somebody else changing the same thing.
 * A sibling's save leaves it alone, which is the independence each form
 * already claimed to have.
 *
 * `key` names the section (`deploy.7.settings.build`). `changed` answers for
 * a group of fields, so a rail head can say which part of a long form has
 * the edits; `changes` is the count the foot prints; `discard` drops the
 * draft and the form reads the saved value again.
 */
export function useSettingDraft<T>(key: string, saved: T) {
  const digest = settingDigest(saved)
  const stored = `${key}@${digest}`
  const [value, set] = useSessionState<T>(stored, saved)

  // Once the saved value has moved on, the draft typed against the old one is
  // finished with. Dropping it keeps it from resurfacing should the saved
  // value ever come back round to what it was — a saved draft reappearing as
  // unsaved edits the next time the setting is changed back.
  const previous = useRef(stored)
  useEffect(() => {
    if (previous.current !== stored) forgetSessionState(previous.current)
    previous.current = stored
  }, [stored])

  const changes = draftChanges(value, saved)
  const changed = (fields: (keyof T)[]) =>
    fields.some(
      (field) =>
        canonical(asSaved((value as Record<keyof T, unknown>)[field])) !==
        canonical(asSaved((saved as Record<keyof T, unknown>)[field])),
    )
  const discard = () => forgetSessionState(stored)
  return { value, set, dirty: changes > 0, changes, changed, discard }
}
