"use client"

import { OnlineImport } from "@/components/database/import"
import { SectionPage } from "@/components/database/shell/section-page"

export default function ImportPage() {
  return <SectionPage section="import" any={OnlineImport} />
}
