/** A labelled identity: a digest, a generation, an id. */
export function Digest({ label, value }: { label: string; value?: string }) {
  return (
    <p className="min-w-0 break-words">
      <span className="text-muted-foreground">{label}: </span>
      <span className="font-mono">{value || "Unknown"}</span>
    </p>
  )
}
