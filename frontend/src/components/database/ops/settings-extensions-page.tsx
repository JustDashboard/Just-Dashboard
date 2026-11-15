"use client"

import { ExtensionsSection } from "@/components/database/ops/settings-extensions"
import { SettingsFrame } from "@/components/database/ops/settings-frame"

export function SettingsExtensions() {
  return <SettingsFrame page="extensions">{() => <ExtensionsSection />}</SettingsFrame>
}
