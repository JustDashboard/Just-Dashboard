"use client"

import dynamic from "next/dynamic"
import { useParams } from "next/navigation"
import { Page, WORKBENCH_WAITING } from "@/components/page"
import { LoadingPanel } from "@/components/loading"

// The workspace draws its own page: the editor is a workbench held to the
// window, and a drawing that is not there is an ordinary reading page.

const BoardWorkspace = dynamic(() => import("@/components/boards/board-workspace"), {
  ssr: false,
  loading: () => (
    <Page fill="workspace">
      <LoadingPanel plain className={WORKBENCH_WAITING} />
    </Page>
  ),
})

export default function BoardPage() {
  const { id } = useParams<{ id: string }>()
  return <BoardWorkspace id={Number(id)} />
}
