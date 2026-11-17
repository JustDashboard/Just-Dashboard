import { ChoiceListSkeleton } from "@/components/choice-row"
import { LoadingRows } from "@/components/loading"
import { LOG_PANE_SIZE } from "@/components/logs/service-logs"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"

/**
 * A log at the foot of a page, as a section: a title and a hairline over the
 * pane, the way every other block on the page is drawn — the pane is the one
 * frame, because it owns its scroll (§7).
 *
 * Until the page knows which logs it has, the section holds the shape it
 * will have: the workbench well from `md` up and the one card a phone draws
 * in its place (`EmbeddedLog`), so the page does not move when it lands.
 */
export function LogSection({
  title,
  ready = true,
  ref,
  children,
}: {
  title: string
  /** False while the page is still asking which logs there are. */
  ready?: boolean
  /** The section, for a page that brings it into view. */
  ref?: React.Ref<HTMLElement>
  children?: React.ReactNode
}) {
  return (
    <Panel plain ref={ref} aria-label={title}>
      <PanelHeader title={title} />
      <PanelBody flush className="pt-3">
        {ready ? (
          children
        ) : (
          <>
            <LoadingRows rows={6} hold className={`max-md:hidden ${LOG_PANE_SIZE.section}`} />
            <ChoiceListSkeleton rows={1} lines={2} className="md:hidden" />
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
