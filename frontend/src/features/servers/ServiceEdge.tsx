import {
  BaseEdge,
  EdgeLabelRenderer,
  getBezierPath,
  type Edge,
  type EdgeProps,
} from "@xyflow/react"

import type {
  EdgeVisualState,
  ServerConnection,
} from "./server-diagram-model"

export type ServiceEdgeData = {
  connection: ServerConnection
  visual: EdgeVisualState
  healthcheckSequence: string
  onSelect: () => void
} & Record<string, unknown>

export type ServiceFlowEdge = Edge<ServiceEdgeData, "service">

export function ServiceEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
  data,
}: EdgeProps<ServiceFlowEdge>) {
  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  })
  if (!data) return null

  const { connection, visual, healthcheckSequence } = data
  const description = `${connection.protocol || "internal"}${
    connection.port ? `:${connection.port}` : ""
  }, ${visual.category}, ${visual.statusLabel}, direction source to target`

  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        markerEnd={markerEnd}
        style={{ stroke: visual.color, strokeWidth: 1.8 }}
      />
      {visual.mode !== "none" && (
        <path
          key={`${visual.mode}-${healthcheckSequence}`}
          d={path}
          fill="none"
          stroke={visual.color}
          strokeLinecap="round"
          strokeWidth="3.5"
          strokeDasharray="1 15"
          aria-hidden="true"
          className={`seizen-service-edge seizen-service-edge--${visual.mode}`}
          style={{ animationDuration: `${visual.durationSeconds}s` }}
        />
      )}
      <EdgeLabelRenderer>
        <button
          type="button"
          title={description}
          aria-label={description}
          onClick={data.onSelect}
          className="nodrag nopan pointer-events-auto absolute rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container-high)] px-2 py-1 font-mono text-[0.57rem] font-semibold text-[var(--on-surface)] shadow-sm focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
          style={{
            transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
          }}
        >
          <span style={{ color: visual.color }}>{visual.icon}</span>{" "}
          {connection.protocol || "internal"}
          {connection.port ? `:${connection.port}` : ""} → {visual.statusLabel}
        </button>
      </EdgeLabelRenderer>
    </>
  )
}
