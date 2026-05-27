# Diagram Sources

This directory holds the editable Mermaid sources for the canonical
architecture diagrams in `../`. Each `.mmd` file is the source of truth for
its rendered SVG.

## Policy

- Edit `.mmd` files here; do not hand-edit the SVGs in `../`.
- Regenerate the SVG in the same commit as the source change.
- Filename base must match the rendered output (e.g.
  `01-package-map.mmd` → `../01-package-map.svg`).

## Render

The project uses [`@mermaid-js/mermaid-cli`](https://github.com/mermaid-js/mermaid-cli)
(`mmdc`) for rendering. Install once:

```bash
npm install -g @mermaid-js/mermaid-cli
```

Render a single diagram:

```bash
mmdc -i src/04-durable-topology.mmd -o 04-durable-topology.svg
```

Render all diagrams from `docs/architecture/diagrams/`:

```bash
for n in 01-package-map 02-interactive-turn-sequence 03-constitutional-flow \
         04-durable-topology 05-state-surfaces 06-delivery-polymorphism; do
  mmdc -i "src/${n}.mmd" -o "${n}.svg"
done
```

If `mmdc` reports "Could not find Chrome", point Puppeteer at an existing
Chromium binary via `PUPPETEER_EXECUTABLE_PATH`, or install Chrome to the
Puppeteer cache via `npx puppeteer browsers install chrome-headless-shell`.
