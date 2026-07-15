import {
  Bot,
  Boxes,
  Cloud,
  Database,
  Globe2,
  HardDrive,
  Layers3,
  MessageSquare,
  Monitor,
  ShieldCheck,
  type LucideIcon,
} from "lucide-react"
import { Handle, Position, type Node, type NodeProps } from "@xyflow/react"

import {
  normalizeServiceKind,
  type ServerService,
  type ServiceKind,
  type ServiceMetadata,
} from "./server-diagram-model"

export type ServiceNodeData = {
  service: ServerService
  metadata: ServiceMetadata
} & Record<string, unknown>

export type ServiceFlowNode = Node<ServiceNodeData, "service">

const kindIcons: Record<ServiceKind, LucideIcon> = {
  internet: Globe2,
  proxy: ShieldCheck,
  frontend: Monitor,
  backend: Boxes,
  database: Database,
  cache: Layers3,
  queue: MessageSquare,
  worker: Bot,
  storage: HardDrive,
  external: Cloud,
}

const kindLabels: Record<ServiceKind, string> = {
  internet: "Internet",
  proxy: "Proxy",
  frontend: "Frontend",
  backend: "Backend",
  database: "Database",
  cache: "Cache",
  queue: "Queue",
  worker: "Worker",
  storage: "Storage",
  external: "External service",
}

const sourceLabels = {
  declared: "Declared",
  verified: "Verified",
  observed: "Observed",
} as const

export function ServiceNode({ data, selected }: NodeProps<ServiceFlowNode>) {
  const { service, metadata } = data
  const kind = normalizeServiceKind(service.kind)
  const Icon = kindIcons[kind]
  const endpoint = [service.host, service.port].filter(Boolean).join(":")
  const resources = [
    metadata.cpuPercent === undefined
      ? ""
      : `${metadata.cpuPercent}% CPU`,
    metadata.memoryUsedMb === undefined
      ? ""
      : `${metadata.memoryUsedMb}${
          metadata.memoryLimitMb === undefined
            ? ""
            : `/${metadata.memoryLimitMb}`
        } MB RAM`,
  ].filter(Boolean)

  return (
    <article
      aria-label={`${service.name}, ${kindLabels[kind]}, ${statusLabel(service.status)}`}
      className={`w-[232px] rounded-2xl border bg-[var(--surface-container-high)] p-3 shadow-[0_8px_24px_var(--shadow-elevated)] transition-[border-color,box-shadow] ${
        selected
          ? "border-[var(--primary)] shadow-[0_0_0_3px_var(--focus-ring)]"
          : "border-[var(--outline-variant)]"
      }`}
    >
      <Handle
        type="target"
        position={Position.Left}
        className="!size-2.5 !border-2 !border-[var(--surface-container-high)] !bg-[var(--primary)]"
      />
      <div className="flex items-start gap-3">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-[var(--primary-container)] text-[var(--on-primary-container)]">
          <Icon className="size-4" aria-hidden="true" />
        </span>
        <span className="min-w-0 flex-1">
          <strong className="block truncate text-xs">{service.name}</strong>
          <small className="mt-0.5 block text-[0.62rem] text-[var(--on-surface-variant)]">
            {kindLabels[kind]}
          </small>
        </span>
        <span
          title={`Health: ${statusLabel(service.status)}`}
          aria-label={`Health: ${statusLabel(service.status)}`}
          className={`mt-1 size-2.5 shrink-0 rounded-full ${statusClass(service.status)}`}
        />
      </div>
      <dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-[0.62rem]">
        <dt className="text-[var(--on-surface-variant)]">Health</dt>
        <dd className="truncate text-right font-medium">{statusLabel(service.status)}</dd>
        <dt className="text-[var(--on-surface-variant)]">Host</dt>
        <dd className="truncate text-right font-mono">{endpoint || "Not declared"}</dd>
        <dt className="text-[var(--on-surface-variant)]">Source</dt>
        <dd className="truncate text-right">
          {sourceLabels[service.source] ?? service.source}
        </dd>
        {resources.length > 0 && (
          <>
            <dt className="text-[var(--on-surface-variant)]">Resources</dt>
            <dd className="truncate text-right">{resources.join(" · ")}</dd>
          </>
        )}
      </dl>
      <Handle
        type="source"
        position={Position.Right}
        className="!size-2.5 !border-2 !border-[var(--surface-container-high)] !bg-[var(--primary)]"
      />
    </article>
  )
}

function statusClass(status: string) {
  if (status === "healthy" || status === "running") return "bg-[var(--success)]"
  if (status === "failed" || status === "error") return "bg-[var(--error)]"
  if (status === "degraded") return "bg-[var(--warning)]"
  return "bg-[var(--outline-variant)]"
}

const statusLabels: Record<string, string> = {
  healthy: "Healthy",
  running: "Running",
  failed: "Failed",
  error: "With errors",
  degraded: "Degraded",
  stopped: "Stopped",
  unknown: "Unknown",
}

export function statusLabel(status: string) {
  return statusLabels[status] ?? status
}
