(() => {
  const el = (id) => document.getElementById(id);
  const tapeCanvas = el("tape");
  const tooltip = el("tooltip");
  const toggle = el("toggle");
  const refreshBtn = el("refresh");
  const windowSel = el("window");
  const filterInput = el("filter");
  const statusEl = el("status");
  const countersEl = el("counters");
  const treeEl = el("tree");
  const dirEl = el("dir");

  const ctx = tapeCanvas.getContext("2d");

  const state = {
    events: [],
    maxEvents: 50000,
    running: true,
    windowSec: 30,
    filter: "",
    rootDir: "",
    // Map<relPath, {li, childUl, expanded, isDir}>; "" is the root.
    nodes: new Map(),
    pendingRefresh: new Set(),
    refreshTimer: null,
    counters: { recv: 0, drawn: 0, unmapped: 0 },
  };

  // ---------- WebSocket ----------

  function connectWS() {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${proto}//${location.host}/ws`);
    ws.onopen = () => { statusEl.textContent = "connected"; };
    ws.onclose = () => {
      statusEl.textContent = "disconnected — retrying";
      setTimeout(connectWS, 1000);
    };
    ws.onerror = () => { statusEl.textContent = "error"; };
    ws.onmessage = (ev) => {
      try {
        const e = JSON.parse(ev.data);
        e._t = new Date(e.ts).getTime();
        state.events.push(e);
        state.counters.recv++;
        if (state.events.length > state.maxEvents) {
          state.events.splice(0, state.events.length - state.maxEvents);
        }
        flashTreeNode(e.path);
        if (mutates(e.op)) scheduleParentRefresh(e.path);
      } catch {}
    };
  }

  function mutates(op) {
    return op === "write" || op === "rename" || op === "unlink" || op === "mkdir" || op === "rmdir";
  }

  // ---------- Canvas ----------

  function resizeCanvas() {
    const dpr = window.devicePixelRatio || 1;
    const r = tapeCanvas.getBoundingClientRect();
    tapeCanvas.width = Math.floor(r.width * dpr);
    tapeCanvas.height = Math.floor(r.height * dpr);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  }
  window.addEventListener("resize", resizeCanvas);

  function colorFor(op) {
    if (op === "read") return "#4aa3ff";
    if (op === "write") return "#ff5c5c";
    return "#888";
  }

  let lastDraw = [];

  // Build a Map<rel, y> for visible tree rows in canvas-local coords.
  // Read-only DOM access (no layout invalidation) batched per frame.
  function visibleRowYMap(canvasRect) {
    const map = new Map();
    for (const [rel, node] of state.nodes) {
      if (!node.li) continue;
      if (node.li.offsetParent === null) continue;
      const row = node.li.querySelector(":scope > .row");
      if (!row) continue;
      const rr = row.getBoundingClientRect();
      const y = rr.top + rr.height / 2 - canvasRect.top;
      if (y < -20 || y > canvasRect.height + 20) continue;
      map.set(rel, y);
    }
    return map;
  }

  // Walk path components from leaf to root looking for a row that's
  // currently visible (rendered + ancestors expanded). If nothing
  // matches (e.g. file lives in a collapsed subfolder, or path is the
  // watch root itself which has no LI), fall back to a fixed "unmapped"
  // zone at the top of the canvas so the user still sees activity.
  function yForPath(rel, rowYMap) {
    if (rel === null) return null;
    if (rowYMap.has(rel)) return rowYMap.get(rel);
    let cur = rel;
    while (cur !== "") {
      const i = cur.lastIndexOf("/");
      cur = i < 0 ? "" : cur.slice(0, i);
      if (rowYMap.has(cur)) return rowYMap.get(cur);
    }
    return 6; // unmapped — drawn just under the top edge
  }

  function draw() {
    const r = tapeCanvas.getBoundingClientRect();
    const w = r.width, h = r.height;
    ctx.fillStyle = "#0d1117";
    ctx.fillRect(0, 0, w, h);

    // Time axis labels along the bottom.
    ctx.fillStyle = "#444c56";
    ctx.font = "10px ui-monospace, monospace";
    ctx.textBaseline = "bottom";
    const ticks = 6;
    for (let i = 0; i <= ticks; i++) {
      const x = (i / ticks) * w;
      const sec = -Math.round(((ticks - i) / ticks) * state.windowSec);
      ctx.fillText(sec === 0 ? "now" : `${sec}s`, x + 2, h - 2);
    }

    const now = Date.now();
    const windowMs = state.windowSec * 1000;
    const minT = now - windowMs;
    const filter = state.filter;
    const rowYMap = visibleRowYMap(r);

    lastDraw = [];
    const tickH = 14;
    let drawn = 0;
    let unmapped = 0;

    for (const e of state.events) {
      if (e._t < minT) continue;
      if (filter && !e.path.includes(filter)) continue;
      const rel = relFromFull(e.path);
      const y = yForPath(rel, rowYMap);
      if (y === null) continue;
      if (rel !== null && !rowYMap.has(rel)) unmapped++;
      const x = w - ((now - e._t) / windowMs) * w;
      ctx.strokeStyle = colorFor(e.op);
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.moveTo(x, y - tickH / 2);
      ctx.lineTo(x, y + tickH / 2);
      ctx.stroke();
      lastDraw.push({ x, y, e, tickH });
      drawn++;
    }
    state.counters.drawn = drawn;
    state.counters.unmapped = unmapped;
    countersEl.textContent = `recv=${state.counters.recv} drawn=${drawn} unmapped=${unmapped}`;
  }

  function loop() {
    if (state.running) draw();
    requestAnimationFrame(loop);
  }

  // ---------- Tooltip ----------

  tapeCanvas.addEventListener("mousemove", (ev) => {
    const r = tapeCanvas.getBoundingClientRect();
    const mx = ev.clientX - r.left;
    const my = ev.clientY - r.top;
    let best = null;
    let bestD = 6;
    for (const d of lastDraw) {
      const dx = Math.abs(d.x - mx);
      if (dx > bestD) continue;
      if (Math.abs(d.y - my) > d.tickH / 2 + 4) continue;
      if (dx < bestD) { bestD = dx; best = d; }
    }
    if (!best) {
      tooltip.classList.add("hidden");
      return;
    }
    tooltip.classList.remove("hidden");
    const ts = new Date(best.e._t).toISOString().slice(11, 23);
    tooltip.textContent = `${ts}  ${best.e.op.padEnd(6)} ${best.e.process}.${best.e.pid}  ${best.e.path}`;
    tooltip.style.left = `${mx + 12}px`;
    tooltip.style.top = `${my + 12}px`;
  });
  tapeCanvas.addEventListener("mouseleave", () => tooltip.classList.add("hidden"));

  // ---------- Controls ----------

  toggle.addEventListener("click", () => {
    state.running = !state.running;
    toggle.textContent = state.running ? "Stop" : "Start";
    toggle.classList.toggle("stopped", !state.running);
  });
  windowSel.addEventListener("change", () => {
    state.windowSec = parseInt(windowSel.value, 10);
  });
  filterInput.addEventListener("input", () => {
    state.filter = filterInput.value.trim();
  });
  refreshBtn.addEventListener("click", () => {
    refreshAllExpanded();
  });

  // ---------- File tree ----------

  async function fetchTree(rel) {
    const res = await fetch(`/api/tree?path=${encodeURIComponent(rel || "")}`);
    if (!res.ok) throw new Error(await res.text());
    return res.json();
  }

  function relFromFull(fullPath) {
    if (!state.rootDir) return null;
    if (fullPath === state.rootDir) return "";
    const prefix = state.rootDir + "/";
    if (!fullPath.startsWith(prefix)) return null;
    return fullPath.slice(prefix.length);
  }

  function parentRel(rel) {
    const i = rel.lastIndexOf("/");
    return i < 0 ? "" : rel.slice(0, i);
  }

  function scheduleParentRefresh(fullPath) {
    const rel = relFromFull(fullPath);
    if (rel === null) return;
    const par = parentRel(rel);
    state.pendingRefresh.add(par);
    if (state.refreshTimer) return;
    state.refreshTimer = setTimeout(() => {
      const dirs = Array.from(state.pendingRefresh);
      state.pendingRefresh.clear();
      state.refreshTimer = null;
      for (const d of dirs) refreshDir(d);
    }, 300);
  }

  async function refreshDir(rel) {
    const node = state.nodes.get(rel);
    if (!node || !node.expanded || !node.childUl) return;
    let entries;
    try {
      entries = await fetchTree(rel);
    } catch {
      return;
    }
    reconcileChildren(rel, node.childUl, entries);
  }

  async function refreshAllExpanded() {
    const expanded = ["", ...Array.from(state.nodes.keys()).filter((k) => state.nodes.get(k).expanded)];
    for (const rel of expanded) {
      if (rel === "") {
        try {
          const entries = await fetchTree("");
          const rootUl = treeEl.querySelector(":scope > ul");
          if (rootUl) reconcileChildren("", rootUl, entries);
        } catch {}
      } else {
        await refreshDir(rel);
      }
    }
  }

  function reconcileChildren(parentRelPath, ulEl, entries) {
    const want = new Map(entries.map((e) => [e.name, e]));
    const have = new Map();
    for (const li of Array.from(ulEl.children)) {
      if (li.tagName === "LI") have.set(li.dataset.name, li);
    }

    // Remove entries that no longer exist.
    for (const [name, li] of have) {
      if (!want.has(name)) {
        const rel = parentRelPath ? `${parentRelPath}/${name}` : name;
        purgeNode(rel);
        li.remove();
      }
    }

    // Build a name → desired-position map and a sorted list of desired names.
    const desired = entries.map((e) => e.name);

    // Add new entries; existing ones stay in place but may need reordering.
    let lastInserted = null;
    for (const name of desired) {
      let li = have.get(name);
      if (!li) {
        const ent = want.get(name);
        li = makeNode(ent, parentRelPath);
        if (lastInserted) {
          lastInserted.after(li);
        } else if (ulEl.firstChild) {
          ulEl.insertBefore(li, ulEl.firstChild);
        } else {
          ulEl.appendChild(li);
        }
      } else {
        // Move into the right position if needed.
        if (lastInserted) {
          if (lastInserted.nextSibling !== li) lastInserted.after(li);
        } else {
          if (ulEl.firstChild !== li) ulEl.insertBefore(li, ulEl.firstChild);
        }
      }
      lastInserted = li;
    }
  }

  function purgeNode(rel) {
    const node = state.nodes.get(rel);
    if (!node) return;
    state.nodes.delete(rel);
    if (node.childUl) {
      // Recursively purge children from the registry.
      for (const li of Array.from(node.childUl.children)) {
        if (li.tagName === "LI" && li.dataset.rel) purgeNode(li.dataset.rel);
      }
    }
  }

  function makeNode(ent, parentRelPath) {
    const rel = parentRelPath ? `${parentRelPath}/${ent.name}` : ent.name;
    const li = document.createElement("li");
    li.dataset.rel = rel;
    li.dataset.name = ent.name;
    li.dataset.full = state.rootDir ? `${state.rootDir}/${rel}` : rel;
    li.classList.add(ent.isDir ? "dir" : "file");
    if (ent.name.startsWith(".")) li.classList.add("hidden-entry");

    const row = document.createElement("div");
    row.className = "row";
    const tri = document.createElement("span");
    tri.className = "triangle";
    tri.textContent = ent.isDir ? "▸" : "";
    const name = document.createElement("span");
    name.className = "name";
    name.textContent = ent.name;
    row.appendChild(tri);
    row.appendChild(name);
    li.appendChild(row);

    const node = { li, childUl: null, expanded: false, isDir: ent.isDir };
    state.nodes.set(rel, node);

    if (ent.isDir) {
      row.addEventListener("click", async (ev) => {
        ev.stopPropagation();
        await toggleDir(rel, node, tri);
      });
    } else {
      row.addEventListener("click", (ev) => {
        ev.stopPropagation();
        document.querySelectorAll("#tree .row.selected").forEach((n) => n.classList.remove("selected"));
        row.classList.add("selected");
        filterInput.value = li.dataset.full;
        state.filter = li.dataset.full;
      });
    }
    return li;
  }

  async function toggleDir(rel, node, tri) {
    node.expanded = !node.expanded;
    tri.textContent = node.expanded ? "▾" : "▸";
    if (node.expanded) {
      if (!node.childUl) {
        node.childUl = document.createElement("ul");
        node.li.appendChild(node.childUl);
        try {
          const kids = await fetchTree(rel);
          for (const k of kids) node.childUl.appendChild(makeNode(k, rel));
        } catch (err) {
          node.childUl.textContent = "error: " + err.message;
        }
      } else {
        node.childUl.style.display = "";
      }
    } else if (node.childUl) {
      node.childUl.style.display = "none";
    }
  }

  function renderTreeRoot() {
    treeEl.innerHTML = "";
    const ul = document.createElement("ul");
    treeEl.appendChild(ul);
    // Register a synthetic root node so refreshAllExpanded can find it.
    state.nodes.set("", { li: null, childUl: ul, expanded: true, isDir: true });
    fetchTree("").then((entries) => {
      for (const ent of entries) ul.appendChild(makeNode(ent, ""));
    }).catch((err) => {
      treeEl.textContent = "tree error: " + err.message;
    });
  }

  function flashTreeNode(fullPath) {
    if (!state.rootDir || !fullPath.startsWith(state.rootDir + "/")) return;
    const sel = `li[data-full="${cssEscape(fullPath)}"] > .row`;
    const row = document.querySelector(sel);
    if (!row) return;
    row.classList.remove("flash");
    void row.offsetWidth;
    row.classList.add("flash");
  }

  function cssEscape(s) {
    return s.replace(/(["\\])/g, "\\$1");
  }

  // ---------- Init ----------

  fetch("/api/info").then((r) => r.json()).then((info) => {
    state.rootDir = info.root;
    dirEl.textContent = info.root;
    // Stamp data-full on already-rendered root entries now that we know root.
    for (const [rel, node] of state.nodes) {
      if (rel && node.li) node.li.dataset.full = `${info.root}/${rel}`;
    }
  }).catch(() => {});

  resizeCanvas();
  connectWS();
  renderTreeRoot();
  requestAnimationFrame(loop);
})();
