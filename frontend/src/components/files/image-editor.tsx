"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import ReactCrop, { centerCrop, makeAspectCrop } from "react-image-crop"
import "react-image-crop/dist/ReactCrop.css"
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
  CornerUpRight,
  External,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { API_BASE, mutationHeaders } from "@/lib/api"
import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import { SidePanel } from "@/components/side-panel"
import { useConfirm } from "@/components/confirm-dialog"
import { FileIcon } from "./file-icon"
import { editorHref } from "./search"
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
  root,
}: {
  path: string | null
  /** The file's modification time, so the source is not read from a cache. */
  modified?: string
  onOpenChange: (open: boolean) => void
  onSaved: (savedPath: string) => void
  root?: string
}) {
  const { confirm, dialog } = useConfirm()
  const [edit, setEdit] = useState<{ path: string; dirty: boolean }>()
  const reportDirty = useCallback(
    (dirty: boolean) => {
      if (path) setEdit({ path, dirty })
    },
    [path],
  )
  const dirty = edit?.path === path && edit.dirty
  const requestClose = (open: boolean) => {
    if (open || !dirty) return onOpenChange(open)
    confirm({
      title: "Close without saving?",
      description: <p>Your image has unsaved edits.</p>,
      confirmLabel: "Discard and close",
      action: async () => onOpenChange(false),
    })
  }
  return (
    <SidePanel
      open={path !== null}
      onOpenChange={requestClose}
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
          root={root}
          onDirtyChange={reportDirty}
          onClose={() => onOpenChange(false)}
          onSaved={onSaved}
        />
      )}
      {dialog}
    </SidePanel>
  )
}

type Rect = { x: number; y: number; w: number; h: number }

type ImageDraft = {
  history: HTMLCanvasElement[]
  redo: HTMLCanvasElement[]
  original: HTMLCanvasElement
  savedCanvas: HTMLCanvasElement | null
  format: string
  quality: number
  adjust: { brightness: number; contrast: number; saturate: number }
}
const imageDrafts = new Map<string, ImageDraft>()

export function ImageEditor({
  path,
  modified,
  onClose,
  onSaved,
  root,
  destination,
  onDirtyChange,
}: {
  path: string
  modified?: string
  onClose: () => void
  onSaved: (savedPath: string) => void
  root?: string
  destination?: boolean
  onDirtyChange?: (dirty: boolean) => void
}) {
  const router = useRouter()
  const [handoff] = useState(() => {
    const value = destination ? imageDrafts.get(path) : undefined
    imageDrafts.delete(path)
    return value
  })
  const [redo, setRedo] = useState<HTMLCanvasElement[]>(handoff?.redo ?? [])
  const [original, setOriginal] = useState<HTMLCanvasElement | null>(handoff?.original ?? null)
  const [savedCanvas, setSavedCanvas] = useState<HTMLCanvasElement | null>(
    handoff?.savedCanvas ?? null,
  )
  const [compare, setCompare] = useState(false)
  const [zoom, setZoom] = useState("fit")
  const [aspect, setAspect] = useState("free")
  const [history, setHistory] = useState<HTMLCanvasElement[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string>()
  const [saving, setSaving] = useState(false)
  const [cropping, setCropping] = useState(false)
  const [crop, setCrop] = useState<Rect | null>(null)
  const [format, setFormat] = useState(handoff?.format ?? defaultFormat(path))
  const [quality, setQuality] = useState(handoff?.quality ?? 90)
  const [adjust, setAdjust] = useState(
    handoff?.adjust ?? { brightness: 100, contrast: 100, saturate: 100 },
  )
  // The size fields default to whatever the canvas currently is, and hold a
  // draft only once somebody types in them. Copying the canvas dimensions into
  // state after every edit would be a render's worth of stale numbers each
  // time, and one more thing to keep in step.
  const [resizeDraft, setResizeDraft] = useState<{ w: string; h: string } | null>(null)
  const [lockRatio, setLockRatio] = useState(true)
  const [saveAs, setSaveAs] = useState("")

  const viewRef = useRef<HTMLCanvasElement>(null)
  const current = history[history.length - 1]
  const dirty = !!current && (current !== (savedCanvas ?? original) || filterFor(adjust) !== "none")
  useEffect(() => {
    onDirtyChange?.(dirty)
  }, [dirty, onDirtyChange])

  useEffect(() => {
    if (handoff) {
      // A microtask keeps restored editor state on the same asynchronous load
      // path as an image decoded from disk.
      queueMicrotask(() => {
        setHistory(handoff.history)
        setLoading(false)
      })
      return
    }
    const image = new Image()
    image.onload = () => {
      const canvas = document.createElement("canvas")
      // An SVG with no intrinsic size decodes to 0×0 and would silently save
      // an empty file; 1024 is a sane raster for one, and the notice below
      // says that saving rasterises it.
      canvas.width = image.naturalWidth || 1024
      canvas.height = image.naturalHeight || 1024
      canvas.getContext("2d")?.drawImage(image, 0, 0, canvas.width, canvas.height)
      setOriginal(canvas)
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
  }, [handoff, path, modified])

  // The visible canvas is repainted from the committed one plus the live
  // adjustment sliders, which are deliberately *not* committed until applied:
  // dragging brightness would otherwise push twenty bitmaps onto the undo
  // stack, one per pointer event.
  useEffect(() => {
    const view = viewRef.current
    if (!view || !current) return
    const source = compare ? (original ?? current) : current
    view.width = source.width
    view.height = source.height
    const ctx = view.getContext("2d")
    if (!ctx) return
    ctx.filter = compare ? "none" : filterFor(adjust)
    ctx.drawImage(source, 0, 0)
    ctx.filter = "none"
  }, [current, adjust, compare, cropping, original])

  const push = useCallback((canvas: HTMLCanvasElement) => {
    // Ten steps of undo, which is more than anybody needs for a crop and a
    // rotate and far less memory than an unbounded stack of full bitmaps.
    setHistory((prev) => [...prev, canvas].slice(-10))
    setRedo([])
    setCompare(false)
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
    if (!current || !crop || crop.w < 1 || crop.h < 1) return
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
    const w = Math.round(Number(resizeDraft?.w ?? current.width))
    const h = Math.round(Number(resizeDraft?.h ?? current.height))
    if (!Number.isFinite(w) || !Number.isFinite(h) || w < 1 || h < 1 || w * h > 32_000_000) {
      notify.error("Choose positive dimensions up to 32 megapixels")
      return
    }
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
    if (!current || saving) return
    const name = (asName || path.split("/").pop() || "image").trim()
    const finalName = withExtension(name, format)
    if (!finalName || finalName.includes("/") || finalName === "." || finalName === "..") {
      notify.error("Choose a filename without a slash")
      return
    }
    const dir = path.slice(0, path.lastIndexOf("/")) || "/"
    const target = (dir + "/" + finalName).replace(/\/{2,}/g, "/")
    setSaving(true)
    try {
      const output = document.createElement("canvas")
      output.width = current.width
      output.height = current.height
      const ctx = output.getContext("2d")
      if (!ctx) throw new Error("The browser could not create an image canvas")
      ctx.filter = filterFor(adjust)
      ctx.drawImage(current, 0, 0)
      const blob = await toBlob(output, format, quality / 100)
      const form = new FormData()
      form.append("file", blob, finalName)
      const res = await fetch(
        `${API_BASE}/files/upload?path=${encodeURIComponent(dir)}&overwrite=${target === path}`,
        { method: "POST", credentials: "include", headers: mutationHeaders(), body: form },
      )
      if (!res.ok) throw new Error((await res.json()).error?.message ?? res.statusText)
      notify.success(`Saved ${finalName}`, {
        description: `${bytes(blob.size)} · ${current.width}×${current.height}`,
      })
      if (target === path) {
        setSavedCanvas(output)
        setHistory([output])
        setAdjust({ brightness: 100, contrast: 100, saturate: 100 })
        setRedo([])
      }
      onSaved(target)
      if (!destination) onClose()
    } catch (err) {
      notify.error("Could not save the image", err)
    } finally {
      setSaving(false)
    }
  }

  const cropPercent = useMemo(
    () =>
      crop && current
        ? {
            unit: "%" as const,
            x: (crop.x / current.width) * 100,
            y: (crop.y / current.height) * 100,
            width: (crop.w / current.width) * 100,
            height: (crop.h / current.height) * 100,
          }
        : undefined,
    [crop, current],
  )

  const selectCrop = (value: string) => {
    if (!current) return
    const selection = centerCrop(
      makeAspectCrop(
        { unit: "%", width: 80 },
        value === "free" ? current.width / current.height : Number(value),
        current.width,
        current.height,
      ),
      current.width,
      current.height,
    )
    setCrop({
      x: (selection.x / 100) * current.width,
      y: (selection.y / 100) * current.height,
      w: (selection.width / 100) * current.width,
      h: (selection.height / 100) * current.height,
    })
  }

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
      <PaneHeader className="flex-wrap gap-2 px-3 py-2">
        <FileIcon
          entry={{ name: path.split("/").pop() ?? "", path, isDir: false, isSymlink: false }}
          className="size-5"
        />
        {destination && <h1 className="text-title font-semibold">{path.split("/").pop()}</h1>}
        <span className="numeric text-hint text-muted-foreground">
          {current.width} × {current.height}
        </span>
        <span className="text-hint text-muted-foreground">
          {dirty ? "Unsaved edits" : savedCanvas ? "Saved" : "Original"}
        </span>
        {!destination && (
          <Button
            size="sm"
            variant="outline"
            className="ml-auto"
            onClick={() => {
              if (!original) return
              imageDrafts.set(path, {
                history,
                redo,
                original,
                savedCanvas,
                format,
                quality,
                adjust,
              })
              router.push(editorHref(path, root))
            }}
          >
            <External className="size-3.5" />
            Open full editor
          </Button>
        )}
      </PaneHeader>
      <PaneHeader className="flex-wrap gap-1.5 px-3">
        <Button size="sm" variant="ghost" onClick={() => rotate(-90)}>
          <RotateCounterClockwise className="size-3" />
          Left
        </Button>
        <Button size="sm" variant="ghost" onClick={() => rotate(90)}>
          <RotateClockwise className="size-3" />
          Right
        </Button>
        <Button size="sm" variant="ghost" aria-label="Flip horizontally" onClick={() => flip("h")}>
          <ArrowLeftRight className="size-3" />
          Horizontal
        </Button>
        <Button size="sm" variant="ghost" aria-label="Flip vertically" onClick={() => flip("v")}>
          <ArrowUpDown className="size-3" />
          Vertical
        </Button>
        <Button
          size="sm"
          variant={cropping ? "secondary" : "ghost"}
          aria-pressed={cropping}
          onClick={() => {
            setCropping((v) => !v)
            setCompare(false)
            if (cropping) setCrop(null)
            else selectCrop(aspect)
          }}
        >
          <Crop className="size-3" />
          Crop
        </Button>
        {cropping && (
          <>
            <Select
              value={aspect}
              onValueChange={(value) => {
                setAspect(value)
                selectCrop(value)
              }}
            >
              <SelectTrigger size="sm" aria-label="Crop aspect ratio" className="h-8 w-28">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="free">Free crop</SelectItem>
                <SelectItem value="1">Square</SelectItem>
                <SelectItem value="1.7777777777777777">16:9</SelectItem>
                <SelectItem value="1.3333333333333333">4:3</SelectItem>
              </SelectContent>
            </Select>
          </>
        )}
        {cropping && crop && crop.w >= 1 && crop.h >= 1 && (
          <Button size="sm" onClick={applyCrop}>
            Apply {Math.round(crop.w)}×{Math.round(crop.h)}
          </Button>
        )}
        <span className="flex-1" />
        <Button
          size="sm"
          variant="ghost"
          disabled={history.length < 2}
          onClick={() => {
            setRedo((prev) => [...prev, current])
            setHistory((prev) => prev.slice(0, -1))
            setCrop(null)
            setResizeDraft(null)
          }}
        >
          <CornerUpLeft className="size-3" />
          Undo
        </Button>
        <Button
          size="sm"
          variant="ghost"
          disabled={redo.length === 0}
          onClick={() => {
            setHistory((prev) => [...prev, redo[redo.length - 1]])
            setRedo((prev) => prev.slice(0, -1))
            setCrop(null)
            setResizeDraft(null)
          }}
        >
          <CornerUpRight className="size-3" />
          Redo
        </Button>
        <Button
          size="sm"
          variant="ghost"
          disabled={!dirty}
          onClick={() => {
            if (original) push(original)
            setAdjust({ brightness: 100, contrast: 100, saturate: 100 })
          }}
        >
          Reset
        </Button>
        <Button
          size="sm"
          variant={compare ? "secondary" : "ghost"}
          aria-pressed={compare}
          onClick={() => {
            setCompare((v) => !v)
            setCropping(false)
          }}
        >
          Original
        </Button>
        <Select value={zoom} onValueChange={setZoom}>
          <SelectTrigger size="sm" aria-label="Image zoom" className="h-8 w-20">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {["fit", "50", "100", "200"].map((value) => (
              <SelectItem value={value} key={value}>
                {value === "fit" ? "Fit" : value + "%"}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </PaneHeader>

      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        <div className="relative flex min-h-44 flex-1 items-center justify-center overflow-auto checkerboard p-4">
          <ReactCrop
            disabled={!cropping || compare}
            crop={cropPercent}
            aspect={aspect === "free" ? undefined : Number(aspect)}
            ruleOfThirds
            onChange={(_, percent) =>
              setCrop({
                x: (percent.x / 100) * current.width,
                y: (percent.y / 100) * current.height,
                w: (percent.width / 100) * current.width,
                h: (percent.height / 100) * current.height,
              })
            }
          >
            <canvas
              ref={viewRef}
              aria-label="Image editing canvas"
              className={cn(
                "object-contain",
                zoom === "fit" ? "max-h-[45vh] max-w-full" : "max-w-none",
              )}
              style={zoom === "fit" ? undefined : { width: (current.width * Number(zoom)) / 100 }}
            />
          </ReactCrop>
        </div>

        <div className="grid shrink-0 gap-3 border-t border-hairline p-3 lg:grid-cols-2">
          <div className="space-y-2">
            <Label className="text-xs text-muted-foreground">Size</Label>
            <div className="flex flex-wrap items-center gap-2">
              <Input
                value={resizeDraft?.w ?? String(current.width)}
                aria-label="Image width"
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
                aria-label="Image height"
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
                  aria-label={key}
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
      </div>
      <PaneFooter className="flex-wrap gap-2 px-3 py-2">
        <Select value={format} onValueChange={setFormat}>
          <SelectTrigger size="sm" aria-label="Image format" className="w-24">
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
              aria-label="Export quality"
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
          aria-label="Save image as"
          onChange={(e) => setSaveAs(e.target.value)}
          placeholder={path.split("/").pop()}
          className="h-8 w-44 font-mono text-xs"
        />
        <span className="flex-1" />
        <Button
          size="sm"
          variant="outline"
          disabled={saving || !saveAs.trim() || saveAs.includes("/")}
          onClick={() => save(saveAs)}
        >
          Save as
        </Button>
        <Button size="sm" onClick={() => save()} pending={saving} disabled={cropping}>
          <FloppyDisk className="size-4" />
          {withExtension(path.split("/").pop() ?? "", format) === path.split("/").pop()
            ? "Save over original"
            : "Save converted copy"}
        </Button>
      </PaneFooter>
    </div>
  )
}

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
  const existing = name.split(".").pop()?.toLowerCase()
  if (
    (mime === "image/jpeg" && (existing === "jpg" || existing === "jpeg")) ||
    (mime === "image/png" && existing === "png") ||
    (mime === "image/webp" && existing === "webp")
  )
    return name
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
