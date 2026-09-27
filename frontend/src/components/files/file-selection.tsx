"use client"

import { Check } from "@/components/icons"
import { cn } from "@/lib/utils"

// File rows have a controlled two-state selection, never a form value or the
// select-all control's mixed state. A native button keeps the same checkbox
// keyboard/accessibility contract without a form-control tree for every file.
export function FileSelection({
  checked,
  onCheckedChange,
  className,
  ...props
}: {
  checked: boolean
  onCheckedChange: (checked: boolean) => void
} & Omit<React.ComponentProps<"button">, "onChange">) {
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={checked}
      data-slot="checkbox"
      data-state={checked ? "checked" : "unchecked"}
      className={cn(
        "peer grid size-4 shrink-0 place-content-center rounded-[4px] border border-input bg-control focus-ring transition-shadow disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[state=checked]:text-primary-foreground dark:data-[state=checked]:bg-primary",
        className,
      )}
      onClick={() => onCheckedChange(!checked)}
      onKeyDown={(event) => {
        if (event.key === "Enter") event.preventDefault()
      }}
      {...props}
    >
      {checked && <Check className="size-3.5" />}
    </button>
  )
}
