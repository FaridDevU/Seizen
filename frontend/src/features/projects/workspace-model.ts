export type WorkspaceViewport = { x: number; y: number; zoom: number }

type StoredNodeBase = {
  id: string
  x: number
  y: number
  width: number
  height: number
  z: number
}

export type TerminalShell = "cmd" | "wsl" | "codex" | "claude" | "opencode"

export type StoredTerminalNode = StoredNodeBase & {
  type: "terminal"
  shell: TerminalShell
}

export type StoredBrowserNode = StoredNodeBase & {
  type: "browser"
  url: string
}

export type StoredPlayerNode = StoredNodeBase & {
  type: "player"
}

export type StoredPhotoNode = StoredNodeBase & {
  type: "photo"
  assetId: string
}

export type StoredEditorNode = StoredNodeBase & {
  type: "editor"
  editorId: string
}

export type StoredWorkspaceNode =
  | StoredTerminalNode
  | StoredBrowserNode
  | StoredPlayerNode
  | StoredPhotoNode
  | StoredEditorNode

export type WorkspaceLayout = {
  viewport: WorkspaceViewport
  nodes: StoredWorkspaceNode[]
}

const defaultViewport: WorkspaceViewport = { x: 80, y: 88, zoom: 1 }

export function clampZoom(value: number) {
  return Math.min(2, Math.max(0.1, value))
}

export function fitWorkspaceViewport(
  canvasWidth: number,
  canvasHeight: number,
  nodes: readonly Pick<StoredNodeBase, "x" | "y" | "width" | "height">[],
): WorkspaceViewport {
  if (!nodes.length) return { ...defaultViewport }

  const left = Math.min(...nodes.map((node) => node.x))
  const top = Math.min(...nodes.map((node) => node.y))
  const right = Math.max(...nodes.map((node) => node.x + node.width))
  const bottom = Math.max(...nodes.map((node) => node.y + node.height))
  const width = Math.max(1, right - left)
  const height = Math.max(1, bottom - top)
  const padding = Math.min(72, canvasWidth / 4, canvasHeight / 4)
  const zoom = clampZoom(
    Math.min(
      (canvasWidth - padding * 2) / width,
      (canvasHeight - padding * 2) / height,
    ),
  )

  return {
    x: (canvasWidth - width * zoom) / 2 - left * zoom,
    y: (canvasHeight - height * zoom) / 2 - top * zoom,
    zoom,
  }
}

export function normalizeBrowserURL(value: string) {
  const candidate = value.trim()
  if (!candidate) return googleSearchURL("")

  const explicitProtocol = /^[a-z][a-z\d+.-]*:\/\//i.test(candidate)
  const hostLike =
    !/\s/.test(candidate) &&
    (/^localhost(?::\d+)?(?:[/?#]|$)/i.test(candidate) ||
      /^(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?(?:[/?#]|$)/.test(candidate) ||
      /^\[[\da-f:]+\](?::\d+)?(?:[/?#]|$)/i.test(candidate) ||
      /^[^/?#]+\.[^/?#]+/.test(candidate))
  if (!explicitProtocol && !hostLike) return googleSearchURL(candidate)

  const local = /^(?:localhost|127(?:\.\d{1,3}){3})(?::\d+)?(?:[/?#]|$)/i.test(
    candidate,
  )
  const url = new URL(explicitProtocol ? candidate : `${local ? "http" : "https"}://${candidate}`)
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error("The browser only supports HTTP or HTTPS addresses")
  }
  const hostname = url.hostname.toLowerCase()
  if (hostname === "wails.localhost" || hostname.endsWith(".wails.localhost")) {
    throw new Error("Seizen's internal address cannot be opened here")
  }
  if (url.username || url.password) {
    throw new Error("The address cannot include credentials")
  }
  return url.toString()
}

export function normalizeAppPreviewURL(value: string) {
  const candidate = value.trim()
  if (!candidate) return ""
  if (/^\d{1,5}$/.test(candidate)) {
    const port = Number(candidate)
    if (port < 1 || port > 65_535) throw new Error("The port is not valid")
    return `http://localhost:${port}/`
  }
  const explicitHTTP = /^https?:\/\//i.test(candidate)
  const hostLike =
    !/\s/.test(candidate) &&
    (/^localhost(?::\d+)?(?:[/?#]|$)/i.test(candidate) ||
      /^(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?(?:[/?#]|$)/.test(candidate) ||
      /^\[[\da-f:]+\](?::\d+)?(?:[/?#]|$)/i.test(candidate) ||
      /^[^/?#]+\.[^/?#]+/.test(candidate))
  if (!explicitHTTP && !hostLike) {
    throw new Error("Enter a port or a valid HTTP/HTTPS URL")
  }
  return normalizeBrowserURL(candidate)
}

export function normalizeEditorURL(value: string) {
  const match = /^http:\/\/127\.0\.0\.1:([1-9]\d{0,4})\/seizen\/[A-Za-z0-9_-]{43}\/$/.exec(
    value,
  )
  if (!match || Number(match[1]) > 65_535) {
    throw new Error("The editor session did not return a valid local address")
  }
  return value
}

function googleSearchURL(query: string) {
  const url = new URL("https://www.google.com/search")
  url.searchParams.set("igu", "1")
  if (query) url.searchParams.set("q", query)
  return url.toString()
}

export function terminalCommandInput(
  shell: StoredTerminalNode["shell"],
  value: string,
) {
  return `${value}${shell === "wsl" ? "\n" : "\r\n"}`
}

export function parseWorkspace(raw: string): WorkspaceLayout {
  if (!raw.trim()) return { viewport: { ...defaultViewport }, nodes: [] }

  try {
    const value: unknown = JSON.parse(raw)
    if (!isRecord(value) || value.version !== 1) throw new Error()

    const viewport = isRecord(value.viewport)
      ? {
          x: finite(value.viewport.x, defaultViewport.x, -1_000_000, 1_000_000),
          y: finite(value.viewport.y, defaultViewport.y, -1_000_000, 1_000_000),
          zoom: clampZoom(finite(value.viewport.zoom, 1, 0.1, 2)),
        }
      : { ...defaultViewport }
    const parsedNodes = Array.isArray(value.nodes)
      ? value.nodes.slice(0, 100).flatMap(parseNode)
      : []
    const seen = new Set<string>()
    let hasEditor = false
    const nodes = parsedNodes.filter((node) => {
      if (seen.has(node.id)) return false
      if (node.type === "editor" && hasEditor) return false
      seen.add(node.id)
      if (node.type === "editor") hasEditor = true
      return true
    })

    return { viewport, nodes }
  } catch {
    return { viewport: { ...defaultViewport }, nodes: [] }
  }
}

export function serializeWorkspace(
  viewport: WorkspaceViewport,
  nodes: readonly StoredWorkspaceNode[],
) {
  return JSON.stringify({
    version: 1,
    viewport: {
      x: viewport.x,
      y: viewport.y,
      zoom: viewport.zoom,
    },
    nodes: nodes.map((node) => {
      if (node.type === "terminal") {
        return {
          id: node.id,
          type: node.type,
          shell: node.shell,
          x: node.x,
          y: node.y,
          width: node.width,
          height: node.height,
          z: node.z,
        }
      }
      if (node.type === "browser") {
        return {
          id: node.id,
          type: node.type,
          url: node.url,
          x: node.x,
          y: node.y,
          width: node.width,
          height: node.height,
          z: node.z,
        }
      }
      if (node.type === "player") {
        return {
          id: node.id,
          type: node.type,
          x: node.x,
          y: node.y,
          width: node.width,
          height: node.height,
          z: node.z,
        }
      }
      if (node.type === "photo") {
        return {
          id: node.id,
          type: node.type,
          assetId: node.assetId,
          x: node.x,
          y: node.y,
          width: node.width,
          height: node.height,
          z: node.z,
        }
      }
      return {
        id: node.id,
        type: node.type,
        editorId: node.editorId,
        x: node.x,
        y: node.y,
        width: node.width,
        height: node.height,
        z: node.z,
      }
    }),
  })
}

function parseNode(value: unknown): StoredWorkspaceNode[] {
  if (!isRecord(value) || typeof value.id !== "string" || !value.id) return []
  const base = {
    id: value.id.slice(0, 100),
    x: finite(value.x, 0, -1_000_000, 1_000_000),
    y: finite(value.y, 0, -1_000_000, 1_000_000),
    width: finite(value.width, 520, 280, 1600),
    height: finite(value.height, 320, 190, 1200),
    z: finite(value.z, 1, 1, 100_000),
  }

  if (
    value.type === "terminal" &&
    (value.shell === "cmd" ||
      value.shell === "wsl" ||
      value.shell === "codex" ||
      value.shell === "claude" ||
      value.shell === "opencode")
  ) {
    return [{ ...base, type: "terminal", shell: value.shell }]
  }
  if (value.type === "browser" && typeof value.url === "string") {
    try {
      return [{ ...base, type: "browser", url: normalizeBrowserURL(value.url) }]
    } catch {
      return []
    }
  }
  if (value.type === "player") {
    return [{
      ...base,
      type: "player",
      ...(base.width === 480 && base.height === 330
        ? { width: 420, height: 190 }
        : {}),
    }]
  }
  if (
    value.type === "photo" &&
    typeof value.assetId === "string" &&
    /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(
      value.assetId,
    )
  ) {
    return [{ ...base, type: "photo", assetId: value.assetId }]
  }
  if (
    value.type === "editor" &&
    typeof value.editorId === "string" &&
    value.editorId
  ) {
    return [
      {
        ...base,
        type: "editor",
        editorId: value.editorId.slice(0, 100),
      },
    ]
  }
  return []
}

function finite(value: unknown, fallback: number, min: number, max: number) {
  return typeof value === "number" && Number.isFinite(value)
    ? Math.min(max, Math.max(min, value))
    : fallback
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null
}
