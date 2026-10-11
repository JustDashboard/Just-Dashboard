"use client"

import dynamic from "next/dynamic"
import { useParams } from "next/navigation"
import { Page } from "@/components/page"
import { LoadingPanel } from "@/components/state"

const BoardWorkspace = dynamic(() => import("@/components/boards/board-workspace"), {
  ssr: false,
  loading: () => <LoadingPanel />,
})

export default function BoardPage() {
  const { id } = useParams<{ id: string }>()
  return (
    <Page fill className="max-w-none gap-0 p-0 md:p-0">
      <BoardWorkspace id={Number(id)} />
    </Page>
  )
}
