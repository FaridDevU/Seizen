export type WorkspaceViewport = { x: number; y: number; zoom: number }

type StoredNodeBase = {
  id: string
  x: number
  y: number
  width: number
  height: number
  z: number
  // Canvas folder membership; assigned on release when a panel overlaps a
  // region by more than half of its area.
  regionId?: string
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

export const noteColors = ["default", "amber", "emerald", "violet", "rose"] as const
export type NoteColor = (typeof noteColors)[number]

export type StoredNoteNode = StoredNodeBase & {
  type: "note"
  text: string
  color: NoteColor
}

export type TodoItem = { id: string; text: string; done: boolean }

export type StoredTodoNode = StoredNodeBase & {
  type: "todo"
  items: TodoItem[]
}

export type DocumentKind = "pdf" | "docx" | "image" | "video" | "audio" | "text"

export type StoredDocumentNode = StoredNodeBase & {
  type: "document"
  assetId: string
  name: string
  kind: DocumentKind
}

export type StoredWorkspaceNode =
  | StoredTerminalNode
  | StoredBrowserNode
  | StoredPlayerNode
  | StoredPhotoNode
  | StoredEditorNode
  | StoredNoteNode
  | StoredTodoNode
  | StoredDocumentNode

// A region is a labeled "folder" rectangle on the canvas. Moving it carries its
// member panels along; its optional cwd points new terminals at a subfolder.
export type StoredRegion = {
  id: string
  x: number
  y: number
  width: number
  height: number
  label: string
  color: NoteColor
  cwd?: string
}

export const maximumRegions = 20
export const maximumRegionLabel = 60
export const maximumRegionCwd = 260

export type WorkspaceLayout = {
  viewport: WorkspaceViewport
  nodes: StoredWorkspaceNode[]
  regions: StoredRegion[]
}

const defaultViewport: WorkspaceViewport = { x: 80, y: 88, zoom: 1 }

export const maximumNoteCharacters = 20_000
export const maximumTodoItems = 200
export const maximumTodoItemCharacters = 500
export const documentKinds: readonly DocumentKind[] = [
  "pdf",
  "docx",
  "image",
  "video",
  "audio",
  "text",
]

export function workspaceAssetURL(projectId: string, assetId: string) {
  return `/workspace-asset/${encodeURIComponent(projectId)}/${assetId}`
}

// --- Auto-layout helpers -------------------------------------------------
// Pure geometry over the stored node shape; the canvas calls these on release
// (snap), on add (free placement), and from the Tidy action (masonry).

const layoutGrid = 20
const layoutGap = 40
export const tidyGap = 16

type LayoutBox = Pick<StoredNodeBase, "x" | "y" | "width" | "height">

export function snapToGrid(value: number) {
  return Math.round(value / layoutGrid) * layoutGrid
}

function boxesOverlap(a: LayoutBox, b: LayoutBox) {
  return !(
    a.x + a.width <= b.x ||
    b.x + b.width <= a.x ||
    a.y + a.height <= b.y ||
    b.y + b.height <= a.y
  )
}

// Walks outward in the four cardinal directions from the preferred spot and
// returns the nearest free slot, snapped to the grid. Falls back to below the
// lowest panel when everything within range is occupied.
export function findFreePosition(
  nodes: readonly LayoutBox[],
  preferred: { x: number; y: number },
  size: { width: number; height: number },
): { x: number; y: number } {
  const start = { x: snapToGrid(preferred.x), y: snapToGrid(preferred.y) }
  if (nodes.length === 0) return start
  const collides = (position: { x: number; y: number }) =>
    nodes.find((node) => boxesOverlap(node, { ...position, ...size }))
  if (!collides(start)) return start

  const centerX = preferred.x + size.width / 2
  const centerY = preferred.y + size.height / 2
  const directions = [
    [1, 0],
    [-1, 0],
    [0, 1],
    [0, -1],
  ] as const
  let best: { x: number; y: number } | null = null
  let bestDistance = Infinity
  for (const [directionX, directionY] of directions) {
    let candidate = {
      x: start.x + directionX * (size.width + layoutGap),
      y: start.y + directionY * (size.height + layoutGap),
    }
    let slot: { x: number; y: number } | null = null
    for (let step = 0; step < 200; step++) {
      const blocking = collides(candidate)
      if (!blocking) {
        slot = candidate
        break
      }
      candidate =
        directionX > 0
          ? { x: blocking.x + blocking.width + layoutGap, y: candidate.y }
          : directionX < 0
            ? { x: blocking.x - size.width - layoutGap, y: candidate.y }
            : directionY > 0
              ? { x: candidate.x, y: blocking.y + blocking.height + layoutGap }
              : { x: candidate.x, y: blocking.y - size.height - layoutGap }
    }
    if (!slot) continue
    const distance = Math.hypot(
      slot.x + size.width / 2 - centerX,
      slot.y + size.height / 2 - centerY,
    )
    if (distance < bestDistance) {
      bestDistance = distance
      best = slot
    }
  }
  const fallback = best ?? {
    x: start.x,
    y: Math.max(...nodes.map((node) => node.y + node.height)) + layoutGap,
  }
  return { x: snapToGrid(fallback.x), y: snapToGrid(fallback.y) }
}

const snapThreshold = 8

// Snaps a released box to the grid, then lets a nearby neighbor edge win when
// it is closer than the grid: aligned panels beat rigid grid positions.
export function snapDragPosition(
  box: LayoutBox,
  neighbors: readonly LayoutBox[],
): { x: number; y: number } {
  let x = snapToGrid(box.x)
  let y = snapToGrid(box.y)
  let bestDeltaX = Math.abs(x - box.x) + 0.01
  let bestDeltaY = Math.abs(y - box.y) + 0.01
  for (const neighbor of neighbors) {
    const candidatesX = [
      neighbor.x,
      neighbor.x + neighbor.width - box.width,
      neighbor.x + neighbor.width + tidyGap,
      neighbor.x - box.width - tidyGap,
    ]
    for (const candidate of candidatesX) {
      const delta = Math.abs(candidate - box.x)
      if (delta < snapThreshold && delta < bestDeltaX) {
        bestDeltaX = delta
        x = candidate
      }
    }
    const candidatesY = [
      neighbor.y,
      neighbor.y + neighbor.height - box.height,
      neighbor.y + neighbor.height + tidyGap,
      neighbor.y - box.height - tidyGap,
    ]
    for (const candidate of candidatesY) {
      const delta = Math.abs(candidate - box.y)
      if (delta < snapThreshold && delta < bestDeltaY) {
        bestDeltaY = delta
        y = candidate
      }
    }
  }
  return { x, y }
}

// Masonry: sorted by current visual order, each box drops into the currently
// shortest column. Returns positions by node index.
export function tidyLayout(
  nodes: readonly LayoutBox[],
  canvasWidth: number,
): Map<number, { x: number; y: number }> {
  const result = new Map<number, { x: number; y: number }>()
  if (nodes.length === 0) return result
  const columnWidth =
    Math.max(...nodes.map((node) => node.width)) + tidyGap
  const columnCount = Math.max(
    1,
    Math.min(nodes.length, Math.floor(canvasWidth / columnWidth) || 1),
  )
  const columnHeights = Array.from({ length: columnCount }, () => 0)
  const order = nodes
    .map((node, index) => ({ node, index }))
    .sort((a, b) => a.node.y - b.node.y || a.node.x - b.node.x)
  for (const { node, index } of order) {
    let shortest = 0
    for (let column = 1; column < columnCount; column++) {
      if (columnHeights[column] < columnHeights[shortest]) shortest = column
    }
    result.set(index, {
      x: snapToGrid(shortest * columnWidth),
      y: snapToGrid(columnHeights[shortest]),
    })
    columnHeights[shortest] += node.height + tidyGap
  }
  return result
}

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
  if (!raw.trim()) {
    return { viewport: { ...defaultViewport }, nodes: [], regions: [] }
  }

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
    const parsedRegions = Array.isArray(value.regions)
      ? value.regions.slice(0, maximumRegions).flatMap(parseRegion)
      : []
    const regionIds = new Set<string>()
    const regions = parsedRegions.filter((region) => {
      if (regionIds.has(region.id)) return false
      regionIds.add(region.id)
      return true
    })
    const seen = new Set<string>()
    let hasEditor = false
    const nodes = parsedNodes
      .filter((node) => {
        if (seen.has(node.id)) return false
        if (node.type === "editor" && hasEditor) return false
        seen.add(node.id)
        if (node.type === "editor") hasEditor = true
        return true
      })
      // A membership pointing at a deleted region is dropped on load.
      .map((node) =>
        node.regionId && !regionIds.has(node.regionId)
          ? { ...node, regionId: undefined }
          : node,
      )

    return { viewport, nodes, regions }
  } catch {
    return { viewport: { ...defaultViewport }, nodes: [], regions: [] }
  }
}

export function serializeWorkspace(
  viewport: WorkspaceViewport,
  nodes: readonly StoredWorkspaceNode[],
  regions: readonly StoredRegion[] = [],
) {
  const geometry = (node: StoredWorkspaceNode) => ({
    id: node.id,
    x: node.x,
    y: node.y,
    width: node.width,
    height: node.height,
    z: node.z,
    ...(node.regionId ? { regionId: node.regionId } : {}),
  })
  return JSON.stringify({
    version: 1,
    viewport: {
      x: viewport.x,
      y: viewport.y,
      zoom: viewport.zoom,
    },
    nodes: nodes.map((node) => {
      if (node.type === "terminal") {
        return { ...geometry(node), type: node.type, shell: node.shell }
      }
      if (node.type === "browser") {
        return { ...geometry(node), type: node.type, url: node.url }
      }
      if (node.type === "player") {
        return { ...geometry(node), type: node.type }
      }
      if (node.type === "photo") {
        return { ...geometry(node), type: node.type, assetId: node.assetId }
      }
      if (node.type === "note") {
        return {
          ...geometry(node),
          type: node.type,
          text: node.text,
          color: node.color,
        }
      }
      if (node.type === "todo") {
        return {
          ...geometry(node),
          type: node.type,
          items: node.items.map((item) => ({
            id: item.id,
            text: item.text,
            done: item.done,
          })),
        }
      }
      if (node.type === "document") {
        return {
          ...geometry(node),
          type: node.type,
          assetId: node.assetId,
          name: node.name,
          kind: node.kind,
        }
      }
      return { ...geometry(node), type: node.type, editorId: node.editorId }
    }),
    regions: regions.map((region) => ({
      id: region.id,
      x: region.x,
      y: region.y,
      width: region.width,
      height: region.height,
      label: region.label,
      color: region.color,
      ...(region.cwd ? { cwd: region.cwd } : {}),
    })),
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
    ...(typeof value.regionId === "string" && value.regionId
      ? { regionId: value.regionId.slice(0, 100) }
      : {}),
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
  if (value.type === "note" && typeof value.text === "string") {
    return [{
      ...base,
      type: "note",
      text: value.text.slice(0, maximumNoteCharacters),
      color: noteColors.includes(value.color as NoteColor)
        ? (value.color as NoteColor)
        : "default",
    }]
  }
  if (value.type === "todo" && Array.isArray(value.items)) {
    const items: TodoItem[] = []
    const seen = new Set<string>()
    for (const item of value.items.slice(0, maximumTodoItems)) {
      if (!isRecord(item) || typeof item.id !== "string" || !item.id) continue
      if (typeof item.text !== "string" || seen.has(item.id)) continue
      seen.add(item.id)
      items.push({
        id: item.id.slice(0, 100),
        text: item.text.slice(0, maximumTodoItemCharacters),
        done: item.done === true,
      })
    }
    return [{ ...base, type: "todo", items }]
  }
  if (
    value.type === "document" &&
    typeof value.assetId === "string" &&
    /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(
      value.assetId,
    ) &&
    typeof value.name === "string" &&
    documentKinds.includes(value.kind as DocumentKind)
  ) {
    return [{
      ...base,
      type: "document",
      assetId: value.assetId,
      name: value.name.slice(0, 255),
      kind: value.kind as DocumentKind,
    }]
  }
  return []
}

function parseRegion(value: unknown): StoredRegion[] {
  if (!isRecord(value) || typeof value.id !== "string" || !value.id) return []
  if (typeof value.label !== "string") return []
  const rawCwd = typeof value.cwd === "string" ? value.cwd.trim() : ""
  const cwd =
    rawCwd &&
    rawCwd.length <= maximumRegionCwd &&
    !rawCwd.includes("..") &&
    !rawCwd.includes(":") &&
    !rawCwd.startsWith("/") &&
    !rawCwd.startsWith("\\")
      ? rawCwd
      : undefined
  return [{
    id: value.id.slice(0, 100),
    x: finite(value.x, 0, -1_000_000, 1_000_000),
    y: finite(value.y, 0, -1_000_000, 1_000_000),
    width: finite(value.width, 640, 160, 6000),
    height: finite(value.height, 420, 160, 6000),
    label: value.label.slice(0, maximumRegionLabel),
    color: noteColors.includes(value.color as NoteColor)
      ? (value.color as NoteColor)
      : "default",
    ...(cwd ? { cwd } : {}),
  }]
}

// Deterministic membership: a panel joins the region covering more than half of
// its area; the largest overlap wins and ties break on the smaller region id.
export function regionContaining(
  box: LayoutBox,
  regions: readonly StoredRegion[],
): string | undefined {
  const area = box.width * box.height
  if (area <= 0) return undefined
  let bestId: string | undefined
  let bestOverlap = area * 0.5
  for (const region of regions) {
    const overlapWidth =
      Math.min(box.x + box.width, region.x + region.width) -
      Math.max(box.x, region.x)
    const overlapHeight =
      Math.min(box.y + box.height, region.y + region.height) -
      Math.max(box.y, region.y)
    const overlap = Math.max(0, overlapWidth) * Math.max(0, overlapHeight)
    if (
      overlap > bestOverlap ||
      (overlap === bestOverlap && bestId !== undefined && region.id < bestId)
    ) {
      bestOverlap = overlap
      bestId = region.id
    }
  }
  return bestId
}

function finite(value: unknown, fallback: number, min: number, max: number) {
  return typeof value === "number" && Number.isFinite(value)
    ? Math.min(max, Math.max(min, value))
    : fallback
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null
}
