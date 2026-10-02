import { API_BASE, ApiError, mutationHeaders, type ApiErrorBody } from "@/lib/api"

/**
 * A multipart POST that says how much of the file has left the browser.
 *
 * `fetch` cannot: a body handed to it is sent with no word until the answer
 * comes back, and an import's answer comes only once every row is written. So
 * the file goes by `XMLHttpRequest`, as the file manager's uploads do, with
 * the same headers and the same errors as every other request the page makes
 * (`lib/api`): a refusal is an `ApiError`, a cancelled send rejects with the
 * signal's own reason.
 */
export function uploadForm<T>(
  path: string,
  body: FormData,
  options: {
    signal?: AbortSignal
    /** Bytes sent so far, of how many. */
    onProgress?: (sent: number, total: number) => void
  } = {},
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const { signal, onProgress } = options
    if (signal?.aborted) {
      reject(new DOMException("The upload was cancelled", "AbortError"))
      return
    }
    const xhr = new XMLHttpRequest()
    xhr.open("POST", `${API_BASE}${path}`)
    xhr.withCredentials = true
    for (const [key, value] of Object.entries(mutationHeaders())) xhr.setRequestHeader(key, value)
    const abort = () => xhr.abort()
    signal?.addEventListener("abort", abort)
    const settle = () => signal?.removeEventListener("abort", abort)
    if (onProgress) {
      xhr.upload.onprogress = (event) => {
        if (event.lengthComputable) onProgress(event.loaded, event.total)
      }
    }
    xhr.onload = () => {
      settle()
      const answer = readAnswer(xhr)
      if (answer instanceof ApiError) reject(answer)
      else resolve(answer as T)
    }
    xhr.onerror = () => {
      settle()
      reject(new Error("The connection dropped before the server answered."))
    }
    xhr.onabort = () => {
      settle()
      reject(new DOMException("The upload was cancelled", "AbortError"))
    }
    xhr.send(body)
  })
}

/** The answer of a finished request: its JSON, or the refusal it carries. */
export function readAnswer(xhr: Pick<XMLHttpRequest, "status" | "statusText" | "responseText">) {
  let parsed: unknown
  try {
    parsed = xhr.responseText ? JSON.parse(xhr.responseText) : undefined
  } catch {
    parsed = undefined
  }
  if (xhr.status >= 200 && xhr.status < 300) return parsed
  const error = (parsed as ApiErrorBody | undefined)?.error
  return new ApiError(
    xhr.status,
    error?.code ?? "unknown",
    error?.message ?? (xhr.responseText.slice(0, 300) || xhr.statusText || `HTTP ${xhr.status}`),
    undefined,
    {
      resource: error?.resource,
      operation: error?.operation,
      reason: error?.reason,
      raw: error?.raw,
      retryable: error?.retryable,
      field: error?.field,
      body: parsed,
    },
  )
}
