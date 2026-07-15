import assert from "node:assert/strict"
import test from "node:test"

import {
  registerWorkspaceSuspender,
  stopProjectInOrder,
  suspendAllWorkspaces,
  suspendWorkspace,
} from "../src/features/projects/workspace-lifecycle.ts"

test("suspends a workspace by id without touching the rest", async () => {
  const calls = []
  const unregisterFirst = registerWorkspaceSuspender("first", async () => calls.push("first"))
  const unregisterSecond = registerWorkspaceSuspender("second", async () => calls.push("second"))

  assert.equal(await suspendWorkspace("second"), true)
  assert.deepEqual(calls, ["second"])
  assert.equal(await suspendWorkspace("missing"), false)

  unregisterFirst()
  unregisterSecond()
})

test("suspende todos los workspaces abiertos al cerrar", async () => {
  const calls = []
  const unregisterFirst = registerWorkspaceSuspender("first", async () => calls.push("first"))
  const unregisterSecond = registerWorkspaceSuspender("second", async () => calls.push("second"))

  unregisterFirst()
  assert.equal(await suspendAllWorkspaces(), true)
  assert.deepEqual(calls, ["second"])

  unregisterSecond()
  assert.equal(await suspendAllWorkspaces(), false)
})

test("no pisa el registro al re-registrar el mismo id", async () => {
  const calls = []
  registerWorkspaceSuspender("same", async () => calls.push("old"))
  const unregisterNew = registerWorkspaceSuspender("same", async () => calls.push("new"))

  assert.equal(await suspendWorkspace("same"), true)
  assert.deepEqual(calls, ["new"])
  unregisterNew()
  assert.equal(await suspendWorkspace("same"), false)
})

test("persiste, limpia runtimes y luego cierra terminales", async () => {
  const calls = []
  await stopProjectInOrder(
    async () => calls.push("persist"),
    async () => calls.push("runtime"),
    async () => calls.push("terminals"),
  )
  assert.deepEqual(calls, ["persist", "runtime", "terminals"])
})
