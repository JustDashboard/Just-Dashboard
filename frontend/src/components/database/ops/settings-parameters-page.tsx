"use client"

import { SettingsFrame } from "@/components/database/ops/settings-frame"
import { ParametersSection } from "@/components/database/ops/settings-parameters"

export function SettingsParameters() {
  return <SettingsFrame page="parameters">{() => <ParametersSection />}</SettingsFrame>
}
