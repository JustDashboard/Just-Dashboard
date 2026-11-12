"use client"

import { useAuth } from "@/hooks/use-auth"
import { EmptyState } from "@/components/state"
import { EngineMark } from "@/components/database/kit/engine-mark"
import { DangerSection } from "@/components/database/ops/settings-danger"
import { SettingsFrame } from "@/components/database/ops/settings-frame"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The acts that end it. They are an administrator's who may destroy; any
 * other role reaching the address is told whose they are, with nothing to
 * press.
 */
export function SettingsDanger() {
  const { engine } = useDatabase()
  const { can } = useAuth()
  const mayDestroy = can("system.admin") && can("destructive")
  return (
    <SettingsFrame page="danger">
      {(confirm) =>
        mayDestroy ? (
          <DangerSection confirm={confirm} />
        ) : (
          <EmptyState
            mark={<EngineMark engine={engine} />}
            title="The danger zone is an administrator's"
            description="Forgetting the connection, deleting the database and removing its container are taken by an administrator whose role may destroy."
          />
        )
      }
    </SettingsFrame>
  )
}
