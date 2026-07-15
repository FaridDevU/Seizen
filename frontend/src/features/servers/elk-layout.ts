import ELK from "elkjs/lib/elk.bundled.js"

const elk = new ELK()

type Positioned = {
  id: string
  position: { x: number; y: number }
}

type Linked = {
  id: string
  source: string
  target: string
}

export async function layoutServerGraph<T extends Positioned>(
  nodes: readonly T[],
  edges: readonly Linked[],
): Promise<T[]> {
  if (nodes.length === 0) return []

  const graph = await elk.layout({
    id: "server",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": "RIGHT",
      "elk.spacing.nodeNode": "56",
      "elk.layered.spacing.nodeNodeBetweenLayers": "96",
      "elk.padding": "[top=48,left=48,bottom=48,right=48]",
    },
    children: nodes.map((node) => ({
      id: node.id,
      width: 232,
      height: 126,
    })),
    edges: edges.map((edge) => ({
      id: edge.id,
      sources: [edge.source],
      targets: [edge.target],
    })),
  })

  const positions = new Map(
    graph.children?.map((node) => [
      node.id,
      { x: node.x ?? 0, y: node.y ?? 0 },
    ]),
  )
  return nodes.map((node) => ({
    ...node,
    position: positions.get(node.id) ?? node.position,
  }))
}
