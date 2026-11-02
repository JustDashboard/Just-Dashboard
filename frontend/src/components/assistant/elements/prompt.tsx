"use client"

import { useRef, useState } from "react"
import { AnimatePresence, motion } from "motion/react"
import { PaperAirplane, StopCircle } from "@/components/icons"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

/**
 * AI Elements' `prompt-input`, reduced to what this product sends: one question, no
 * attachments. A frame with the box and a toolbar under it, so what it is asked with
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
  onAsk: (question: string) => void
  onStop: () => void
  maxLength?: number
  rows?: number
  className?: string
}) {
  const [question, setQuestion] = useState("")
  const box = useRef<HTMLTextAreaElement>(null)
  const ready = !busy && !disabled && question.trim() !== ""
  const send = () => {
    if (!ready) return
    onAsk(question.trim())
    setQuestion("")
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
      <div
        data-slot="prompt-frame"
        className="relative cursor-text rounded-xl border border-border-strong bg-card"
        onMouseDown={(event) => {
          // The frame's own ground and the toolbar's gaps are the box's too; a
          // control in the toolbar keeps its press.
          const target = event.target as HTMLElement
          if (target === box.current || target.closest("button, a, [role='combobox']")) return
          event.preventDefault()
          box.current?.focus()
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
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
              event.preventDefault()
              send()
            }
          }}
        />
        <div className="flex min-w-0 items-center gap-1.5 px-2 pb-2">
          <div className="mr-auto flex min-w-0 items-center gap-1">{toolbar}</div>
          <AskButton busy={busy} ready={ready} onStop={onStop} />
        </div>
      </div>
    </form>
  )
}

/**
 * One button that is Ask while it is quiet and Stop while it works: the face eases
 * from the command's blue to a control's, and the word and its glyph cross over in
 * place, so the press under the pointer is never a different button that jumped in.
 */
function AskButton({ busy, ready, onStop }: { busy: boolean; ready: boolean; onStop: () => void }) {
  return (
    <Button
      type={busy ? "button" : "submit"}
      size="sm"
      variant={busy ? "outline" : "default"}
      data-slot="prompt-send"
      data-state={busy ? "stop" : "ask"}
      // The word crossing over is two words for a moment; the name is one.
      aria-label={busy ? "Stop" : "Ask"}
      className="relative shrink-0 overflow-hidden transition-[background-color,border-color,color,opacity] duration-200"
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
          className="inline-flex items-center gap-1.5"
          initial={{ opacity: 0, y: 6 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0, y: -6 }}
          transition={{ duration: 0.16, ease: [0.16, 1, 0.3, 1] }}
        >
          {busy ? <StopCircle /> : <PaperAirplane />}
          {busy ? "Stop" : "Ask"}
        </motion.span>
      </AnimatePresence>
    </Button>
  )
}
