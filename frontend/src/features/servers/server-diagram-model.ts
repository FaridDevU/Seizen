export const serviceKinds = [
  "internet",
  "proxy",
  "frontend",
  "backend",
  "database",
  "cache",
  "queue",
  "worker",
  "storage",
  "external",
] as const

export type ServiceKind = (typeof serviceKinds)[number]
export type TopologySource = "declared" | "verified" | "observed"

export type ServerService = {
  id: string
  serverId: string
  name: string
  kind: string
  host: string
  port: number | null
  protocol: string
  healthcheckUrl: string
  status: string
  source: TopologySource
  metadataJson: string
  positionJson: string
}

export type ServerConnection = {
  id: string
  serverId: string
  sourceServiceId: string | null
  targetServiceId: string | null
  protocol: string
  port: number | null
  status: string
  source: TopologySource
  trafficRate: number
  errorRate: number
  metadataJson: string
}

export type ServiceMetadata = {
  cpuPercent?: number
  memoryUsedMb?: number
  memoryLimitMb?: number
}

export type EdgeFlowMode =
  | "none"
  | "healthy"
  | "traffic"
  | "degraded"
  | "healthcheck"

export type EdgeVisualState = {
  color: string
  category: string
  icon: string
  mode: EdgeFlowMode
  durationSeconds: number
  statusLabel: string
}

const stoppedStatuses = new Set([
  "draft",
  "provisioning",
  "stopped",
  "starting",
  "stopping",
  "failed",
  "deleting",
])

const errorStatuses = new Set(["error", "failed", "unhealthy"])

export function normalizeServiceKind(kind: string): ServiceKind {
  return serviceKinds.includes(kind as ServiceKind)
    ? (kind as ServiceKind)
    : "external"
}

export function parseServicePosition(
  raw: string,
): { x: number; y: number } | undefined {
  try {
    const value: unknown = JSON.parse(raw || "{}")
    if (
      value &&
      typeof value === "object" &&
      typeof (value as { x?: unknown }).x === "number" &&
      Number.isFinite((value as { x: number }).x) &&
      typeof (value as { y?: unknown }).y === "number" &&
      Number.isFinite((value as { y: number }).y)
    ) {
      return {
        x: (value as { x: number }).x,
        y: (value as { y: number }).y,
      }
    }
  } catch {
    // Invalid agent metadata is ignored at the UI boundary.
  }
  return undefined
}

export function parseServiceMetadata(raw: string): ServiceMetadata {
  try {
    const value: unknown = JSON.parse(raw || "{}")
    if (!value || typeof value !== "object") return {}
    const record = value as Record<string, unknown>
    return {
      ...finiteNumber("cpuPercent", record.cpuPercent),
      ...finiteNumber("memoryUsedMb", record.memoryUsedMb),
      ...finiteNumber("memoryLimitMb", record.memoryLimitMb),
    }
  } catch {
    return {}
  }
}

export function protocolVisual(protocol: string) {
  const normalized = protocol.trim().toLowerCase()
  if (normalized === "http" || normalized === "https") {
    return { color: "#3b82f6", category: "HTTP", icon: "HTTP" }
  }
  if (normalized === "ws" || normalized === "wss") {
    return { color: "#06b6d4", category: "WebSocket", icon: "WS" }
  }
  if (["postgres", "mysql"].includes(normalized)) {
    return { color: "#22c55e", category: "Database", icon: "DB" }
  }
  if (["redis", "amqp", "mqtt"].includes(normalized)) {
    return { color: "#f59e0b", category: "Cache or queue", icon: "QUEUE" }
  }
  if (normalized === "storage") {
    return { color: "#64748b", category: "Storage", icon: "DISK" }
  }
  return { color: "#8b5cf6", category: "Internal", icon: "INT" }
}

export function edgeVisualState(
  serverStatus: string,
  connection: ServerConnection,
  healthcheckSequence = "",
): EdgeVisualState {
  const protocol = protocolVisual(connection.protocol)
  const status = connection.status.trim().toLowerCase()

  if (stoppedStatuses.has(serverStatus)) {
    return {
      ...protocol,
      mode: "none",
      durationSeconds: 0,
      statusLabel: "No flow: server stopped",
    }
  }
  if (errorStatuses.has(status) || connection.errorRate > 0) {
    return {
      color: "#ef4444",
      category: protocol.category,
      icon: "ERROR",
      mode: "none",
      durationSeconds: 0,
      statusLabel: "Error",
    }
  }
  if (healthcheckSequence) {
    return {
      ...protocol,
      mode: "healthcheck",
      durationSeconds: 0.9,
      statusLabel: "Checking health",
    }
  }
  if (serverStatus === "degraded" || status === "degraded") {
    return {
      ...protocol,
      mode: "degraded",
      durationSeconds: 1.8,
      statusLabel: "Degraded",
    }
  }
  if (connection.trafficRate > 0) {
    return {
      ...protocol,
      mode: "traffic",
      durationSeconds: trafficDuration(connection.trafficRate),
      statusLabel: `Traffic ${connection.trafficRate}`,
    }
  }
  if (status === "healthy") {
    return {
      ...protocol,
      mode: "healthy",
      durationSeconds: 2.8,
      statusLabel: "Healthy",
    }
  }
  return {
    ...protocol,
    mode: "none",
    durationSeconds: 0,
    statusLabel: connection.source === "declared" ? "Declared" : status,
  }
}

function trafficDuration(rate: number) {
  return Math.max(0.8, Math.min(5, 5 - Math.log10(rate + 1) * 1.3))
}

function finiteNumber(key: keyof ServiceMetadata, value: unknown) {
  return typeof value === "number" && Number.isFinite(value) && value >= 0
    ? { [key]: value }
    : {}
}
