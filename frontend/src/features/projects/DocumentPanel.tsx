import { useEffect, useRef, useState } from "react"
import { CircleAlert, ExternalLink, LoaderCircle } from "lucide-react"

import { Button } from "@/components/ui/button"

import type { DocumentKind } from "./workspace-model"

// Universal read-only viewer for workspace document nodes. The asset streams from
// Seizen's asset server (same origin), so fetch/pdf.js/video all work without CORS.
export function DocumentPanel({
  url,
  kind,
  name,
  onOpenExternally,
}: {
  url: string
  kind: DocumentKind
  name: string
  onOpenExternally?: () => void
}) {
  if (kind === "image") {
    return (
      <div className="h-full bg-[var(--surface)] p-1">
        <img
          src={url}
          alt={name}
          draggable={false}
          className="h-full w-full select-none object-contain"
        />
      </div>
    )
  }
  if (kind === "video") {
    return (
      <div className="flex h-full items-center justify-center bg-[var(--surface)]">
        {/* eslint-disable-next-line jsx-a11y/media-has-caption -- user files have no captions track */}
        <video src={url} controls className="max-h-full max-w-full" />
      </div>
    )
  }
  if (kind === "audio") {
    return (
      <div className="flex h-full items-center justify-center bg-[var(--surface)] px-6">
        {/* eslint-disable-next-line jsx-a11y/media-has-caption -- user files have no captions track */}
        <audio src={url} controls className="w-full max-w-md" />
      </div>
    )
  }
  if (kind === "pdf") {
    return <PdfViewer url={url} name={name} onOpenExternally={onOpenExternally} />
  }
  if (kind === "docx") {
    return <DocxViewer url={url} name={name} onOpenExternally={onOpenExternally} />
  }
  return <TextViewer url={url} name={name} />
}

function ViewerStatus({ error, onRetry, onOpenExternally }: {
  error?: string
  onRetry?: () => void
  onOpenExternally?: () => void
}) {
  if (!error) {
    return (
      <div role="status" className="flex h-full items-center justify-center gap-2 text-xs text-[var(--on-surface-variant)]">
        <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />
        Loading document
      </div>
    )
  }
  return (
    <div role="alert" className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
      <CircleAlert className="size-5 text-[var(--error)]" />
      <p className="max-w-sm text-xs text-[var(--on-surface-variant)]">{error}</p>
      <div className="flex items-center gap-2">
        {onRetry && (
          <Button type="button" variant="outline" className="h-8 px-3 text-xs" onClick={onRetry}>
            Retry
          </Button>
        )}
        {onOpenExternally && (
          <Button type="button" variant="outline" className="h-8 px-3 text-xs" onClick={onOpenExternally}>
            <ExternalLink className="size-3.5" />
            Open in system app
          </Button>
        )}
      </div>
    </div>
  )
}

// pdf.js pages render sequentially into canvases; the worker loads once per app.
const maximumPdfPages = 200

let pdfjsModule: Promise<typeof import("pdfjs-dist")> | null = null
function loadPdfjs() {
  if (!pdfjsModule) {
    pdfjsModule = Promise.all([
      import("pdfjs-dist"),
      import("pdfjs-dist/build/pdf.worker.min.mjs?url"),
    ]).then(([pdfjs, worker]) => {
      pdfjs.GlobalWorkerOptions.workerSrc = worker.default
      return pdfjs
    })
  }
  return pdfjsModule
}

function PdfViewer({ url, name, onOpenExternally }: {
  url: string
  name: string
  onOpenExternally?: () => void
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [state, setState] = useState<{ status: "loading" | "ready" | "error"; error?: string; pages?: number }>({ status: "loading" })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let cancelled = false
    setState({ status: "loading" })
    const container = containerRef.current
    if (container) container.replaceChildren()

    void (async () => {
      try {
        const pdfjs = await loadPdfjs()
        const document_ = await pdfjs.getDocument({ url }).promise
        if (cancelled) return
        const total = Math.min(document_.numPages, maximumPdfPages)
        setState({ status: "ready", pages: document_.numPages })
        const scale = 1.5 * (window.devicePixelRatio || 1)
        for (let pageNumber = 1; pageNumber <= total; pageNumber++) {
          if (cancelled || !containerRef.current) return
          const page = await document_.getPage(pageNumber)
          if (cancelled) return
          const viewport = page.getViewport({ scale })
          const canvas = document.createElement("canvas")
          canvas.width = viewport.width
          canvas.height = viewport.height
          canvas.style.width = "100%"
          canvas.style.height = "auto"
          canvas.className = "rounded-md shadow-[0_1px_3px_var(--shadow-soft)]"
          const context = canvas.getContext("2d")
          if (!context) continue
          containerRef.current.append(canvas)
          await page.render({ canvas, canvasContext: context, viewport }).promise
        }
      } catch (error) {
        if (!cancelled) {
          setState({
            status: "error",
            error: `Could not display ${name}: ${error instanceof Error ? error.message : String(error)}`,
          })
        }
      }
    })()
    return () => {
      cancelled = true
    }
  }, [url, name, attempt])

  return (
    <div className="h-full overflow-y-auto bg-[var(--surface)]">
      {state.status !== "ready" && (
        <ViewerStatus
          error={state.status === "error" ? state.error : undefined}
          onRetry={() => setAttempt((current) => current + 1)}
          onOpenExternally={onOpenExternally}
        />
      )}
      <div
        ref={containerRef}
        aria-label={`Pages of ${name}`}
        className="mx-auto flex max-w-3xl flex-col gap-3 p-3"
      />
      {state.status === "ready" && (state.pages ?? 0) > maximumPdfPages && (
        <p className="pb-3 text-center text-[0.65rem] text-[var(--on-surface-variant)]">
          Showing the first {maximumPdfPages} pages of {state.pages}.
        </p>
      )}
    </div>
  )
}

function DocxViewer({ url, name, onOpenExternally }: {
  url: string
  name: string
  onOpenExternally?: () => void
}) {
  const [state, setState] = useState<{ status: "loading" | "ready" | "error"; error?: string; html?: string }>({ status: "loading" })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let cancelled = false
    setState({ status: "loading" })
    void (async () => {
      try {
        const [mammoth, response] = await Promise.all([
          import("mammoth/mammoth.browser"),
          fetch(url),
        ])
        if (!response.ok) throw new Error(`the document could not be read (${response.status})`)
        const arrayBuffer = await response.arrayBuffer()
        const result = await mammoth.default.convertToHtml({ arrayBuffer })
        if (!cancelled) setState({ status: "ready", html: result.value })
      } catch (error) {
        if (!cancelled) {
          setState({
            status: "error",
            error: `Could not display ${name}: ${error instanceof Error ? error.message : String(error)}`,
          })
        }
      }
    })()
    return () => {
      cancelled = true
    }
  }, [url, name, attempt])

  if (state.status !== "ready") {
    return (
      <div className="h-full bg-[var(--surface)]">
        <ViewerStatus
          error={state.status === "error" ? state.error : undefined}
          onRetry={() => setAttempt((current) => current + 1)}
          onOpenExternally={onOpenExternally}
        />
      </div>
    )
  }
  return (
    <div className="h-full overflow-y-auto bg-[var(--surface)]">
      <div
        aria-label={name}
        className="document-prose mx-auto max-w-3xl select-text px-6 py-5 text-sm leading-6"
        // mammoth generates the markup itself from the DOCX body; it never passes
        // raw HTML through, so this stays inert.
        dangerouslySetInnerHTML={{ __html: state.html ?? "" }}
      />
    </div>
  )
}

const maximumTextPreviewBytes = 2 << 20 // 2 MiB is plenty for a readable preview.

function TextViewer({ url, name }: { url: string; name: string }) {
  const [state, setState] = useState<{ status: "loading" | "ready" | "error"; error?: string; text?: string; truncated?: boolean }>({ status: "loading" })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let cancelled = false
    setState({ status: "loading" })
    void (async () => {
      try {
        const response = await fetch(url)
        if (!response.ok) throw new Error(`the document could not be read (${response.status})`)
        const blob = await response.blob()
        const truncated = blob.size > maximumTextPreviewBytes
        const text = await blob.slice(0, maximumTextPreviewBytes).text()
        if (!cancelled) setState({ status: "ready", text, truncated })
      } catch (error) {
        if (!cancelled) {
          setState({
            status: "error",
            error: `Could not display ${name}: ${error instanceof Error ? error.message : String(error)}`,
          })
        }
      }
    })()
    return () => {
      cancelled = true
    }
  }, [url, name, attempt])

  if (state.status !== "ready") {
    return (
      <div className="h-full bg-[var(--surface)]">
        <ViewerStatus
          error={state.status === "error" ? state.error : undefined}
          onRetry={() => setAttempt((current) => current + 1)}
        />
      </div>
    )
  }
  return (
    <div className="h-full overflow-y-auto bg-[var(--surface)]">
      <pre className="select-text whitespace-pre-wrap break-words px-4 py-3 font-mono text-xs leading-5 text-[var(--on-surface)]">
        {state.text}
      </pre>
      {state.truncated && (
        <p className="pb-3 text-center text-[0.65rem] text-[var(--on-surface-variant)]">
          Preview truncated at 2 MiB.
        </p>
      )}
    </div>
  )
}
