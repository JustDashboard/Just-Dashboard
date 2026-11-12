"use client"

import { useAuth } from "@/hooks/use-auth"
import { EmptyState } from "@/components/state"
import { EngineMark } from "@/components/database/kit/engine-mark"
import { SettingsFrame } from "@/components/database/ops/settings-frame"
import { VersionSections } from "@/components/database/ops/settings-version"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The release it runs and moving it to a newer one. An upgrade replaces the
 * server, so the page is an administrator's; any other role reaching the
 * address is told whose it is.
 */
export function SettingsVersion() {
  const { engine } = useDatabase()
  const { can } = useAuth()
  return (
    <SettingsFrame page="version">
      {(confirm) =>
        can("system.admin") ? (
          <VersionSections confirm={confirm} />
        ) : (
          <EmptyState
            mark={<EngineMark engine={engine} />}
            title="Upgrades are an administrator's"
            description="Which release the server runs, and moving it to a newer one, are read and done by an administrator."
          />
        )
      }
    </SettingsFrame>
  )
}
