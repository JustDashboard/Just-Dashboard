"use client"

import { Cross, MagnifyingGlass } from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { ProductGlyph } from "@/components/product-logo"
import { Status, type DotTone } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { previewReading, type IPAMPreview } from "@/lib/network-ipam"
import { ownerProduct } from "@/components/network/ipam/reading"

/** Only a known overlap is a refusal; incomplete coverage is a caution, not a pass. */
const TONE: Record<IPAMPreview["status"], DotTone> = {
  known_overlap: "danger",
  unknown_coverage: "warning",
  no_known_overlap: "running",
}

/**
 * Ask whether a prefix overlaps anything the planner knows of, before making
 * a pool or a reservation of it. The answer stays under the field until it is
 * dismissed, with each thing it runs into on a line of its own.
 */
export function IPAMPreviewField({
  value,
  onChange,
  preview,
  busy,
  error,
  onPreview,
  onDismiss,
}: {
  value: string
  onChange: (value: string) => void
  preview?: IPAMPreview
  busy: boolean
  error?: string
  onPreview: () => void
  onDismiss: () => void
}) {
  const reading = preview ? previewReading(preview) : undefined
  return (
    <div className="space-y-3">
      <form
        className="flex min-w-0 flex-wrap items-center gap-2"
        onSubmit={(event) => {
          event.preventDefault()
          if (value.trim() && !busy) onPreview()
        }}
      >
        <div className="relative min-w-0 flex-1 sm:max-w-sm">
          <MagnifyingGlass
            aria-hidden
            className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
          />
          <Input
            aria-label="Prefix to preview"
            value={value}
            onChange={(event) => onChange(event.target.value)}
            className="pl-8 font-mono"
            disabled={busy}
            placeholder="10.244.0.0/24 or fd48:abcd::/64"
            spellCheck={false}
          />
        </div>
        <Button variant="outline" type="submit" pending={busy} disabled={busy || !value.trim()}>
          Preview overlap
        </Button>
      </form>
      {error && (
        <p role="alert" className="text-body text-destructive">
          {error}
        </p>
      )}
      {preview && reading && (
        <div
          data-testid="ipam-preview"
          className="space-y-2 rounded-lg border border-hairline px-4 py-3"
        >
          <div className="flex min-w-0 items-center gap-3">
            <Status tone={TONE[preview.status]} label={reading.label} />
            <span className="truncate font-mono text-body">{preview.prefix}</span>
            <IconAction label="Dismiss preview" className="ml-auto" onClick={onDismiss}>
              <Cross />
            </IconAction>
          </div>
          <p className="text-hint text-muted-foreground">{reading.detail}</p>
          {preview.conflicts.length > 0 && (
            <ul aria-label="Overlaps" className="divide-y divide-hairline border-t border-hairline">
              {preview.conflicts.map((conflict, i) => {
                const product = ownerProduct(conflict.owner)
                return (
                  <li
                    key={i}
                    className="flex h-9 min-w-0 items-center gap-2 font-mono text-hint"
                    title={`${conflict.prefix} · ${conflict.owner} · ${conflict.resource} · ${conflict.basis}`}
                  >
                    {product && <ProductGlyph id={product} />}
                    <span className="shrink-0 text-foreground">{conflict.prefix}</span>
                    <span className="truncate text-muted-foreground">
                      {conflict.owner} · {conflict.resource} · {conflict.basis}
                    </span>
                  </li>
                )
              })}
            </ul>
          )}
          <p className="text-hint text-muted-foreground">
            Checked <time dateTime={preview.checkedAt}>{clock(preview.checkedAt)}</time>; native
            creation rechecks current evidence.
          </p>
        </div>
      )}
    </div>
  )
}

const clock = (iso: string) =>
  new Date(iso).toLocaleString("en-GB", { dateStyle: "medium", timeStyle: "short" })
