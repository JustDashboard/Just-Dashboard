"use client"

import { SettingsFrame } from "@/components/database/ops/settings-frame"
import { PoolingSections } from "@/components/database/ops/settings-pooling"

export function SettingsPooling() {
  return (
    <SettingsFrame page="pooling">
      {(confirm) => <PoolingSections confirm={confirm} />}
    </SettingsFrame>
  )
}
