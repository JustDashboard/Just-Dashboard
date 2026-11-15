"use client"

import type { DeploymentEnvironmentConfiguration } from "@/lib/types"
import { SaveBarProvider } from "@/components/settings/save-bar"
import { SettingsColumn } from "@/components/settings/settings-column"
import { PendingChanges } from "@/components/deploy/settings/pending-changes"
import { LastFailureRemedy } from "@/components/deploy/settings/last-failure"
import {
  ConfigurationState,
  type useConfiguration,
} from "@/components/deploy/settings/use-configuration"

/**
 * A deployment's settings page: its configuration read, what is saved but
 * not live, and its forms in one run of sections — all of it in the
 * `SettingsColumn` every settings page in the product is drawn in, so the
 * strip lines up with the fields under it and the save bar has its room at
 * the foot. It used to type the column's classes out again, which is how two
 * columns that must be one width can come apart.
 *
 * No heading of its own. The project's other pages add none under the
 * project header, and the first section's head already names what the page is.
 * The content rises once when the first read lands and not on every revision
 * after it, because a save is not an arrival.
 *
 * The pieces inside it are in `setting-card.tsx`: `SettingForm`, one form and
 * one write, and `SettingSection`, one head and its fields.
 */
export function SettingsPage({
  state,
  pageKinds,
  children,
}: {
  state: ReturnType<typeof useConfiguration>
  /** The pending-change kinds this page edits — see `PendingChanges`. */
  pageKinds?: string[]
  children: (configuration: DeploymentEnvironmentConfiguration) => React.ReactNode
}) {
  return (
    <SaveBarProvider>
      <ConfigurationState state={state}>
        {(configuration) => (
          <SettingsColumn
            className="animate-rise"
            strip={
              <>
                <PendingChanges pending={configuration.pending} pageKinds={pageKinds} />
                <LastFailureRemedy />
              </>
            }
          >
            {children(configuration)}
          </SettingsColumn>
        )}
      </ConfigurationState>
    </SaveBarProvider>
  )
}
