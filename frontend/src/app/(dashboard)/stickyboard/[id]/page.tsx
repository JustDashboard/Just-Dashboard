"use client"

import dynamic from "next/dynamic"
import { useParams } from "next/navigation"
import { Page } from "@/components/page"
import { LoadingPanel } from "@/components/state"

const StickyWorkspace = dynamic(() => import("@/components/stickyboard/workspace"), {
  ssr: false,
  loading: () => <LoadingPanel />,
})

export default function StickyboardBoardPage() {
  const { id } = useParams<{ id: string }>()
  return (
    <Page fill className="max-w-none gap-0 p-0 md:p-0">
      <StickyWorkspace id={Number(id)} />
    </Page>
  )
}
