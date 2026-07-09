package pkgflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RenderHTML renders graph as a self-contained interactive HTML document.
func RenderHTML(graph Graph) (string, error) {
	payload, err := json.Marshal(graph)
	if err != nil {
		return "", fmt.Errorf("marshal graph: %w", err)
	}

	return strings.Replace(htmlTemplate, "__PKGFLOW_GRAPH_JSON__", string(payload), 1), nil
}

const htmlTemplate = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Package Flow</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f8fafc;
      --panel: #ffffff;
      --text: #172033;
      --muted: #5b6474;
      --line: #d6dce8;
      --cmd: #2563eb;
      --internal: #16a34a;
      --root: #ca8a04;
      --external: #64748b;
      --selected: #e11d48;
    }
    * {
      box-sizing: border-box;
    }
    body {
      margin: 0;
      min-height: 100vh;
      background: var(--bg);
      color: var(--text);
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    main {
      display: grid;
      grid-template-columns: minmax(0, 1fr) 320px;
      min-height: 100vh;
    }
    .workspace {
      min-width: 0;
      padding: 24px;
    }
    .toolbar {
      display: flex;
      align-items: center;
      gap: 12px;
      margin-bottom: 16px;
    }
    h1 {
      margin: 0;
      font-size: 24px;
      line-height: 1.2;
      font-weight: 700;
    }
    .module {
      margin: 4px 0 0;
      color: var(--muted);
      font-size: 13px;
    }
    .toolbar-spacer {
      flex: 1;
    }
    input {
      width: min(360px, 38vw);
      height: 38px;
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 0 12px;
      font: inherit;
      background: #ffffff;
      color: var(--text);
    }
    button {
      height: 38px;
      border: 1px solid var(--line);
      border-radius: 6px;
      background: #ffffff;
      color: var(--text);
      padding: 0 12px;
      font: inherit;
      cursor: pointer;
    }
    button:hover {
      border-color: #94a3b8;
    }
    .graph-shell {
      height: calc(100vh - 104px);
      min-height: 560px;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #ffffff;
      overflow: auto;
    }
    svg {
      display: block;
      min-width: 100%;
      min-height: 100%;
    }
    .lane-label {
      fill: var(--muted);
      font-size: 12px;
      font-weight: 700;
      letter-spacing: 0;
      text-transform: uppercase;
    }
    .edge {
      fill: none;
      stroke: #94a3b8;
      stroke-width: 1.6;
      opacity: 0.48;
    }
    .edge.dim {
      opacity: 0.08;
    }
    .edge.active {
      stroke: var(--selected);
      stroke-width: 2.4;
      opacity: 0.95;
    }
    .node {
      cursor: pointer;
    }
    .node rect {
      fill: #ffffff;
      stroke: var(--line);
      stroke-width: 1.2;
      rx: 7;
    }
    .node.cmd rect {
      stroke: var(--cmd);
    }
    .node.internal rect {
      stroke: var(--internal);
    }
    .node.root rect {
      stroke: var(--root);
    }
    .node.external rect {
      stroke: var(--external);
    }
    .node text {
      fill: var(--text);
      font-size: 13px;
      font-weight: 650;
      pointer-events: none;
    }
    .node .subtext {
      fill: var(--muted);
      font-size: 11px;
      font-weight: 500;
    }
    .node.dim {
      opacity: 0.18;
    }
    .node.active rect {
      stroke: var(--selected);
      stroke-width: 2.4;
      fill: #fff1f2;
    }
    .node.search-hit rect {
      fill: #fef9c3;
    }
    aside {
      border-left: 1px solid var(--line);
      background: var(--panel);
      padding: 24px;
      min-width: 0;
    }
    .summary {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 8px;
      margin: 16px 0 24px;
    }
    .metric {
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 12px;
    }
    .metric strong {
      display: block;
      font-size: 24px;
      line-height: 1;
    }
    .metric span {
      color: var(--muted);
      font-size: 12px;
    }
    .section-title {
      margin: 20px 0 8px;
      color: var(--muted);
      font-size: 12px;
      font-weight: 700;
      letter-spacing: 0;
      text-transform: uppercase;
    }
    .details-path {
      overflow-wrap: anywhere;
      font-size: 13px;
      line-height: 1.45;
    }
    .list {
      display: grid;
      gap: 6px;
      margin: 0;
      padding: 0;
      list-style: none;
      font-size: 13px;
      color: var(--text);
    }
    .list li {
      overflow-wrap: anywhere;
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 8px;
      background: #f8fafc;
    }
    @media (max-width: 900px) {
      main {
        grid-template-columns: 1fr;
      }
      aside {
        border-left: 0;
        border-top: 1px solid var(--line);
      }
      .toolbar {
        align-items: flex-start;
        flex-direction: column;
      }
      input {
        width: 100%;
      }
      .toolbar-spacer {
        display: none;
      }
      .graph-shell {
        height: 60vh;
      }
    }
  </style>
</head>
<body>
  <main>
    <section class="workspace" aria-label="Package graph workspace">
      <div class="toolbar">
        <div>
          <h1>Package Flow</h1>
          <p class="module" id="module"></p>
        </div>
        <div class="toolbar-spacer"></div>
        <input id="search" type="search" placeholder="Search packages" autocomplete="off">
        <button id="reset" type="button">Reset</button>
      </div>
      <div class="graph-shell">
        <svg id="graph" role="img" aria-label="Go package import graph"></svg>
      </div>
    </section>
    <aside>
      <h2 id="details-title">Overview</h2>
      <p class="details-path" id="details">Select a package to inspect direct imports and importers.</p>
      <div class="summary">
        <div class="metric"><strong id="node-count">0</strong><span>Packages</span></div>
        <div class="metric"><strong id="edge-count">0</strong><span>Imports</span></div>
      </div>
      <div class="section-title">Imports</div>
      <ul class="list" id="imports"></ul>
      <div class="section-title">Imported By</div>
      <ul class="list" id="importers"></ul>
    </aside>
  </main>
  <script>
    const graphData = __PKGFLOW_GRAPH_JSON__;

    const state = {
      selected: null,
      query: ""
    };

    const groupOrder = ["root", "cmd", "internal", "external"];
    const colors = {
      root: "#ca8a04",
      cmd: "#2563eb",
      internal: "#16a34a",
      external: "#64748b"
    };

    const svg = document.getElementById("graph");
    const search = document.getElementById("search");
    const reset = document.getElementById("reset");
    const importsList = document.getElementById("imports");
    const importersList = document.getElementById("importers");
    const details = document.getElementById("details");
    const detailsTitle = document.getElementById("details-title");

    document.getElementById("module").textContent = graphData.modulePath || "Go module";
    document.getElementById("node-count").textContent = graphData.nodes.length.toString();
    document.getElementById("edge-count").textContent = graphData.edges.length.toString();

    const nodesByID = new Map(graphData.nodes.map((node) => [node.id, node]));
    const importsByID = new Map();
    const importersByID = new Map();
    for (const node of graphData.nodes) {
      importsByID.set(node.id, []);
      importersByID.set(node.id, []);
    }
    for (const edge of graphData.edges) {
      if (importsByID.has(edge.from)) {
        importsByID.get(edge.from).push(edge.to);
      }
      if (importersByID.has(edge.to)) {
        importersByID.get(edge.to).push(edge.from);
      }
    }

    function layout() {
      const groups = groupOrder
        .filter((group) => graphData.nodes.some((node) => node.group === group))
        .map((group) => ({
          name: group,
          nodes: graphData.nodes.filter((node) => node.group === group)
        }));

      const laneWidth = 270;
      const nodeWidth = 210;
      const nodeHeight = 54;
      const rowGap = 26;
      const top = 72;
      const left = 42;
      const maxRows = Math.max(1, ...groups.map((group) => group.nodes.length));
      const width = Math.max(920, left * 2 + groups.length * laneWidth);
      const height = Math.max(560, top + maxRows * (nodeHeight + rowGap) + 48);
      svg.setAttribute("viewBox", "0 0 " + width + " " + height);
      svg.setAttribute("width", String(width));
      svg.setAttribute("height", String(height));

      const positions = new Map();
      groups.forEach((group, laneIndex) => {
        const x = left + laneIndex * laneWidth;
        addText(group.name.toUpperCase(), x, 32, "lane-label");
        group.nodes.forEach((node, rowIndex) => {
          positions.set(node.id, {
            x,
            y: top + rowIndex * (nodeHeight + rowGap),
            width: nodeWidth,
            height: nodeHeight
          });
        });
      });
      return positions;
    }

    function render() {
      svg.replaceChildren();
      const positions = layout();
      const activeIDs = relatedIDs(state.selected);
      const query = state.query.trim().toLowerCase();

      for (const edge of graphData.edges) {
        const from = positions.get(edge.from);
        const to = positions.get(edge.to);
        if (!from || !to) continue;
        const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
        const x1 = from.x + from.width;
        const y1 = from.y + from.height / 2;
        const x2 = to.x;
        const y2 = to.y + to.height / 2;
        const curve = Math.max(48, Math.abs(x2 - x1) * 0.42);
        path.setAttribute("d", "M " + x1 + " " + y1 + " C " + (x1 + curve) + " " + y1 + ", " + (x2 - curve) + " " + y2 + ", " + x2 + " " + y2);
        path.classList.add("edge");
        if (state.selected) {
          if (edge.from === state.selected || edge.to === state.selected) {
            path.classList.add("active");
          } else {
            path.classList.add("dim");
          }
        }
        svg.appendChild(path);
      }

      for (const node of graphData.nodes) {
        const pos = positions.get(node.id);
        if (!pos) continue;
        const matches = !query || node.label.toLowerCase().includes(query) || node.path.toLowerCase().includes(query);
        const group = document.createElementNS("http://www.w3.org/2000/svg", "g");
        group.classList.add("node", node.group || "root");
        if (state.selected && !activeIDs.has(node.id)) {
          group.classList.add("dim");
        }
        if (state.selected === node.id) {
          group.classList.add("active");
        }
        if (query && matches) {
          group.classList.add("search-hit");
        }
        group.setAttribute("transform", "translate(" + pos.x + " " + pos.y + ")");
        group.setAttribute("tabindex", "0");
        group.setAttribute("role", "button");
        group.setAttribute("aria-label", node.path);
        group.addEventListener("click", () => selectNode(node.id));
        group.addEventListener("keydown", (event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            selectNode(node.id);
          }
        });

        const rect = document.createElementNS("http://www.w3.org/2000/svg", "rect");
        rect.setAttribute("width", String(pos.width));
        rect.setAttribute("height", String(pos.height));
        group.appendChild(rect);

        const title = document.createElementNS("http://www.w3.org/2000/svg", "text");
        title.setAttribute("x", "14");
        title.setAttribute("y", "22");
        title.textContent = truncate(node.label, 24);
        group.appendChild(title);

        const subtitle = document.createElementNS("http://www.w3.org/2000/svg", "text");
        subtitle.setAttribute("x", "14");
        subtitle.setAttribute("y", "40");
        subtitle.classList.add("subtext");
        subtitle.textContent = truncate(relativePath(node.path), 30);
        group.appendChild(subtitle);

        const marker = document.createElementNS("http://www.w3.org/2000/svg", "circle");
        marker.setAttribute("cx", String(pos.width - 15));
        marker.setAttribute("cy", "17");
        marker.setAttribute("r", "5");
        marker.setAttribute("fill", colors[node.group] || colors.root);
        group.appendChild(marker);

        svg.appendChild(group);
      }

      updateDetails();
    }

    function addText(value, x, y, className) {
      const text = document.createElementNS("http://www.w3.org/2000/svg", "text");
      text.setAttribute("x", String(x));
      text.setAttribute("y", String(y));
      text.classList.add(className);
      text.textContent = value;
      svg.appendChild(text);
    }

    function selectNode(id) {
      state.selected = state.selected === id ? null : id;
      render();
    }

    function relatedIDs(id) {
      const ids = new Set();
      if (!id) return ids;
      ids.add(id);
      for (const imported of importsByID.get(id) || []) ids.add(imported);
      for (const importer of importersByID.get(id) || []) ids.add(importer);
      return ids;
    }

    function updateDetails() {
      const node = nodesByID.get(state.selected);
      if (!node) {
        detailsTitle.textContent = "Overview";
        details.textContent = "Select a package to inspect direct imports and importers.";
        renderList(importsList, []);
        renderList(importersList, []);
        return;
      }

      detailsTitle.textContent = node.label;
      details.textContent = node.path;
      renderList(importsList, importsByID.get(node.id) || []);
      renderList(importersList, importersByID.get(node.id) || []);
    }

    function renderList(list, ids) {
      list.replaceChildren();
      if (ids.length === 0) {
        const item = document.createElement("li");
        item.textContent = "None";
        list.appendChild(item);
        return;
      }
      ids.slice().sort().forEach((id) => {
        const item = document.createElement("li");
        item.textContent = relativePath(id);
        item.title = id;
        list.appendChild(item);
      });
    }

    function relativePath(id) {
      const prefix = (graphData.modulePath || "") + "/";
      if (graphData.modulePath && id === graphData.modulePath) return ".";
      return id.startsWith(prefix) ? id.slice(prefix.length) : id;
    }

    function truncate(value, max) {
      if (value.length <= max) return value;
      return value.slice(0, Math.max(0, max - 3)) + "...";
    }

    search.addEventListener("input", () => {
      state.query = search.value;
      render();
    });

    reset.addEventListener("click", () => {
      state.selected = null;
      state.query = "";
      search.value = "";
      render();
    });

    render();
  </script>
</body>
</html>
`
