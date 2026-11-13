"use client"

import { Access } from "@/components/database/ops/access"
import { SectionPage } from "@/components/database/shell/section-page"

export default function AccessPage() {
  return <SectionPage section="access" any={Access} />
}
