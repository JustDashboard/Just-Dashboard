/**
 * A count of added and removed lines, in the colours the lines are drawn in:
 * the head of a stack's compose diff and of a drift repair's owned file. It
 * was Docker's (`docker/stack-diff.tsx`) and Network imported it from there.
 */
export function DiffCount({ added, removed }: { added: number; removed: number }) {
  return (
    <span className="numeric flex shrink-0 items-center gap-1.5 font-mono text-hint">
      <span className="text-success">+{added}</span>
      <span className="text-destructive">−{removed}</span>
    </span>
  )
}
