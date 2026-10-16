"use client"

import { SettingsNetwork } from "@/components/database/ops"
import { SectionPage } from "@/components/database/shell/section-page"

export default function SettingsNetworkPage() {
  return <SectionPage section="settings" any={SettingsNetwork} />
}
