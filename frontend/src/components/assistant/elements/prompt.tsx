"use client"

import { useState } from "react"
import { PaperAirplane, StopCircle } from "@/components/icons"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { cn } from "@/lib/utils"

/**
 * AI Elements' `prompt-input`, reduced to what this product sends: one question, no
 * attachments. A frame with the box and a toolbar under it, so what it is asked with
 * — the model, how much it may do — sits beside the press that asks. A beam runs
 * round the frame while it works (§11's `BorderBeam`: work is happening in here).
 */
export function PromptInput({
  busy,
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
  const ready = !busy && question.trim() !== ""
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
      <div className="relative rounded-xl border border-border-strong bg-card focus-within:border-signal">
        {busy && <BorderBeam size={96} duration={4} />}
        <Textarea
          aria-label={label}
          placeholder={placeholder}
          value={question}
          maxLength={maxLength}
          rows={rows}
          className="max-h-48 min-h-14 resize-none border-0 bg-transparent px-3 pt-2.5 shadow-none focus-visible:border-0 focus-visible:shadow-none"
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
          {busy ? (
            <Button type="button" size="sm" variant="outline" className="shrink-0" onClick={onStop}>
              <StopCircle />
              Stop
            </Button>
          ) : (
            <Button type="submit" size="sm" className="shrink-0" disabled={!ready}>
              <PaperAirplane />
              Ask
            </Button>
          )}
        </div>
      </div>
    </form>
  )
}
