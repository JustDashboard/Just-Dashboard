"use client"

import { useState } from "react"
import { Download } from "@/components/icons"
import { downloadUrl } from "@/lib/api"
import { notify } from "@/lib/toast"
import { bytes } from "@/lib/format"
import type { LogSource } from "@/lib/types"
import { filterQuery, isFilterActive, resolveRange, TIME_RANGES } from "@/lib/log-filter"
import type { LogFilterState, LogTimeRange } from "@/components/logs/types"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Input } from "@/components/ui/input"

/**
 * The export takes the filter with it.
 *
 * It used to ignore it entirely: narrow the view to one request id, press
 * Export, and get the whole file — so the download and the screen described two
 * different logs and only one of them was the one being read. The dialog says
 * in a sentence what is about to be written, because an export nobody can
 * predict is one nobody uses twice.
 */
export function ExportDialog({
  sourceId,
  source,
  filter,
  boot,
}: {
  sourceId: string
  source: LogSource | null
  filter: LogFilterState
  boot: boolean
}) {
  const [open, setOpen] = useState(false)
  const [range, setRange] = useState<LogTimeRange>("all")
  const [since, setSince] = useState("")
  const [until, setUntil] = useState("")
  const [withFilter, setWithFilter] = useState(true)
  const [archives, setArchives] = useState(false)

  const window = resolveRange(range, since, until)
  const hasArchives = (source?.archives ?? 0) > 0
  const href = downloadUrl("/logs/download", {
    source: sourceId,
    ...(withFilter ? filterQuery(filter) : {}),
    ...window,
    archives: archives && hasArchives ? "true" : undefined,
    boot: boot ? "true" : undefined,
  })

  const rangeLabel = TIME_RANGES.find((r) => r.id === range)?.label.toLowerCase() ?? "everything"
  const summary = [
    withFilter && isFilterActive(filter) ? "the lines this filter keeps" : "every line",
    range === "all" ? "on disk" : `from ${rangeLabel}`,
    archives && hasArchives ? `including ${source?.archives} rotated archives` : null,
  ]
    .filter(Boolean)
    .join(", ")

  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        <Download className="size-4" />
        Export
      </Button>
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={<>Export {source?.label ?? sourceId}</>}
        description="A plain text file, oldest line first. Lines with no parseable timestamp are kept —
            they continue the record above them."
        footer={
          <>
            <Button asChild onClick={() => notify.success("Export started")}>
              <a href={href} download>
                <Download className="size-4" />
                Download
              </a>
            </Button>
          </>
        }
      >
        <div className="grid gap-3">
          <div className="space-y-1.5">
            <Label>Window</Label>
            <Select value={range} onValueChange={(v) => setRange(v as LogTimeRange)}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {TIME_RANGES.map((r) => (
                  <SelectItem key={r.id} value={r.id}>
                    {r.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {range === "custom" && (
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="export-since">From</Label>
                <Input
                  id="export-since"
                  type="datetime-local"
                  value={since}
                  onChange={(e) => setSince(e.target.value)}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="export-until">To</Label>
                <Input
                  id="export-until"
                  type="datetime-local"
                  value={until}
                  onChange={(e) => setUntil(e.target.value)}
                />
              </div>
            </div>
          )}

          {isFilterActive(filter) && (
            <label className="flex items-center gap-2.5 text-sm">
              <Switch checked={withFilter} onCheckedChange={setWithFilter} />
              Apply the filter that is on screen
            </label>
          )}

          {hasArchives && (
            <label className="flex items-center gap-2.5 text-sm">
              <Switch checked={archives} onCheckedChange={setArchives} />
              Include {source?.archives} rotated {source?.archives === 1 ? "archive" : "archives"} (
              {bytes(source?.archiveBytes)})
            </label>
          )}

          <p className="rounded-md bg-surface-sunken px-3 py-2 text-xs leading-relaxed text-muted-foreground">
            Downloads {summary}.
          </p>
        </div>
      </Modal>
    </>
  )
}
