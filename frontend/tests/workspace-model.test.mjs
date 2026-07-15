import assert from "node:assert/strict"
import test from "node:test"

import {
  fitWorkspaceViewport,
  normalizeAppPreviewURL,
  normalizeBrowserURL,
  normalizeEditorURL,
  parseWorkspace,
  serializeWorkspace,
  terminalCommandInput,
} from "../src/features/projects/workspace-model.ts"

test("fits all content inside the canvas", () => {
  assert.deepEqual(
    fitWorkspaceViewport(1000, 700, [
      { x: 100, y: 50, width: 400, height: 200 },
    ]),
    { x: -100, y: 50, zoom: 2 },
  )
  assert.deepEqual(fitWorkspaceViewport(1000, 700, []), {
    x: 80,
    y: 88,
    zoom: 1,
  })
})

test("persists layout without session data", () => {
  const json = serializeWorkspace(
    { x: 10, y: 20, zoom: 1 },
    [
      {
        id: "terminal-1",
        type: "terminal",
        shell: "cmd",
        x: 1,
        y: 2,
        width: 500,
        height: 300,
        z: 1,
        sessionId: "private",
        output: "private",
      },
      {
        id: "terminal-codex",
        type: "terminal",
        shell: "codex",
        x: 2,
        y: 3,
        width: 500,
        height: 300,
        z: 2,
      },
      {
        id: "terminal-claude",
        type: "terminal",
        shell: "claude",
        x: 3,
        y: 4,
        width: 500,
        height: 300,
        z: 3,
      },
      {
        id: "editor-vscode",
        type: "editor",
        editorId: "vscode",
        x: 4,
        y: 5,
        width: 680,
        height: 430,
        z: 4,
        sessionId: "editor-private",
        url: `http://127.0.0.1:43123/seizen/${"s".repeat(43)}/`,
        status: "running",
      },
      {
        id: "editor-cursor",
        type: "editor",
        editorId: "cursor",
        x: 5,
        y: 6,
        width: 680,
        height: 430,
        z: 5,
      },
    ],
  )

  assert.equal(json.includes("sessionId"), false)
  assert.equal(json.includes("output"), false)
  assert.equal(json.includes('"status"'), false)
  assert.equal(json.includes("secret"), false)
  assert.deepEqual(
    parseWorkspace(json).nodes.map((node) =>
      node.type === "terminal" ? node.shell : node.type,
    ),
    ["cmd", "codex", "claude", "editor"],
  )
})

test("keeps v1 layouts and persists the Spotify player", () => {
  const legacy = JSON.stringify({
    version: 1,
    viewport: { x: 12, y: 24, zoom: 1 },
    nodes: [
      {
        id: "browser-legacy",
        type: "browser",
        url: "https://example.com/",
        x: 20,
        y: 30,
        width: 600,
        height: 400,
        z: 1,
      },
    ],
  })
  assert.equal(parseWorkspace(legacy).nodes[0]?.type, "browser")

  const serialized = serializeWorkspace(
    { x: 12, y: 24, zoom: 1 },
    [
      ...parseWorkspace(legacy).nodes,
      {
        id: "player-1",
        type: "player",
        x: 60,
        y: 70,
        width: 480,
        height: 330,
        z: 2,
      },
    ],
  )
  const restored = parseWorkspace(serialized).nodes
  assert.deepEqual(restored.map(({ type }) => type), ["browser", "player"])
  assert.deepEqual(restored[1], {
    id: "player-1",
    type: "player",
    x: 60,
    y: 70,
    width: 420,
    height: 190,
    z: 2,
  })
})

test("persists photos by managed asset and never stores their data", () => {
  const assetId = "12345678-1234-4123-8123-123456789abc"
  const serialized = serializeWorkspace(
    { x: 10, y: 20, zoom: 1 },
    [{
      id: "photo-1",
      type: "photo",
      assetId,
      dataURL: "data:image/png;base64,secret",
      status: "ready",
      x: 30,
      y: 40,
      width: 520,
      height: 360,
      z: 2,
    }],
  )

  assert.equal(serialized.includes("data:image"), false)
  assert.equal(serialized.includes('"status"'), false)
  assert.deepEqual(parseWorkspace(serialized).nodes, [{
    id: "photo-1",
    type: "photo",
    assetId,
    x: 30,
    y: 40,
    width: 520,
    height: 360,
    z: 2,
  }])

  for (const unsafe of ["../photo", "C:\\photo.png", "1234", assetId.toUpperCase()]) {
    const parsed = parseWorkspace(JSON.stringify({
      version: 1,
      nodes: [{
        id: "photo-unsafe",
        type: "photo",
        assetId: unsafe,
        x: 0,
        y: 0,
        width: 520,
        height: 360,
        z: 1,
      }],
    }))
    assert.deepEqual(parsed.nodes, [])
  }
})

test("the editor only accepts the local gateway with secret prefix", () => {
  const valid = `http://127.0.0.1:43123/seizen/${"aZ0_-".repeat(8)}abc/`
  assert.equal(
    normalizeEditorURL(valid),
    valid,
  )
  for (const unsafe of [
    valid.replace("http:", "https:"),
    valid.replace("127.0.0.1", "localhost"),
    valid.replace("43123", "0"),
    valid.replace("43123", "65536"),
    valid.replace("/seizen/", "/"),
    valid.replace(/.$/, ""),
    `${valid}?tkn=secret`,
    `${valid}#fragment`,
    valid.replace("127.0.0.1", "user:pass@127.0.0.1"),
    valid.replace("abc/", "ab/"),
    valid.replace("abc/", "ab%2Fc/"),
  ]) {
    assert.throws(() => normalizeEditorURL(unsafe))
  }
})

test("distinguishes searches, addresses, and internal origins", () => {
  assert.equal(normalizeBrowserURL("localhost:3000"), "http://localhost:3000/")
  assert.equal(normalizeBrowserURL("react.dev"), "https://react.dev/")
  assert.equal(
    normalizeBrowserURL("react components"),
    "https://www.google.com/search?igu=1&q=react+components",
  )
  assert.equal(normalizeBrowserURL(""), "https://www.google.com/search?igu=1")
  assert.throws(() => normalizeBrowserURL("file:///C:/secret.txt"))
  assert.throws(() => normalizeBrowserURL("http://wails.localhost"))
  assert.throws(() => normalizeBrowserURL("https://user:pass@example.com"))
})

test("the App only accepts ports or HTTP/HTTPS previews, never searches", () => {
  assert.equal(normalizeAppPreviewURL("3000"), "http://localhost:3000/")
  assert.equal(normalizeAppPreviewURL("localhost:4173"), "http://localhost:4173/")
  assert.equal(normalizeAppPreviewURL("https://example.com/app"), "https://example.com/app")
  assert.equal(normalizeAppPreviewURL(""), "")
  assert.throws(() => normalizeAppPreviewURL("run my application"))
  assert.throws(() => normalizeAppPreviewURL("0"))
  assert.throws(() => normalizeAppPreviewURL("http://wails.localhost"))
  assert.throws(() => normalizeAppPreviewURL("https://user:pass@example.com"))
})

test("uses the correct line break for each shell", () => {
  assert.equal(terminalCommandInput("cmd", "dir"), "dir\r\n")
  assert.equal(terminalCommandInput("wsl", "pwd"), "pwd\n")
  assert.equal(terminalCommandInput("codex", "status"), "status\r\n")
  assert.equal(terminalCommandInput("claude", "status"), "status\r\n")
  assert.equal(terminalCommandInput("opencode", "status"), "status\r\n")
})
