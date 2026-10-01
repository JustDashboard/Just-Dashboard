"use client"

import { SqlSearch } from "@/components/database/search"
import { SectionPage } from "@/components/database/shell/section-page"

export default function SearchPage() {
  return <SectionPage section="search" sql={SqlSearch} />
}
