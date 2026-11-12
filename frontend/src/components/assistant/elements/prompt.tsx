"use client"

import { useRef, useState } from "react"
import { AnimatePresence, motion } from "motion/react"
import { ArrowUp, Cross, Image as ImageGlyph } from "@/components/icons"
import { Spinner } from "@/components/state"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import {
  MAX_IMAGES,
  imageFiles,
  imageSource,
  prepareImage,
  takeImages,
  type PastedImage,
} from "@/components/assistant/agent/images"
import { ImageLightbox } from "@/components/image-lightbox"

/**
 * AI Elements' `prompt-input`, reduced to what this product sends: one question,
 * and, where `acceptImages` says, images with it. A screenshot is pasted into the
 * box, dropped on the frame or picked with the toolbar's first button, and waits
 * above the words as a thumbnail with a way to take it out; a press on the
 * thumbnail opens it large, to check it is the right one. An image alone is a
 * question. A frame with the box and a toolbar under it, so what it is asked with
 * — the model, how much it may do — sits beside the press that asks. A beam runs
 * round the frame while it works (§11's `BorderBeam`: work is happening in here),
 * fading in and out rather than switching.
 *
 * Focus draws nothing here. The box is the page's one place to type and the caret
 * blinking in it is the whole answer to "is it ready": a ring or a lit border round
 * a frame that is always there only covered the words. The buttons in the toolbar
 * keep their ring for the keyboard. A press anywhere on the frame's own ground puts
 * the caret in the box.
 */
export function PromptInput({
  busy,
  disabled,
  placeholder,
  label,
  note,
  toolbar,
  onAsk,
  onStop,
  acceptImages = false,
  imagesNote,
  maxLength = 4000,
  rows = 2,
  className,
}: {
  busy: boolean
  /** Nothing can be asked yet; what is typed waits. */
  disabled?: boolean
  placeholder: string
  label: string
  /** The line above the box: something the reader should know before asking. */
  note?: React.ReactNode
  toolbar?: React.ReactNode
  onAsk: (question: string, images: PastedImage[]) => void
  onStop: () => void
  /** Images may be pasted, dropped or picked, and go with the question. */
  acceptImages?: boolean
  /** Said above the box while images wait to be sent. */
  imagesNote?: React.ReactNode
  maxLength?: number
  rows?: number
  className?: string
}) {
  const [question, setQuestion] = useState("")
  const [images, setImages] = useState<PastedImage[]>([])
  // Images still being made small, drawn as tiles that wait.
  const [reading, setReading] = useState(0)
  const [problem, setProblem] = useState("")
  const [dragging, setDragging] = useState(false)
  // The image opened large, by its place among those waiting.
  const [viewing, setViewing] = useState<number>()
  const box = useRef<HTMLTextAreaElement>(null)
  const picker = useRef<HTMLInputElement>(null)
  const ready = !busy && !disabled && reading === 0 && (question.trim() !== "" || images.length > 0)
  const send = () => {
    if (!ready) return
    onAsk(question.trim(), images)
    setQuestion("")
    setImages([])
    setProblem("")
  }
  const add = (files: File[]) => {
    const { taken, left } = takeImages(images.length + reading, files)
    setProblem(left > 0 ? `A question carries at most ${MAX_IMAGES} images.` : "")
    if (taken.length === 0) return
    setReading((n) => n + taken.length)
    for (const file of taken) {
      prepareImage(file)
        .then((image) => setImages((held) => (held.length < MAX_IMAGES ? [...held, image] : held)))
        .catch((err: unknown) =>
          setProblem(err instanceof Error ? err.message : "That image could not be read."),
        )
        .finally(() => setReading((n) => n - 1))
    }
  }
  return (
    <form
      data-slot="prompt-input"
      className={cn("shrink-0 space-y-2 px-3 pt-1 pb-3", className)}
      onSubmit={(event) => {
        event.preventDefault()
        send()
      }}
    >
      {note}
      {images.length > 0 && imagesNote}
      {problem && (
        <p role="alert" data-slot="prompt-problem" className="px-1 text-hint text-destructive">
          {problem}
        </p>
      )}
      <div
        data-slot="prompt-frame"
        data-dragging={dragging || undefined}
        className={cn(
          "relative cursor-text rounded-xl border border-border-strong bg-card transition-colors",
          dragging && "border-primary",
        )}
        onMouseDown={(event) => {
          // The frame's own ground and the toolbar's gaps are the box's too; a
          // control in the toolbar keeps its press.
          const target = event.target as HTMLElement
          // A press in the opened image's dialog reaches here through React, from
          // outside the frame: it is the dialog's.
          if (
            !event.currentTarget.contains(target) ||
            target === box.current ||
            target.closest("button, a, input, [role='combobox'], [data-slot='prompt-images']")
          )
            return
          event.preventDefault()
          box.current?.focus()
        }}
        onDragOver={(event) => {
          if (!acceptImages || !event.dataTransfer.types.includes("Files")) return
          event.preventDefault()
          setDragging(true)
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={(event) => {
          setDragging(false)
          if (!acceptImages) return
          const files = imageFiles(event.dataTransfer.files)
          if (files.length === 0) return
          event.preventDefault()
          add(files)
        }}
      >
        <AnimatePresence>
          {busy && (
            <motion.div
              key="beam"
              aria-hidden
              className="pointer-events-none absolute inset-0 rounded-[inherit]"
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              transition={{ duration: 0.25 }}
            >
              <BorderBeam size={96} duration={4} />
            </motion.div>
          )}
        </AnimatePresence>
        {(images.length > 0 || reading > 0) && (
          <ul data-slot="prompt-images" className="flex flex-wrap gap-2 px-3 pt-3">
            <AnimatePresence initial={false} mode="popLayout">
              {images.map((image, at) => (
                <motion.li
                  key={image.id}
                  layout
                  data-slot="prompt-image"
                  className="relative"
                  initial={{ opacity: 0, scale: 0.9 }}
                  animate={{ opacity: 1, scale: 1 }}
                  exit={{ opacity: 0, scale: 0.9 }}
                  transition={{ duration: 0.16, ease: [0.16, 1, 0.3, 1] }}
                >
                  <button
                    type="button"
                    aria-label={`Open image ${at + 1}`}
                    title="Open it large"
                    data-slot="prompt-image-open"
                    className="block cursor-zoom-in overflow-hidden rounded-md border border-hairline focus-ring"
                    onClick={() => setViewing(at)}
                  >
                    {/* A data address of a thumbnail made here: nothing for Next to optimise. */}
                    {/* eslint-disable-next-line @next/next/no-img-element */}
                    <img
                      src={image.src}
                      alt={`Image ${at + 1}`}
                      className="block size-14 object-cover transition-[filter] duration-150 hover:brightness-90"
                    />
                  </button>
                  <button
                    type="button"
                    aria-label={`Remove image ${at + 1}`}
                    className="absolute -top-1.5 -right-1.5 grid size-5 place-items-center rounded-full border border-border bg-card text-muted-foreground shadow-sm focus-ring transition-colors hover:text-foreground"
                    onClick={() => setImages((held) => held.filter((one) => one.id !== image.id))}
                  >
                    <Cross aria-hidden className="size-3" />
                  </button>
                </motion.li>
              ))}
              {Array.from({ length: reading }, (_, at) => (
                <motion.li
                  key={`reading-${at}`}
                  layout
                  aria-label="Reading an image"
                  className="grid size-14 place-items-center rounded-md border border-hairline bg-surface-sunken"
                  initial={{ opacity: 0, scale: 0.9 }}
                  animate={{ opacity: 1, scale: 1 }}
                  exit={{ opacity: 0 }}
                  transition={{ duration: 0.16 }}
                >
                  <Spinner className="text-muted-foreground" />
                </motion.li>
              ))}
            </AnimatePresence>
          </ul>
        )}
        <textarea
          ref={box}
          data-slot="prompt-box"
          aria-label={label}
          placeholder={placeholder}
          value={question}
          maxLength={maxLength}
          rows={rows}
          className="block field-sizing-content max-h-48 min-h-14 w-full resize-none border-0 bg-transparent px-3.5 pt-3 pb-1 text-base leading-relaxed caret-foreground outline-none placeholder:text-muted-foreground sm:text-body"
          onChange={(event) => setQuestion(event.target.value)}
          onPaste={(event) => {
            if (!acceptImages) return
            const files = imageFiles(
              Array.from(event.clipboardData.items, (item) =>
                item.kind === "file" ? item.getAsFile() : null,
              ),
            )
            if (files.length === 0) return
            // A screenshot comes alone; an image copied from a page brings its words
            // too, which still paste as words.
            if (!event.clipboardData.getData("text/plain")) event.preventDefault()
            add(files)
          }}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
              event.preventDefault()
              send()
            }
          }}
        />
        <div className="flex min-w-0 items-center gap-1.5 px-2 pb-2">
          <div className="mr-auto flex min-w-0 items-center gap-1">
            {acceptImages && (
              <>
                <Button
                  type="button"
                  size="xs"
                  variant="ghost"
                  aria-label="Add an image"
                  title="Add an image, or paste one"
                  data-slot="prompt-add-image"
                  disabled={images.length + reading >= MAX_IMAGES}
                  className="text-muted-foreground"
                  onClick={() => picker.current?.click()}
                >
                  <ImageGlyph aria-hidden />
                </Button>
                <input
                  ref={picker}
                  type="file"
                  accept="image/png,image/jpeg,image/gif,image/webp"
                  multiple
                  hidden
                  data-slot="prompt-image-picker"
                  onChange={(event) => {
                    add(imageFiles(event.target.files ?? []))
                    event.target.value = ""
                  }}
                />
              </>
            )}
            {toolbar}
          </div>
          <AskButton busy={busy} ready={ready} onStop={onStop} />
        </div>
      </div>
      {acceptImages && (
        <ImageLightbox
          images={images.map((image, at) => ({
            src: imageSource(image),
            placeholder: image.src,
            width: image.width,
            height: image.height,
            alt: images.length > 1 ? `Image ${at + 1} to send` : "The image to send",
          }))}
          at={viewing !== undefined && viewing < images.length ? viewing : undefined}
          onAt={setViewing}
          onClose={() => setViewing(undefined)}
        />
      )}
    </form>
  )
}

/**
 * One round button that is Ask while it is quiet and Stop while it works, as the
 * chat apps people know draw it: an arrow up on the command's blue to send, a
 * square on the text's own colour to stop. The glyph crosses over in place, so the
 * press under the pointer is never a different button that jumped in; the name is
 * the word, since there is none to read.
 */
function AskButton({ busy, ready, onStop }: { busy: boolean; ready: boolean; onStop: () => void }) {
  return (
    <Button
      type={busy ? "button" : "submit"}
      size="icon-sm"
      variant="default"
      data-slot="prompt-send"
      data-state={busy ? "stop" : "ask"}
      aria-label={busy ? "Stop" : "Ask"}
      title={busy ? "Stop" : "Ask"}
      className={cn(
        "relative shrink-0 overflow-hidden rounded-full transition-[background-color,color,opacity] duration-200 disabled:opacity-40",
        busy && "bg-foreground text-background hover:bg-foreground/85",
      )}
      disabled={!busy && !ready}
      onClick={
        busy
          ? (event) => {
              event.preventDefault()
              onStop()
            }
          : undefined
      }
    >
      <AnimatePresence mode="popLayout" initial={false}>
        <motion.span
          key={busy ? "stop" : "ask"}
          aria-hidden
          className="flex items-center justify-center"
          initial={{ opacity: 0, scale: 0.6 }}
          animate={{ opacity: 1, scale: 1 }}
          exit={{ opacity: 0, scale: 0.6 }}
          transition={{ duration: 0.16, ease: [0.16, 1, 0.3, 1] }}
        >
          {busy ? (
            <span className="block size-2.5 rounded-[2px] bg-current" />
          ) : (
            <ArrowUp className="size-4 [&_path]:stroke-[2.5]" />
          )}
        </motion.span>
      </AnimatePresence>
    </Button>
  )
}
