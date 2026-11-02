import { TextShimmer } from "@/components/ui/text-shimmer"

/** One line saying the list is being read, inside a list. */
export function LoadingLine({ label = "Reading its models…" }: { label?: string }) {
  return (
    <p role="status" className="px-2 py-1.5 text-hint">
      <TextShimmer>{label}</TextShimmer>
    </p>
  )
}
