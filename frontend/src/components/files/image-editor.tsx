"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  ArrowLeftRight,
  ArrowUpDown,
  CornerUpLeft,
  Crop,
  FloppyDisk,
  RotateClockwise,
  RotateCounterClockwise,
  SettingsSliders,
  Sparkles,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { API_BASE, mutationHeaders } from "@/lib/api"
import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import { SidePanel } from "@/components/side-panel"
import { Spinner } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Slider } from "@/components/ui/slider"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { rawUrl } from "@/components/files/preview-panel"
import { PaneFooter, PaneHeader } from "@/components/panel"

/**
 * Crop, rotate, resize and re-encode a picture, in the browser.
 *
 * The argument for this being here at all is the same one behind the rest of
 * the product: the alternative is scp to a laptop, open something, scp back —
 * for a favicon that is 40 pixels too wide, or a screenshot with somebody's
 * address in the corner, on a server whose whole purpose is to serve that
 * file. Nothing here needs ImageMagick installed on the host: the picture is
 * already in the browser to be looked at, and a canvas can do all of it.
 *
 * Every operation is *committed* to a new canvas rather than composed into a
 * live pipeline of parameters. That is what makes undo a stack of bitmaps
 * instead of a stack of transformations to replay in the right order, and it
 * is the difference between "rotate, crop, rotate again" behaving the way it
 * looks and being a source of arithmetic nobody can hold in their head.
 *
 * Save writes through the ordinary upload route, so it lands with the file's
 * existing owner and mode rather than inventing new ones.
 */
export function ImageEditorSheet({
  path,
  modified,
  onOpenChange,
  onSaved,
}: {
  path: string | null
  /** The file's modification time, so the source is not read from a cache. */
  modified?: string
  onOpenChange: (open: boolean) => void
  onSaved: (savedPath: string) => void
}) {
  return (
    <SidePanel
      open={path !== null}
      onOpenChange={onOpenChange}
      width="xl"
      title={path?.split("/").pop() ?? "Image"}
      description={path ?? undefined}
      bodyClassName="flex min-h-0 flex-1 flex-col p-0"
    >
      {path && (
        <ImageEditor
          // Keyed on the path so opening another picture starts from that
          // picture rather than from the previous one's edit history.
          key={path}
          path={path}
          modified={modified}
          onClose={() => onOpenChange(false)}
          onSaved={onSaved}
        />
      )}
    </SidePanel>
  )
}

type Rect = { x: number; y: number; w: number; h: number }

function ImageEditor({
  path,
  modified,
  onClose,
  onSaved,
}: {
  path: string
  modified?: string
  onClose: () => void
  onSaved: (savedPath: string) => void
}) {
  const [history, setHistory] = useState<HTMLCanvasElement[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string>()
  const [saving, setSaving] = useState(false)
  const [cropping, setCropping] = useState(false)
  const [crop, setCrop] = useState<Rect | null>(null)
  const [format, setFormat] = useState(() => defaultFormat(path))
  const [quality, setQuality] = useState(90)
  const [adjust, setAdjust] = useState({ brightness: 100, contrast: 100, saturate: 100 })
  // The size fields default to whatever the canvas currently is, and hold a
  // draft only once somebody types in them. Copying the canvas dimensions into
  // state after every edit would be a render's worth of stale numbers each
  // time, and one more thing to keep in step.
  const [resizeDraft, setResizeDraft] = useState<{ w: string; h: string } | null>(null)
  const [lockRatio, setLockRatio] = useState(true)
  const [saveAs, setSaveAs] = useState("")

  const viewRef = useRef<HTMLCanvasElement>(null)
  const surfaceRef = useRef<HTMLDivElement>(null)
  const current = history[history.length - 1]

  useEffect(() => {
    const image = new Image()
    image.onload = () => {
      const canvas = document.createElement("canvas")
      // An SVG with no intrinsic size decodes to 0×0 and would silently save
      // an empty file; 1024 is a sane raster for one, and the notice below
      // says that saving rasterises it.
      canvas.width = image.naturalWidth || 1024
      canvas.height = image.naturalHeight || 1024
      canvas.getContext("2d")?.drawImage(image, 0, 0, canvas.width, canvas.height)
      setHistory([canvas])
      setLoading(false)
    }
    image.onerror = () => {
      setError("This image could not be decoded by the browser.")
      setLoading(false)
    }
    image.src = rawUrl(path, modified)
    return () => {
      image.onload = null
      image.onerror = null
    }
  }, [path, modified])

  // The visible canvas is repainted from the committed one plus the live
  // adjustment sliders, which are deliberately *not* committed until applied:
  // dragging brightness would otherwise push twenty bitmaps onto the undo
  // stack, one per pointer event.
  useEffect(() => {
    const view = viewRef.current
    if (!view || !current) return
    view.width = current.width
    view.height = current.height
    const ctx = view.getContext("2d")
    if (!ctx) return
    ctx.filter = filterFor(adjust)
    ctx.drawImage(current, 0, 0)
    ctx.filter = "none"
  }, [current, adjust])

  const push = useCallback((canvas: HTMLCanvasElement) => {
    // Ten steps of undo, which is more than anybody needs for a crop and a
    // rotate and far less memory than an unbounded stack of full bitmaps.
    setHistory((prev) => [...prev, canvas].slice(-10))
    setCrop(null)
    setCropping(false)
    setResizeDraft(null)
  }, [])

  const rotate = (degrees: 90 | -90) => {
    if (!current) return
    const out = document.createElement("canvas")
    out.width = current.height
    out.height = current.width
    const ctx = out.getContext("2d")
    if (!ctx) return
    ctx.translate(out.width / 2, out.height / 2)
    ctx.rotate((degrees * Math.PI) / 180)
    ctx.drawImage(current, -current.width / 2, -current.height / 2)
    push(out)
  }

  const flip = (axis: "h" | "v") => {
    if (!current) return
    const out = document.createElement("canvas")
    out.width = current.width
    out.height = current.height
    const ctx = out.getContext("2d")
    if (!ctx) return
    ctx.translate(axis === "h" ? out.width : 0, axis === "v" ? out.height : 0)
    ctx.scale(axis === "h" ? -1 : 1, axis === "v" ? -1 : 1)
    ctx.drawImage(current, 0, 0)
    push(out)
  }

  const applyCrop = () => {
    if (!current || !crop || crop.w < 2 || crop.h < 2) return
    const out = document.createElement("canvas")
    out.width = Math.round(crop.w)
    out.height = Math.round(crop.h)
    out
      .getContext("2d")
      ?.drawImage(
        current,
        Math.round(crop.x),
        Math.round(crop.y),
        out.width,
        out.height,
        0,
        0,
        out.width,
        out.height,
      )
    push(out)
  }

  const applyResize = () => {
    if (!current) return
    const w = Math.max(1, Math.round(Number(resizeDraft?.w) || current.width))
    const h = Math.max(1, Math.round(Number(resizeDraft?.h) || current.height))
    if (w === current.width && h === current.height) return
    const out = document.createElement("canvas")
    out.width = w
    out.height = h
    const ctx = out.getContext("2d")
    if (!ctx) return
    ctx.imageSmoothingQuality = "high"
    ctx.drawImage(current, 0, 0, w, h)
    push(out)
  }

  const applyAdjust = () => {
    if (!current || filterFor(adjust) === "none") return
    const out = document.createElement("canvas")
    out.width = current.width
    out.height = current.height
    const ctx = out.getContext("2d")
    if (!ctx) return
    ctx.filter = filterFor(adjust)
    ctx.drawImage(current, 0, 0)
    push(out)
    setAdjust({ brightness: 100, contrast: 100, saturate: 100 })
  }

  const save = async (asName?: string) => {
    if (!current) return
    const name = (asName || path.split("/").pop() || "image").trim()
    const finalName = withExtension(name, format)
    const dir = path.slice(0, path.lastIndexOf("/")) || "/"
    setSaving(true)
    try {
      const blob = await toBlob(current, format, quality / 100)
      const form = new FormData()
      form.append("file", blob, finalName)
      const res = await fetch(
        `${API_BASE}/files/upload?path=${encodeURIComponent(dir)}&overwrite=true`,
        { method: "POST", credentials: "include", headers: mutationHeaders(), body: form },
      )
      if (!res.ok) throw new Error((await res.json()).error?.message ?? res.statusText)
      notify.success(`Saved ${finalName}`, {
        description: `${bytes(blob.size)} · ${current.width}×${current.height}`,
      })
      onSaved(`${dir}/${finalName}`.replace(/\/{2,}/g, "/"))
      onClose()
    } catch (err) {
      notify.error("Could not save the image", err)
    } finally {
      setSaving(false)
    }
  }

  const onSurfacePointerDown = (event: React.PointerEvent) => {
    const view = viewRef.current
    if (!cropping || !view) return
    const rect = view.getBoundingClientRect()
    const scaleX = view.width / rect.width
    const scaleY = view.height / rect.height
    const startX = (event.clientX - rect.left) * scaleX
    const startY = (event.clientY - rect.top) * scaleY
    event.currentTarget.setPointerCapture(event.pointerId)

    const move = (e: PointerEvent) => {
      const x = clamp((e.clientX - rect.left) * scaleX, 0, view.width)
      const y = clamp((e.clientY - rect.top) * scaleY, 0, view.height)
      setCrop({
        x: Math.min(startX, x),
        y: Math.min(startY, y),
        w: Math.abs(x - startX),
        h: Math.abs(y - startY),
      })
    }
    const up = () => {
      window.removeEventListener("pointermove", move)
      window.removeEventListener("pointerup", up)
    }
    window.addEventListener("pointermove", move)
    window.addEventListener("pointerup", up)
  }

  // The crop rectangle is held in image pixels and drawn as a percentage of
  // the picture, so it stays put when the panel is resized mid-gesture. The
  // percentages come from the committed canvas rather than from the DOM node,
  // which is the same number and one React can actually see.
  const cropStyle = useMemo(() => {
    if (!crop || !current) return undefined
    return {
      left: `${(crop.x / current.width) * 100}%`,
      top: `${(crop.y / current.height) * 100}%`,
      width: `${(crop.w / current.width) * 100}%`,
      height: `${(crop.h / current.height) * 100}%`,
    }
  }, [crop, current])

  if (loading) {
    return (
      <div className="flex flex-1 items-center justify-center">
        <Spinner className="size-5 text-muted-foreground" />
      </div>
    )
  }
  if (error || !current) {
    return <p className="p-4 text-body text-destructive">{error}</p>
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PaneHeader className="flex-wrap gap-1.5 px-3">
        <Button size="xs" variant="outline" onClick={() => rotate(-90)}>
          <RotateCounterClockwise className="size-3" />
          Left
        </Button>
        <Button size="xs" variant="outline" onClick={() => rotate(90)}>
          <RotateClockwise className="size-3" />
          Right
        </Button>
        <Button size="xs" variant="outline" onClick={() => flip("h")}>
          <ArrowLeftRight className="size-3" />
          Flip
        </Button>
        <Button size="xs" variant="outline" onClick={() => flip("v")}>
          <ArrowUpDown className="size-3" />
          Flip
        </Button>
        <Button
          size="xs"
          variant={cropping ? "default" : "outline"}
          onClick={() => {
            setCropping((v) => !v)
            setCrop(null)
          }}
        >
          <Crop className="size-3" />
          {cropping ? "Drag a box" : "Crop"}
        </Button>
        {cropping && crop && crop.w > 2 && (
          <Button size="xs" onClick={applyCrop}>
            Apply {Math.round(crop.w)}×{Math.round(crop.h)}
          </Button>
        )}
        <span className="flex-1" />
        <Button
          size="xs"
          variant="ghost"
          disabled={history.length < 2}
          onClick={() => setHistory((prev) => prev.slice(0, -1))}
        >
          <CornerUpLeft className="size-3" />
          Undo
        </Button>
      </PaneHeader>

      <div
        ref={surfaceRef}
        className="relative flex min-h-0 flex-1 items-center justify-center overflow-auto checkerboard p-4"
      >
        <div className="relative max-h-full">
          <canvas
            ref={viewRef}
            onPointerDown={onSurfacePointerDown}
            className={cn(
              "max-h-[52vh] max-w-full object-contain shadow-sm",
              cropping && "cursor-crosshair",
            )}
          />
          {crop && cropStyle && (
            <div
              className="pointer-events-none absolute border-2 border-primary bg-plot-primary"
              style={cropStyle}
            />
          )}
        </div>
      </div>

      <div className="grid shrink-0 gap-3 border-t border-hairline p-3 sm:grid-cols-2">
        <div className="space-y-2">
          <Label className="text-xs text-muted-foreground">Size</Label>
          <div className="flex items-center gap-2">
            <Input
              value={resizeDraft?.w ?? String(current.width)}
              inputMode="numeric"
              className="h-8 w-24 font-mono text-xs"
              onChange={(e) => {
                const w = e.target.value
                setResizeDraft((prev) => ({
                  w,
                  h:
                    lockRatio && Number(w) > 0
                      ? String(Math.round((Number(w) / current.width) * current.height))
                      : (prev?.h ?? String(current.height)),
                }))
              }}
            />
            <span className="text-xs text-muted-foreground">×</span>
            <Input
              value={resizeDraft?.h ?? String(current.height)}
              inputMode="numeric"
              className="h-8 w-24 font-mono text-xs"
              onChange={(e) => {
                const h = e.target.value
                setResizeDraft((prev) => ({
                  h,
                  w:
                    lockRatio && Number(h) > 0
                      ? String(Math.round((Number(h) / current.height) * current.width))
                      : (prev?.w ?? String(current.width)),
                }))
              }}
            />
            <label className="flex items-center gap-1.5 text-hint text-muted-foreground">
              <Checkbox checked={lockRatio} onCheckedChange={(v) => setLockRatio(v === true)} />
              Lock ratio
            </label>
            <Button size="xs" variant="outline" onClick={applyResize}>
              Resize
            </Button>
          </div>
          <p className="text-hint text-muted-foreground">
            Now {current.width}×{current.height}
          </p>
        </div>

        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <Label className="text-xs text-muted-foreground">
              <SettingsSliders className="mr-1 inline size-3" />
              Adjust
            </Label>
            <Button
              size="xs"
              variant="outline"
              disabled={filterFor(adjust) === "none"}
              onClick={applyAdjust}
            >
              <Sparkles className="size-3" />
              Apply
            </Button>
          </div>
          {(["brightness", "contrast", "saturate"] as const).map((key) => (
            <div key={key} className="flex items-center gap-2">
              <span className="w-16 text-hint text-muted-foreground capitalize">{key}</span>
              <Slider
                value={[adjust[key]]}
                min={0}
                max={200}
                step={1}
                className="flex-1"
                onValueChange={([v]) => setAdjust((prev) => ({ ...prev, [key]: v }))}
              />
              <span className="numeric w-9 text-right text-hint text-muted-foreground">
                {adjust[key]}%
              </span>
            </div>
          ))}
        </div>
      </div>

      <PaneFooter className="gap-2 px-3">
        <Select value={format} onValueChange={setFormat}>
          <SelectTrigger size="sm" className="w-28">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="image/png">PNG</SelectItem>
            <SelectItem value="image/jpeg">JPEG</SelectItem>
            <SelectItem value="image/webp">WebP</SelectItem>
          </SelectContent>
        </Select>
        {format !== "image/png" && (
          <div className="flex items-center gap-2">
            <span className="text-hint text-muted-foreground">Quality</span>
            <Slider
              value={[quality]}
              min={30}
              max={100}
              step={1}
              className="w-24"
              onValueChange={([v]) => setQuality(v)}
            />
            <span className="numeric w-8 text-hint text-muted-foreground">{quality}</span>
          </div>
        )}
        <Input
          value={saveAs}
          onChange={(e) => setSaveAs(e.target.value)}
          placeholder={path.split("/").pop()}
          className="h-8 w-44 font-mono text-xs"
        />
        <span className="flex-1" />
        <Button
          size="sm"
          variant="outline"
          disabled={saving || !saveAs.trim()}
          onClick={() => save(saveAs)}
        >
          Save as
        </Button>
        <Button size="sm" onClick={() => save()} pending={saving}>
          <FloppyDisk className="size-4" />
          Save over original
        </Button>
      </PaneFooter>
    </div>
  )
}

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v))

function filterFor(adjust: { brightness: number; contrast: number; saturate: number }) {
  const parts: string[] = []
  if (adjust.brightness !== 100) parts.push(`brightness(${adjust.brightness}%)`)
  if (adjust.contrast !== 100) parts.push(`contrast(${adjust.contrast}%)`)
  if (adjust.saturate !== 100) parts.push(`saturate(${adjust.saturate}%)`)
  return parts.length ? parts.join(" ") : "none"
}

/**
 * The format to save in, defaulting to the one the file already is.
 *
 * Anything the canvas cannot re-encode — an SVG, an ICO, an AVIF the browser
 * decodes but will not write — becomes a PNG, because silently writing a file
 * whose extension no longer matches its bytes is the worse outcome.
 */
function defaultFormat(path: string) {
  const ext = path.split(".").pop()?.toLowerCase()
  if (ext === "jpg" || ext === "jpeg") return "image/jpeg"
  if (ext === "webp") return "image/webp"
  return "image/png"
}

function withExtension(name: string, mime: string) {
  const ext = mime === "image/jpeg" ? "jpg" : mime === "image/webp" ? "webp" : "png"
  const dot = name.lastIndexOf(".")
  const stem = dot > 0 ? name.slice(0, dot) : name
  return `${stem}.${ext}`
}

function toBlob(canvas: HTMLCanvasElement, mime: string, quality: number): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob(
      (blob) =>
        blob ? resolve(blob) : reject(new Error("the browser could not encode this image")),
      mime,
      quality,
    )
  })
}
