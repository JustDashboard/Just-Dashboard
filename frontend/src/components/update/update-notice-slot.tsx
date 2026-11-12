"use client"

import dynamic from "next/dynamic"
import { updateHasNews, useSelfUpdate } from "@/hooks/use-self-update"

/**
 * The notice's place in the rail, without the notice.
 *
 * The notice says nothing on almost every page view, and when it does speak it
 * draws with the deployment pipeline's segments, the run transcript, motion and
 * confetti. Imported outright by the sidebar, all of that (the 1,680-line
 * deployment vocabulary among it) rode in the shell's chunks on every route
 * and after every release, to draw nothing. So the rail holds this instead:
 * it reads the one poll the shell already runs, and fetches the notice only
 * once there is something for it to say. It arrives with `animate-rise`, so a
 * moment's delay reads as the notice arriving.
 */
const UpdateNotice = dynamic(
  () => import("@/components/update/update-notice").then((notice) => notice.UpdateNotice),
  { ssr: false },
)

export function UpdateNoticeSlot({ collapsed }: { collapsed: boolean }) {
  const { report, outcome } = useSelfUpdate()
  if (!updateHasNews(report, outcome)) return null
  return <UpdateNotice collapsed={collapsed} />
}
