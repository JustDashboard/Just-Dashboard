"use client"

import { Logs } from "@/components/database/ops"
import { SectionPage } from "@/components/database/shell/section-page"

export default function LogsPage() {
  return <SectionPage section="logs" any={Logs} />
}
