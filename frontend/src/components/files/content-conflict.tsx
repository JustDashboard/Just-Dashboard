import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"

export function ContentConflict({
  onReview,
  onReload,
  busy,
}: {
  onReview: () => void
  onReload: () => void
  busy?: boolean
}) {
  return (
    <div role="alert" className="shrink-0 p-3">
      <Notice tone="warning" title="The file changed on disk">
        Your draft is kept. Review it against the current file before saving, or discard it and
        reload the current file.
        <div className="mt-2 flex flex-wrap gap-2">
          <Button size="xs" variant="outline" onClick={onReview} disabled={busy}>
            Review current file
          </Button>
          <Button size="xs" variant="ghost" onClick={onReload} disabled={busy}>
            Discard draft and reload
          </Button>
        </div>
      </Notice>
    </div>
  )
}
