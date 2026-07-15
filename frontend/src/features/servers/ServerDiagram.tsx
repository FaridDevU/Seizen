import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  Background,
  BackgroundVariant,
  Controls,
  MarkerType,
  ReactFlow,
  ReactFlowProvider,
  useNodesState,
  useReactFlow,
  type EdgeTypes,
  type NodeMouseHandler,
  type NodeTypes,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import {
  Activity,
  AlignHorizontalDistributeCenter,
  CircleAlert,
  FileText,
  LoaderCircle,
  Network,
  SquareTerminal,
  X,
} from "lucide-react"
import { EventsOn } from "../../../wailsjs/runtime/runtime"

import { Button } from "@/components/ui/button"
import {
  projectService,
  type ProjectServer,
  type TopologyHealthcheckResult,
} from "@/features/projects/project-service"

import { layoutServerGraph } from "./elk-layout"
import { ServiceEdge, type ServiceFlowEdge } from "./ServiceEdge"
import { ServiceNode, statusLabel, type ServiceFlowNode } from "./ServiceNode"
import {
  edgeVisualState,
  parseServiceMetadata,
  parseServicePosition,
  type ServerConnection,
  type ServerService,
} from "./server-diagram-model"

const nodeTypes: NodeTypes = { service: ServiceNode }
const edgeTypes: EdgeTypes = { service: ServiceEdge }

type HealthPulse = Pick<
  TopologyHealthcheckResult,
  "sequenceId" | "serverId" | "serviceId" | "connectionId"
>

export function ServerDiagram({
  projectId,
  server,
  onOpenTerminal,
  onOpenLogs,
}: {
  projectId: string
  server: ProjectServer
  onOpenTerminal: (service?: ServerService) => void
  onOpenLogs: () => void
}) {
  return (
    <ReactFlowProvider>
      <ServerDiagramCanvas
        projectId={projectId}
        server={server}
        onOpenTerminal={onOpenTerminal}
        onOpenLogs={onOpenLogs}
      />
    </ReactFlowProvider>
  )
}

function ServerDiagramCanvas({
  projectId,
  server,
  onOpenTerminal,
  onOpenLogs,
}: {
  projectId: string
  server: ProjectServer
  onOpenTerminal: (service?: ServerService) => void
  onOpenLogs: () => void
}) {
  const [services, setServices] = useState<ServerService[]>([])
  const [connections, setConnections] = useState<ServerConnection[]>([])
  const [nodes, setNodes, onNodesChange] = useNodesState<ServiceFlowNode>([])
  const [selectedServiceId, setSelectedServiceId] = useState("")
  const [selectedConnectionId, setSelectedConnectionId] = useState("")
  const [pulse, setPulse] = useState<HealthPulse>()
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const fittedServer = useRef("")
  const pulseTimer = useRef<number | undefined>(undefined)
  const { fitView } = useReactFlow()

  const load = useCallback(async () => {
    try {
      const [nextServices, nextConnections] = await Promise.all([
        projectService.listServerServices(projectId, server.id),
        projectService.listServerConnections(projectId, server.id),
      ])
      setServices(nextServices ?? [])
      setConnections(nextConnections ?? [])
      setError("")
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setLoading(false)
    }
  }, [projectId, server.id])

  useEffect(() => {
    setLoading(true)
    setSelectedServiceId("")
    setSelectedConnectionId("")
    fittedServer.current = ""
    void load()
  }, [load])

  useEffect(() => {
    setNodes(
      services.map((service, index) => ({
        id: service.id,
        type: "service",
        position: parseServicePosition(service.positionJson) ?? {
          x: (index % 3) * 300 + 48,
          y: Math.floor(index / 3) * 190 + 48,
        },
        data: { service, metadata: parseServiceMetadata(service.metadataJson) },
        ariaLabel: `${service.name}, ${service.kind}, ${service.status}`,
      })),
    )
  }, [services, setNodes])

  const edges = useMemo<ServiceFlowEdge[]>(() => {
    const serviceIds = new Set(services.map(({ id }) => id))
    return connections.flatMap((connection) => {
      const source = connection.sourceServiceId
      const target = connection.targetServiceId
      if (!source || !target || !serviceIds.has(source) || !serviceIds.has(target)) {
        return []
      }
      const sequence =
        pulse &&
        (pulse.connectionId === connection.id ||
          pulse.serviceId === source ||
          pulse.serviceId === target)
          ? pulse.sequenceId
          : ""
      const visual = edgeVisualState(server.status, connection, sequence)
      return [
        {
          id: connection.id,
          type: "service",
          source,
          target,
          data: {
            connection,
            visual,
            healthcheckSequence: sequence,
            onSelect: () => {
              setSelectedConnectionId(connection.id)
              setSelectedServiceId("")
            },
          },
          markerEnd: { type: MarkerType.ArrowClosed, color: visual.color },
          ariaLabel: `${connection.protocol}, ${visual.statusLabel}, source to target`,
        },
      ]
    })
  }, [connections, pulse, server.status, services])

  useEffect(() => {
    if (nodes.length === 0 || fittedServer.current === server.id) return
    fittedServer.current = server.id
    requestAnimationFrame(() => void fitView({ padding: 0.18, duration: 250 }))
  }, [fitView, nodes.length, server.id])

  useEffect(() => {
    const refreshEvents = [
      "server.topology.service.registered",
      "server.topology.service.updated",
      "server.topology.service.position",
      "server.topology.service.healthy",
      "server.topology.service.failed",
      "server.topology.connection.registered",
      "server.topology.connection.updated",
      "server.topology.connection.healthy",
      "server.topology.connection.failed",
      "server.topology.healthcheck.result",
    ].map((event) =>
      EventsOn(event, (payload: unknown) => {
        if (belongsToServer(payload, server.id)) void load()
      }),
    )
    const offPulse = EventsOn(
      "server.topology.healthcheck.pulse",
      (payload: unknown) => {
        if (!isHealthPulse(payload) || payload.serverId !== server.id) return
        setPulse(payload)
        window.clearTimeout(pulseTimer.current)
        pulseTimer.current = window.setTimeout(
          () => setPulse((current) =>
            current?.sequenceId === payload.sequenceId ? undefined : current,
          ),
          1_100,
        )
      },
    )
    return () => {
      refreshEvents.forEach((off) => off())
      offPulse()
      window.clearTimeout(pulseTimer.current)
    }
  }, [load, server.id])

  const run = async (action: () => Promise<unknown>) => {
    setBusy(true)
    setError("")
    try {
      await action()
      await load()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  const autoLayout = () =>
    run(async () => {
      const arranged = await layoutServerGraph(nodes, edges)
      setNodes(arranged)
      await Promise.all(
        arranged.map((node) =>
          projectService.updateServicePosition(
            projectId,
            server.id,
            node.id,
            JSON.stringify(node.position),
          ),
        ),
      )
      requestAnimationFrame(() => void fitView({ padding: 0.18, duration: 300 }))
    })

  const selectNode: NodeMouseHandler<ServiceFlowNode> = (_, node) => {
    setSelectedServiceId(node.id)
    setSelectedConnectionId("")
  }

  const selectedService = services.find(({ id }) => id === selectedServiceId)
  const selectedConnection = connections.find(
    ({ id }) => id === selectedConnectionId,
  )

  return (
    <div
      role="region"
      aria-label={`Topology of ${server.name}`}
      className="absolute inset-0 bg-[var(--surface-container)]"
    >
      <ReactFlow<ServiceFlowNode, ServiceFlowEdge>
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={onNodesChange}
        onNodeClick={selectNode}
        onEdgeClick={(_, edge) => {
          setSelectedConnectionId(edge.id)
          setSelectedServiceId("")
        }}
        onPaneClick={() => {
          setSelectedServiceId("")
          setSelectedConnectionId("")
        }}
        onNodeDragStop={(_, node) =>
          void run(() =>
            projectService.updateServicePosition(
              projectId,
              server.id,
              node.id,
              JSON.stringify(node.position),
            ),
          )
        }
        fitViewOptions={{ padding: 0.18 }}
        minZoom={0.25}
        maxZoom={2}
        nodesConnectable={false}
        deleteKeyCode={null}
      >
        <Background
          variant={BackgroundVariant.Dots}
          gap={20}
          size={1}
          color="var(--dot)"
          bgColor="transparent"
        />
        <Controls position="bottom-left" showInteractive={false} />
      </ReactFlow>

      <div className="absolute left-3 top-3 z-10 flex items-center gap-2">
        <Button
          type="button"
          variant="ghost"
          disabled={busy || nodes.length === 0}
          onClick={() => void autoLayout()}
          className="rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container-high)] shadow-sm"
          title="Automatically rearrange with ELK"
        >
          {busy ? (
            <LoaderCircle className="size-3.5 animate-spin" />
          ) : (
            <AlignHorizontalDistributeCenter className="size-3.5" />
          )}
          Rearrange
        </Button>
      </div>

      {loading && (
        <div className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center bg-[var(--surface-container)]/70 text-xs text-[var(--on-surface-variant)]">
          <LoaderCircle className="mr-2 size-4 animate-spin" /> Loading topology
        </div>
      )}

      {!loading && services.length === 0 && (
        <div className="pointer-events-none absolute inset-0 z-[5] flex flex-col items-center justify-center px-6 text-center">
          <Network className="size-6 text-[var(--primary)]" />
          <p className="mt-3 text-sm font-semibold">No topology declared</p>
          <p className="mt-1 max-w-md text-xs leading-5 text-[var(--on-surface-variant)]">
            Claude Code or Codex can register services and connections using
            Seizen's tools.
          </p>
        </div>
      )}

      {error && (
        <p
          role="alert"
          className="status-toast absolute bottom-3 left-1/2 z-20 flex max-w-[70%] -translate-x-1/2 items-center gap-2 rounded-full bg-[var(--error-container)] px-3 py-2 text-xs text-[var(--on-error-container)] shadow-[0_8px_24px_var(--shadow-elevated)]"
        >
          <CircleAlert className="size-4 shrink-0" /> {error}
        </p>
      )}

      {(selectedService || selectedConnection) && (
        <TopologyDetails
          server={server}
          service={selectedService}
          connection={selectedConnection}
          services={services}
          busy={busy}
          onClose={() => {
            setSelectedServiceId("")
            setSelectedConnectionId("")
          }}
          onVerifyService={(id) =>
            run(() => projectService.verifyServerService(projectId, server.id, id))
          }
          onHealthcheck={(id) =>
            run(() =>
              projectService.runServerServiceHealthcheck(projectId, server.id, id),
            )
          }
          onVerifyConnection={(id) =>
            run(() =>
              projectService.verifyServerConnection(projectId, server.id, id),
            )
          }
          onOpenTerminal={onOpenTerminal}
          onOpenLogs={onOpenLogs}
        />
      )}
    </div>
  )
}

function TopologyDetails({
  server,
  service,
  connection,
  services,
  busy,
  onClose,
  onVerifyService,
  onHealthcheck,
  onVerifyConnection,
  onOpenTerminal,
  onOpenLogs,
}: {
  server: ProjectServer
  service?: ServerService
  connection?: ServerConnection
  services: ServerService[]
  busy: boolean
  onClose: () => void
  onVerifyService: (id: string) => Promise<void>
  onHealthcheck: (id: string) => Promise<void>
  onVerifyConnection: (id: string) => Promise<void>
  onOpenTerminal: (service?: ServerService) => void
  onOpenLogs: () => void
}) {
  const canCheck = ["running", "degraded"].includes(server.status)
  const sourceName = services.find(
    ({ id }) => id === connection?.sourceServiceId,
  )?.name
  const targetName = services.find(
    ({ id }) => id === connection?.targetServiceId,
  )?.name
  const metadata = service ? parseServiceMetadata(service.metadataJson) : {}

  return (
    <aside
      aria-label="Topology details"
      className="panel-in absolute inset-y-3 right-3 z-20 w-[min(20rem,calc(100%-1.5rem))] overflow-auto rounded-2xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-4 shadow-[0_16px_42px_var(--shadow-elevated)]"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold">
            {service?.name ?? `${sourceName ?? "Source"} → ${targetName ?? "Target"}`}
          </p>
          <p className="mt-1 text-[0.65rem] text-[var(--on-surface-variant)]">
            {service ? `${service.kind} · ${service.source}` : `${connection?.protocol} · ${connection?.source}`}
          </p>
        </div>
        <button
          type="button"
          onClick={onClose}
          aria-label="Close details"
          className="rounded-full p-1.5 text-[var(--on-surface-variant)] hover:bg-[var(--state-layer)]"
        >
          <X className="size-4" />
        </button>
      </div>

      <dl className="mt-4 grid grid-cols-[auto_1fr] gap-x-3 gap-y-2 text-xs">
        <dt className="text-[var(--on-surface-variant)]">Status</dt>
        <dd className="text-right font-medium">{statusLabel(service?.status ?? connection?.status ?? "unknown")}</dd>
        {service && (
          <>
            <dt className="text-[var(--on-surface-variant)]">Host</dt>
            <dd className="break-all text-right font-mono">
              {[service.host, service.port].filter(Boolean).join(":") || "Not declared"}
            </dd>
            <dt className="text-[var(--on-surface-variant)]">Healthcheck</dt>
            <dd className="break-all text-right font-mono">
              {service.healthcheckUrl || "Not declared"}
            </dd>
            {metadata.cpuPercent !== undefined && (
              <>
                <dt className="text-[var(--on-surface-variant)]">CPU</dt>
                <dd className="text-right">{metadata.cpuPercent}%</dd>
              </>
            )}
            {metadata.memoryUsedMb !== undefined && (
              <>
                <dt className="text-[var(--on-surface-variant)]">RAM</dt>
                <dd className="text-right">{metadata.memoryUsedMb} MB</dd>
              </>
            )}
          </>
        )}
        {connection && (
          <>
            <dt className="text-[var(--on-surface-variant)]">Port</dt>
            <dd className="text-right font-mono">{connection.port ?? "Not declared"}</dd>
            <dt className="text-[var(--on-surface-variant)]">Traffic</dt>
            <dd className="text-right">{connection.trafficRate || "No traffic measured"}</dd>
            <dt className="text-[var(--on-surface-variant)]">Errors</dt>
            <dd className="text-right">{connection.errorRate}</dd>
          </>
        )}
      </dl>

      <div className="mt-5 flex flex-wrap gap-2">
        {service && (
          <>
            <Button
              type="button"
              disabled={busy || !canCheck || (!service.healthcheckUrl && !service.port)}
              onClick={() => void onVerifyService(service.id)}
              className="rounded-full"
            >
              <Activity className="size-3.5" /> Verify
            </Button>
            {service.healthcheckUrl && (
              <Button
                type="button"
                variant="ghost"
                disabled={busy || !canCheck}
                onClick={() => void onHealthcheck(service.id)}
                className="rounded-full"
              >
                <Activity className="size-3.5" /> Healthcheck
              </Button>
            )}
          </>
        )}
        {connection && (
          <Button
            type="button"
            disabled={busy || !canCheck}
            onClick={() => void onVerifyConnection(connection.id)}
            className="rounded-full"
          >
            <Activity className="size-3.5" /> Verify connection
          </Button>
        )}
        <Button
          type="button"
          variant="ghost"
          disabled={server.provider !== "wsl"}
          onClick={() => onOpenTerminal(service)}
          className="rounded-full"
          title={server.provider === "wsl" ? "Open terminal inside the server" : "Only available in WSL"}
        >
          <SquareTerminal className="size-3.5" /> {service ? "Service terminal" : "Root terminal"}
        </Button>
        <Button
          type="button"
          variant="ghost"
          onClick={onOpenLogs}
          className="rounded-full"
        >
          <FileText className="size-3.5" /> Logs
        </Button>
      </div>
    </aside>
  )
}

function belongsToServer(value: unknown, serverId: string) {
  return (
    typeof value === "object" &&
    value !== null &&
    "serverId" in value &&
    (value as { serverId?: unknown }).serverId === serverId
  )
}

function isHealthPulse(value: unknown): value is HealthPulse {
  return (
    belongsToServer(value, (value as { serverId?: unknown })?.serverId as string) &&
    typeof (value as { sequenceId?: unknown }).sequenceId === "string"
  )
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}
