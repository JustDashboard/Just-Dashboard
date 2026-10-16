"use client"

import { SettingsDatabases } from "@/components/database/ops"
import { SectionPage } from "@/components/database/shell/section-page"

export default function SettingsDatabasesPage() {
  return <SectionPage section="settings" any={SettingsDatabases} />
}
