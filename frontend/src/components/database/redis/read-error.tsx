import { ApiError } from "@/lib/api"
import { ErrorState } from "@/components/state"

/**
 * The same failure, said to be worth asking again. `ErrorState` offers the
 * retry only where the server marked the failure so, and the Redis routes
 * mark none: a server that did not answer is simply `connect_failed`. Every
 * failure drawn through here is a read of a server that may answer the next
 * time — it was restarting, the network blinked, a scan timed out — so the
 * way to ask again is always offered.
 */
export function worthRetrying(error: Error): ApiError {
  if (!(error instanceof ApiError)) {
    return new ApiError(0, "unreadable", error.message, undefined, { retryable: true })
  }
  if (error.retryable) return error
  return new ApiError(error.status, error.code, error.message, error.confirmPhrase, {
    resource: error.resource,
    operation: error.operation,
    reason: error.reason,
    raw: error.raw,
    field: error.field,
    body: error.body,
    retryable: true,
  })
}

/** A read that failed, where the content would be, with the way to try again. */
export function ReadError({
  error,
  onRetry,
  className,
}: {
  error: Error
  onRetry: () => void
  className?: string
}) {
  return <ErrorState error={worthRetrying(error)} onRetry={onRetry} className={className} />
}
