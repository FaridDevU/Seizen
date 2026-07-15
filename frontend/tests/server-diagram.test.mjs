import assert from "node:assert/strict"
import test from "node:test"

import { layoutServerGraph } from "../src/features/servers/elk-layout.ts"
import {
  edgeVisualState,
  normalizeServiceKind,
  parseServiceMetadata,
  parseServicePosition,
  protocolVisual,
  serviceKinds,
} from "../src/features/servers/server-diagram-model.ts"

const connection = {
  id: "connection-1",
  serverId: "server-1",
  sourceServiceId: "frontend",
  targetServiceId: "backend",
  protocol: "https",
  port: 443,
  status: "healthy",
  source: "verified",
  trafficRate: 0,
  errorRate: 0,
  metadataJson: "{}",
}

test("modela los diez tipos y solo muestra metadatos reales", () => {
  assert.deepEqual(
    serviceKinds.map(normalizeServiceKind),
    serviceKinds,
  )
  assert.equal(normalizeServiceKind("inventado"), "external")
  assert.deepEqual(parseServicePosition('{"x":12,"y":34}'), { x: 12, y: 34 })
  assert.equal(parseServicePosition('{"x":"12","y":34}'), undefined)
  assert.deepEqual(
    parseServiceMetadata(
      '{"cpuPercent":12.5,"memoryUsedMb":256,"memoryLimitMb":1024,"fake":9}',
    ),
    { cpuPercent: 12.5, memoryUsedMb: 256, memoryLimitMb: 1024 },
  )
  assert.deepEqual(parseServiceMetadata("no es json"), {})
})

test("distinguishes protocols with a label besides the color", () => {
  assert.deepEqual(protocolVisual("https"), {
    color: "#3b82f6",
    category: "HTTP",
    icon: "HTTP",
  })
  assert.equal(protocolVisual("wss").icon, "WS")
  assert.equal(protocolVisual("postgres").icon, "DB")
  assert.equal(protocolVisual("redis").icon, "QUEUE")
  assert.equal(protocolVisual("storage").icon, "DISK")
  assert.equal(protocolVisual("grpc").icon, "INT")
})

test("the flow represents real state and never invents traffic", () => {
  assert.equal(edgeVisualState("stopped", connection).mode, "none")
  assert.equal(edgeVisualState("running", connection).mode, "healthy")
  assert.equal(
    edgeVisualState("running", { ...connection, status: "unknown" }).mode,
    "none",
  )
  assert.equal(
    edgeVisualState("running", connection, "health-1").mode,
    "healthcheck",
  )
  assert.equal(edgeVisualState("degraded", connection).mode, "degraded")

  const slow = edgeVisualState("running", {
    ...connection,
    trafficRate: 1,
  })
  const fast = edgeVisualState("running", {
    ...connection,
    trafficRate: 1_000,
  })
  assert.equal(slow.mode, "traffic")
  assert.ok(fast.durationSeconds < slow.durationSeconds)

  const failed = edgeVisualState("running", {
    ...connection,
    errorRate: 0.01,
  })
  assert.equal(failed.mode, "none")
  assert.equal(failed.color, "#ef4444")
  assert.equal(failed.icon, "ERROR")
})

test("ELK ordena el flujo sin mutar los nodos", async () => {
  const nodes = [
    { id: "a", position: { x: 0, y: 0 } },
    { id: "b", position: { x: 0, y: 0 } },
    { id: "c", position: { x: 0, y: 0 } },
  ]
  const arranged = await layoutServerGraph(nodes, [
    { id: "ab", source: "a", target: "b" },
    { id: "bc", source: "b", target: "c" },
  ])

  assert.deepEqual(nodes.map(({ position }) => position), [
    { x: 0, y: 0 },
    { x: 0, y: 0 },
    { x: 0, y: 0 },
  ])
  assert.ok(arranged[0].position.x < arranged[1].position.x)
  assert.ok(arranged[1].position.x < arranged[2].position.x)
})
