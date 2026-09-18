"use client"

import { useCallback } from "react"
import { get, put } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentConfiguration, DeploymentEnvironmentConfiguration } from "@/lib/types"
import { ErrorState, LoadingPanel } from "@/components/state"

export type ConfigurationChanges = Partial<
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
 */
export function ConfigurationState({
  state,
  children,
}: {
  state: ReturnType<typeof useConfiguration>
  children: (configuration: DeploymentEnvironmentConfiguration) => React.ReactNode
}) {
  if (state.loading) return <LoadingPanel rows={5} />
  if (state.error && !state.configuration)
    return <ErrorState error={state.error} onRetry={state.refresh} />
  if (!state.configuration) return null
  return <>{children(state.configuration)}</>
}
