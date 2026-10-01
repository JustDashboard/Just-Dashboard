"use client"

import { Access } from "@/components/database/ops"
import { SectionPage } from "@/components/database/shell/section-page"

export default function AccessPage() {
  return <SectionPage section="access" any={Access} />
}
