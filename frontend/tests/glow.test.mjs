import assert from "node:assert/strict"
import test from "node:test"

const { hexToHsv, hsvToHex } = await import("../src/lib/glow.ts")

test("hex -> hsv -> hex round-trips", () => {
  for (const hex of ["#ff0000", "#00ff00", "#0000ff", "#818cf8", "#ff9a62", "#000000", "#ffffff", "#123456"]) {
    const [h, s, v] = hexToHsv(hex)
    assert.equal(hsvToHex(h, s, v), hex)
  }
})

test("invalid hex falls back to white", () => {
  assert.deepEqual(hexToHsv("#nope"), [0, 0, 1])
})
