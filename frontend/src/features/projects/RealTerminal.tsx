import { useEffect, useRef, useState } from "react"
import { LoaderCircle } from "lucide-react"
import { FitAddon } from "@xterm/addon-fit"
import { Terminal } from "@xterm/xterm"
import "@xterm/xterm/css/xterm.css"
import {
  ClipboardGetText,
  ClipboardSetText,
} from "../../../wailsjs/runtime/runtime"

import { cn } from "@/lib/utils"

export type RealTerminalStatus = "starting" | "running" | "exited" | "error"

export type RealTerminalHandle = {
  write: (data: string) => void
  writeln: (data?: string) => void
  focus: () => void
}

type TerminalCallback = void | Promise<void>

export type RealTerminalProps = {
  sessionId?: string
  status: RealTerminalStatus
  error?: string | null
  onReady: (handle: RealTerminalHandle | null) => void
  onData: (sessionId: string, data: string) => TerminalCallback
  onBinary?: (sessionId: string, data: string) => TerminalCallback
  onResize: (sessionId: string, columns: number, rows: number) => TerminalCallback
  autoFocus?: boolean
  zoom?: number
  ariaLabel?: string
  className?: string
}

const fitDelay = 40
const resizeEpsilon = 0.5

function RealTerminal({
  sessionId,
  status,
  error,
  onReady,
  onData,
  onBinary,
  onResize,
  autoFocus = false,
  zoom = 1,
  ariaLabel = "Terminal",
  className,
}: RealTerminalProps) {
  const hostRef = useRef<HTMLDivElement>(null)
  const terminalRef = useRef<Terminal | null>(null)
  const handleRef = useRef<RealTerminalHandle | null>(null)
  const sessionIdRef = useRef(sessionId)
  const statusRef = useRef(status)
  const zoomRef = useRef(zoom)
  const onReadyRef = useRef(onReady)
  const onDataRef = useRef(onData)
  const onBinaryRef = useRef(onBinary)
  const onResizeRef = useRef(onResize)
  const inputQueueRef = useRef<Promise<void>>(Promise.resolve())
  const sessionGenerationRef = useRef(0)
  const lastGridRef = useRef({ columns: 0, rows: 0 })
  const lastSentResizeRef = useRef<{
    sessionId: string
    columns: number
    rows: number
  } | null>(null)
  const mountedRef = useRef(false)
  const [internalError, setInternalError] = useState("")

  sessionIdRef.current = sessionId
  statusRef.current = status
  zoomRef.current = zoom
  onReadyRef.current = onReady
  onDataRef.current = onData
  onBinaryRef.current = onBinary
  onResizeRef.current = onResize

  const reportError = (caught: unknown) => {
    if (mountedRef.current) {
      setInternalError(caught instanceof Error ? caught.message : String(caught))
    }
  }

  const sendResize = (columns: number, rows: number) => {
    lastGridRef.current = { columns, rows }
    const activeSession = sessionIdRef.current
    if (!activeSession || statusRef.current !== "running") return

    const last = lastSentResizeRef.current
    if (
      last?.sessionId === activeSession &&
      last.columns === columns &&
      last.rows === rows
    ) {
      return
    }
    lastSentResizeRef.current = { sessionId: activeSession, columns, rows }
    const generation = sessionGenerationRef.current

    try {
      Promise.resolve(onResizeRef.current(activeSession, columns, rows)).catch(
        (caught: unknown) => {
          if (
            generation === sessionGenerationRef.current &&
            activeSession === sessionIdRef.current
          ) {
            reportError(caught)
          }
        },
      )
    } catch (caught) {
      reportError(caught)
    }
  }

  useEffect(() => {
    mountedRef.current = true
    const host = hostRef.current
    if (!host) return

    const terminal = new Terminal({
      allowTransparency: false,
      convertEol: false,
      cursorBlink: true,
      disableStdin: !sessionIdRef.current || statusRef.current !== "running",
      fontFamily:
        '"Cascadia Mono", "SFMono-Regular", Consolas, "Liberation Mono", monospace',
      fontSize: 12,
      scrollback: 5_000,
      theme: {
        background: "#111513",
        foreground: "#e4e9e4",
        cursor: "#a7c4ae",
        cursorAccent: "#111513",
        selectionBackground: "#5f786966",
      },
      windowsPty: { backend: "conpty" },
    })
    const fitAddon = new FitAddon()
    terminal.loadAddon(fitAddon)
    terminal.open(host)
    // Explicit clipboard: the WebView's native paste event isn't reliable,
    // so we copy/paste via the Wails runtime with a fallback to the browser.
    const copySelection = () => {
      const selection = terminal.getSelection()
      if (!selection) return
      void ClipboardSetText(selection).catch(() => {
        void navigator.clipboard?.writeText(selection).catch(reportError)
      })
    }
    const pasteFromClipboard = () => {
      if (!sessionIdRef.current || statusRef.current !== "running") return
      void ClipboardGetText()
        .then((text) => {
          if (text && !disposed) terminal.paste(text)
        })
        .catch(() => {
          void navigator.clipboard
            ?.readText()
            .then((text) => {
              if (text && !disposed) terminal.paste(text)
            })
            .catch(reportError)
        })
    }
    terminal.attachCustomKeyEventHandler((event) => {
      if (event.type !== "keydown") return true
      const key = event.key.toLowerCase()
      const wantsCopy =
        (event.ctrlKey && !event.altKey && key === "c") ||
        (event.ctrlKey && key === "insert")
      if (wantsCopy && terminal.hasSelection()) {
        copySelection()
        return false
      }
      const wantsPaste =
        (event.ctrlKey && !event.altKey && key === "v") ||
        (event.shiftKey && key === "insert")
      if (wantsPaste) {
        pasteFromClipboard()
        return false
      }
      return true
    })
    terminal.textarea?.setAttribute("aria-label", ariaLabel)
    terminalRef.current = terminal

    let disposed = false
    let fitTimer: number | undefined
    let fitFrame: number | undefined
    let lastWidth = 0
    let lastHeight = 0

    const handle: RealTerminalHandle = {
      write: (data) => {
        if (!disposed) terminal.write(data)
      },
      writeln: (data = "") => {
        if (!disposed) terminal.writeln(data)
      },
      focus: () => {
        if (!disposed) terminal.focus()
      },
    }
    handleRef.current = handle
    onReadyRef.current(handle)

    const fit = () => {
      fitFrame = undefined
      if (disposed || host.clientWidth < 1 || host.clientHeight < 1) return
      lastWidth = host.clientWidth
      lastHeight = host.clientHeight
      try {
        fitAddon.fit()
        sendResize(terminal.cols, terminal.rows)
      } catch {
        // The host can briefly have no measurable cells during layout changes.
      }
    }

    const scheduleFit = () => {
      const width = host.clientWidth
      const height = host.clientHeight
      if (
        Math.abs(width - lastWidth) < resizeEpsilon &&
        Math.abs(height - lastHeight) < resizeEpsilon
      ) {
        return
      }
      window.clearTimeout(fitTimer)
      if (fitFrame !== undefined) window.cancelAnimationFrame(fitFrame)
      fitTimer = window.setTimeout(() => {
        fitTimer = undefined
        fitFrame = window.requestAnimationFrame(fit)
      }, fitDelay)
    }

    const resizeObserver = new ResizeObserver(scheduleFit)
    resizeObserver.observe(host)
    fitFrame = window.requestAnimationFrame(fit)

    const queueInput = (
      data: string,
      callback: (sessionId: string, value: string) => TerminalCallback,
    ) => {
      const activeSession = sessionIdRef.current
      if (!activeSession || statusRef.current !== "running") return
      const generation = sessionGenerationRef.current

      inputQueueRef.current = inputQueueRef.current
        .catch(() => undefined)
        .then(() => {
          if (
            generation !== sessionGenerationRef.current ||
            activeSession !== sessionIdRef.current ||
            statusRef.current !== "running"
          ) {
            return
          }
          return callback(activeSession, data)
        })
        .catch((caught: unknown) => {
          if (
            generation === sessionGenerationRef.current &&
            activeSession === sessionIdRef.current
          ) {
            reportError(caught)
          }
        })
    }
    const inputDisposable = terminal.onData((data) => {
      queueInput(data, (activeSession, value) =>
        onDataRef.current(activeSession, value),
      )
    })
    const binaryDisposable = terminal.onBinary((data) => {
      queueInput(data, (activeSession, value) =>
        (onBinaryRef.current ?? onDataRef.current)(activeSession, value),
      )
    })
    const resizeDisposable = terminal.onResize(({ cols, rows }) => {
      sendResize(cols, rows)
    })

    const clipboardOnRightClick = (event: MouseEvent) => {
      event.preventDefault()
      event.stopPropagation()
      if (terminal.hasSelection()) {
        void ClipboardSetText(terminal.getSelection()).catch(reportError)
        return
      }
      const activeSession = sessionIdRef.current
      const generation = sessionGenerationRef.current
      if (!activeSession || statusRef.current !== "running") return
      terminal.focus()
      void ClipboardGetText()
        .then((text) => {
          if (
            text &&
            !disposed &&
            generation === sessionGenerationRef.current &&
            activeSession === sessionIdRef.current &&
            statusRef.current === "running"
          ) {
            terminal.paste(text)
          }
        })
        .catch(reportError)
    }
    host.addEventListener("contextmenu", clipboardOnRightClick, true)

    // xterm measures cells before the workspace's CSS zoom is applied. Correct
    // mouse coordinates so selection and mouse-aware TUIs hit the right cell.
    const adjustedMouseEvents = new WeakSet<Event>()
    let mouseDragActive = false
    const adjustMouseCoordinates = (event: MouseEvent) => {
      const targetIsTerminal =
        event.target instanceof Node && host.contains(event.target)
      if (event.type === "mousedown" && targetIsTerminal) {
        mouseDragActive = true
      }
      if ((!targetIsTerminal && !mouseDragActive) || adjustedMouseEvents.has(event)) {
        return
      }
      adjustedMouseEvents.add(event)
      const scale = zoomRef.current
      if (!Number.isFinite(scale) || Math.abs(scale - 1) < 0.001) return
      const screen = host.querySelector<HTMLElement>(".xterm-screen")
      if (!screen) return
      const rect = screen.getBoundingClientRect()
      try {
        Object.defineProperty(event, "clientX", {
          value: rect.left + (event.clientX - rect.left) / scale,
          configurable: true,
        })
        Object.defineProperty(event, "clientY", {
          value: rect.top + (event.clientY - rect.top) / scale,
          configurable: true,
        })
      } catch {
        // Some embedded browser versions expose non-configurable coordinates.
      }
    }
    const onMouseEvent = (event: Event) => {
      adjustMouseCoordinates(event as MouseEvent)
      if (event.type === "mouseup") mouseDragActive = false
    }
    const stopMouseDrag = () => {
      mouseDragActive = false
    }
    document.addEventListener("mousedown", onMouseEvent, true)
    document.addEventListener("mousemove", onMouseEvent, true)
    document.addEventListener("mouseup", onMouseEvent, true)
    document.addEventListener("contextmenu", onMouseEvent, true)
    document.addEventListener("wheel", onMouseEvent, true)
    window.addEventListener("blur", stopMouseDrag)

    return () => {
      mountedRef.current = false
      disposed = true
      resizeObserver.disconnect()
      inputDisposable.dispose()
      binaryDisposable.dispose()
      resizeDisposable.dispose()
      host.removeEventListener("contextmenu", clipboardOnRightClick, true)
      window.clearTimeout(fitTimer)
      if (fitFrame !== undefined) window.cancelAnimationFrame(fitFrame)
      document.removeEventListener("mousedown", onMouseEvent, true)
      document.removeEventListener("mousemove", onMouseEvent, true)
      document.removeEventListener("mouseup", onMouseEvent, true)
      document.removeEventListener("contextmenu", onMouseEvent, true)
      document.removeEventListener("wheel", onMouseEvent, true)
      window.removeEventListener("blur", stopMouseDrag)
      onReadyRef.current(null)
      handleRef.current = null
      terminalRef.current = null
      terminal.dispose()
    }
  }, [])

  useEffect(() => {
    sessionGenerationRef.current += 1
    inputQueueRef.current = Promise.resolve()
    lastSentResizeRef.current = null
    setInternalError("")

    const terminal = terminalRef.current
    if (terminal) {
      terminal.options.disableStdin = !sessionId || status !== "running"
    }
    if (sessionId && status === "running") {
      const { columns, rows } = lastGridRef.current
      if (columns > 0 && rows > 0) sendResize(columns, rows)
    }
  }, [sessionId, status])

  useEffect(() => {
    if (!autoFocus || status !== "running") return
    const frame = window.requestAnimationFrame(() => handleRef.current?.focus())
    return () => window.cancelAnimationFrame(frame)
  }, [autoFocus, sessionId, status])

  const visibleError = error?.trim() || internalError
  const message = visibleError
    ? visibleError
    : status === "starting"
      ? "Starting terminal…"
      : status === "exited"
        ? "Terminal closed."
        : status === "error"
          ? "Could not start the terminal."
          : ""

  return (
    <div
      aria-label={ariaLabel}
      className={cn(
        "relative size-full min-h-0 overflow-hidden bg-[#111513]",
        className,
      )}
    >
      <div ref={hostRef} className="absolute inset-2 overflow-hidden" />
      {status === "starting" && !visibleError && (
        <div
          role="status"
          aria-live="polite"
          className="overlay-in pointer-events-none absolute inset-0 flex items-center justify-center gap-2 text-[0.68rem] text-[#b5bdb7]"
        >
          <LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />
          {message}
        </div>
      )}
      {message && (status !== "starting" || Boolean(visibleError)) && (
        <div
          role={visibleError || status === "error" ? "alert" : "status"}
          aria-live={visibleError || status === "error" ? "assertive" : "polite"}
          className={cn(
            // ponytail: hex fixed on purpose — the terminal is always dark, theme tokens don't apply
            "view-enter pointer-events-none absolute inset-x-3 bottom-3 rounded-xl px-3 py-1.5 text-[0.62rem] shadow-[0_4px_14px_rgba(0,0,0,0.3)]",
            visibleError || status === "error"
              ? "bg-[#4a2424]/95 text-[#ffcbc7]"
              : "bg-[#252c28]/95 text-[#b5bdb7]",
          )}
        >
          {message}
        </div>
      )}
    </div>
  )
}

export { RealTerminal }
