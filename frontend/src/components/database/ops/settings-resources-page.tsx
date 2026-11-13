"use client"

import { SettingsFrame } from "@/components/database/ops/settings-frame"
import { ResourcesSection } from "@/components/database/ops/settings-resources"

export function SettingsResources() {
  return (
    <SettingsFrame page="resources">
      {(confirm) => <ResourcesSection confirm={confirm} />}
    </SettingsFrame>
  )
}
