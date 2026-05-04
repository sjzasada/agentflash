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
  const treeContent = el("treeContent");
  const expandAllBtn = el("expandAll");
  const hideHiddenBtn = el("hideHidden");
  const dirEl = el("dir");
  const claudeGoalEl = el("claudeGoal");
  const claudeActionEl = el("claudeAction");
  const claudeSubEl = el("claudeSub");
  const claudeStateEl = el("claudeState");
  const claudeLogEl = el("claudeLog");
  const claudeClearBtn = el("claudeClear");
  const claudeHideBtn = el("claudeHide");
  const resizerEl = el("resizer");

  const ctx = tapeCanvas.getContext("2d");

  const CLAUDE_LOG_MAX = 500;
  const CLAUDE_PHASE_LABEL = {
    pre: "PRE",
    post: "POST",
    user_prompt: "PROMPT",
    session_start: "START",
    stop: "STOP",
    subagent_stop: "SUB-END",
    notification: "NOTIF",
  };

  const state = {
    events: [],
    maxEvents: 50000,
    running: true,
    autoPause: false,      // set from /api/info; pause timeline on Claude Stop hook
    windowSec: 30,
    filter: "",
    rootDir: "",
    // Map<relPath, {li, childUl, expanded, isDir}>; "" is the root.
    nodes: new Map(),
    pendingRefresh: new Set(),
    refreshTimer: null,
    counters: { recv: 0, drawn: 0 },
    claude: {
      goal: null,
      action: null,        // { tool, summary, ts }
      subagentType: null,
      state: "idle",       // idle | working | waiting
      events: [],          // recent claude events for the lane (op === "claude")
      maxEvents: 500,
      actionDimTimer: null,
      idleTimer: null,     // schedules transition to "waiting" after silence
      activeTools: 0,      // count of in-flight tool calls (pre - post)
    },
  };

  // ---------- WebSocket ----------

  async function onConnect() {
    let info;
    try {
      info = await fetch("/api/info").then((r) => r.json());
    } catch {
      return;
    }
    const newRoot = info.root;
    if (newRoot === state.rootDir) return;
    state.rootDir = newRoot;
    state.autoPause = !!info.autoPause;
    dirEl.textContent = newRoot;
    state.events = [];
    state.counters.recv = 0;
    state.nodes.clear();
    await renderTreeRoot();
    await expandAll();
  }

  function connectWS() {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${proto}//${location.host}/ws`);
    ws.onopen = () => { statusEl.textContent = "connected"; onConnect(); };
    ws.onclose = () => {
      statusEl.textContent = "disconnected — retrying";
      setTimeout(connectWS, 1000);
    };
    ws.onerror = () => { statusEl.textContent = "error"; };
    ws.onmessage = (ev) => {
      try {
        const e = JSON.parse(ev.data);
        if (e.op === "dirchange") {
          const rel = relFromFull(e.path);
          if (rel !== null) scheduleRefresh(rel);
          return;
        }
        if (e.op === "claude") {
          handleClaudeEvent(e);
          return;
        }
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

  // ---------- Claude events ----------

  // State transitions:
  //   user_prompt / pre / post  → "working" (green) + reset idle timer
  //   idle timer fires, no in-flight tools → "thinking" (blue): model is
  //     generating its next response; no hook fires during inference
  //   Stop hook fires            → "waiting" (amber): Claude is done,
  //     awaiting user input
  //   session_start              → "idle"
  // Long-running tools (e.g. a 30 s Bash) keep the timer rescheduling
  // so the pill stays green for the duration.
  const CLAUDE_IDLE_MS = 5000;

  function pingClaudeActive() {
    setStateLabel("working");
    armIdleTimer();
  }

  function armIdleTimer() {
    if (state.claude.idleTimer) clearTimeout(state.claude.idleTimer);
    state.claude.idleTimer = setTimeout(checkIdleOrReschedule, CLAUDE_IDLE_MS);
  }

  function checkIdleOrReschedule() {
    if (state.claude.activeTools > 0) {
      // Tool(s) still in flight — keep waiting for their PostToolUse.
      state.claude.idleTimer = setTimeout(checkIdleOrReschedule, CLAUDE_IDLE_MS);
      return;
    }
    // No tool calls in flight and no activity for CLAUDE_IDLE_MS — the
    // model is generating its next response. "waiting" is reserved for
    // when the Stop hook fires (Claude is done and awaiting the user).
    setStateLabel("thinking");
  }

  function handleClaudeEvent(e) {
    const ts = new Date(e.ts).getTime();
    const c = e.claude || {};
    const entry = { ts, ...c };
    state.claude.events.push(entry);
    if (state.claude.events.length > state.claude.maxEvents) {
      state.claude.events.splice(0, state.claude.events.length - state.claude.maxEvents);
    }
    appendClaudeLogRow(entry);
    switch (c.phase) {
      case "user_prompt":
        state.claude.goal = c.summary || c.prompt || "";
        state.claude.action = null;
        state.claude.subagentType = null;
        if (state.autoPause) setRunning(true);
        pingClaudeActive();
        break;
      case "pre":
        state.claude.activeTools++;
        state.claude.action = {
          tool: c.tool || "",
          summary: c.summary || c.tool || "",
          fullPath: c.filePath || "",
          command: c.command || "",
          prompt: c.prompt || "",
        };
        if (c.tool === "Task" && c.subagentType) {
          state.claude.subagentType = c.subagentType;
        }
        if (state.autoPause) setRunning(true);
        pingClaudeActive();
        scheduleActionDim();
        break;
      case "post":
        state.claude.activeTools = Math.max(0, state.claude.activeTools - 1);
        pingClaudeActive();
        scheduleActionDim();
        break;
      case "session_start":
        state.claude.goal = null;
        state.claude.action = null;
        state.claude.subagentType = null;
        state.claude.activeTools = 0;
        if (state.claude.idleTimer) clearTimeout(state.claude.idleTimer);
        setStateLabel("idle");
        break;
      case "stop":
        if (state.claude.idleTimer) clearTimeout(state.claude.idleTimer);
        setStateLabel("waiting");
        if (state.autoPause) setRunning(false);
        break;
      case "subagent_stop":
        state.claude.subagentType = null;
        break;
      // notification: ignored for state purposes (logged in panel).
    }
    renderClaudeChips();
  }

  function appendClaudeLogRow(entry) {
    const placeholder = claudeLogEl.querySelector(".empty");
    if (placeholder) placeholder.remove();

    const row = document.createElement("div");
    row.className = "row";
    if (entry.subagentType) row.classList.add("subagent");

    const time = new Date(entry.ts).toISOString().slice(11, 19);
    const phase = CLAUDE_PHASE_LABEL[entry.phase] || entry.phase || "";
    let toolLabel = entry.tool || "";
    if (entry.subagentType) toolLabel = "[" + entry.subagentType + "]" + (toolLabel ? " " + toolLabel : "");
    let bodyText = entry.summary || "";
    if (entry.tool === "Read" || entry.tool === "Edit" || entry.tool === "Write" || entry.tool === "NotebookEdit") {
      bodyText = entry.filePath || bodyText;
    } else if (entry.tool === "Bash") {
      bodyText = entry.command || bodyText;
    } else if (entry.phase === "user_prompt" && entry.prompt) {
      bodyText = entry.prompt;
    }

    row.innerHTML = "";
    const t = document.createElement("span"); t.className = "time"; t.textContent = time;
    const p = document.createElement("span"); p.className = "phase phase-" + entry.phase; p.textContent = phase;
    const tool = document.createElement("span"); tool.className = "tool"; tool.textContent = toolLabel;
    const body = document.createElement("span"); body.className = "body"; body.textContent = bodyText;
    row.append(t, p, tool, body);

    const tip = [phase, toolLabel, bodyText, entry.filePath, entry.prompt].filter(Boolean).join("\n");
    row.title = tip;

    claudeLogEl.prepend(row);

    while (claudeLogEl.children.length > CLAUDE_LOG_MAX) {
      claudeLogEl.removeChild(claudeLogEl.lastChild);
    }
  }

  if (claudeClearBtn) {
    claudeClearBtn.addEventListener("click", () => {
      claudeLogEl.innerHTML = '<div class="empty">no claude activity yet</div>';
    });
  }
  if (claudeHideBtn) {
    claudeHideBtn.addEventListener("click", () => {
      const collapsed = document.body.classList.toggle("claude-collapsed");
      claudeHideBtn.textContent = collapsed ? "show" : "hide";
      claudeHideBtn.title = collapsed ? "Show panel" : "Hide panel";
    });
  }
  // Initial placeholder.
  claudeLogEl.innerHTML = '<div class="empty">no claude activity yet</div>';

  function setStateLabel(s) {
    state.claude.state = s;
    claudeStateEl.classList.remove("state-idle", "state-working", "state-thinking", "state-waiting");
    claudeStateEl.classList.add("state-" + s);
    claudeStateEl.textContent = s;
  }

  function renderClaudeChips() {
    if (state.claude.goal) {
      claudeGoalEl.hidden = false;
      claudeGoalEl.querySelector("em").textContent = state.claude.goal;
      claudeGoalEl.title = state.claude.goal;
    } else {
      claudeGoalEl.hidden = true;
    }
    if (state.claude.action) {
      claudeActionEl.hidden = false;
      claudeActionEl.classList.remove("fading");
      claudeActionEl.querySelector("em").textContent = state.claude.action.summary;
      const tip = [
        state.claude.action.tool,
        state.claude.action.summary,
        state.claude.action.fullPath,
        state.claude.action.command,
        state.claude.action.prompt && "prompt: " + state.claude.action.prompt,
      ].filter(Boolean).join("\n");
      claudeActionEl.title = tip;
    } else {
      claudeActionEl.hidden = true;
    }
    if (state.claude.subagentType) {
      claudeSubEl.hidden = false;
      claudeSubEl.querySelector("em").textContent = state.claude.subagentType;
      claudeSubEl.title = "subagent: " + state.claude.subagentType;
    } else {
      claudeSubEl.hidden = true;
    }
  }

  function scheduleActionDim() {
    if (state.claude.actionDimTimer) clearTimeout(state.claude.actionDimTimer);
    state.claude.actionDimTimer = setTimeout(() => {
      claudeActionEl.classList.add("fading");
    }, 5000);
  }

  // ---------- Canvas ----------

  function resizeCanvas() {
    const dpr = window.devicePixelRatio || 1;
    const r = tapeCanvas.getBoundingClientRect();
    const w = Math.floor(r.width * dpr);
    const h = Math.floor(r.height * dpr);
    if (tapeCanvas.width === w && tapeCanvas.height === h) return;
    tapeCanvas.width = w;
    tapeCanvas.height = h;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  }
  window.addEventListener("resize", resizeCanvas);

  // Three intent buckets:
  //   read   — file content was read
  //   modify — file content/metadata was modified or file/dir created
  //   delete — file/dir was removed
  // Anything unknown stays grey so it's visible but stands out.
  const MODIFY_OPS = new Set([
    "write", "rename", "mkdir", "truncate", "chmod", "chown",
    "utimes", "utimensat", "futimes", "futimens", "lutimes",
    "setattrlist", "setattrlistat", "fsetattrlist",
    "chflags", "fchflags", "lchflags", "chflagsat",
    "fchown", "lchown", "fchownat", "fchmodat",
    "link", "linkat", "symlink", "symlinkat",
  ]);
  const DELETE_OPS = new Set(["unlink", "rmdir"]);

  function colorFor(op) {
    if (op === "read") return "#4aa3ff";
    if (MODIFY_OPS.has(op)) return "#ff5c5c";
    if (DELETE_OPS.has(op)) return "#f0883e";
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
  // currently visible (rendered + ancestors expanded). Returns null if
  // nothing matches — the marker is skipped this frame and will start
  // rendering once the file's tree row appears (e.g. after the
  // FSEvents-driven dirchange refresh propagates).
  function yForPath(rel, rowYMap) {
    if (rel === null) return null;
    if (rowYMap.has(rel)) return rowYMap.get(rel);
    let cur = rel;
    while (cur !== "") {
      const i = cur.lastIndexOf("/");
      cur = i < 0 ? "" : cur.slice(0, i);
      if (rowYMap.has(cur)) return rowYMap.get(cur);
    }
    return null;
  }

  function draw() {
    resizeCanvas();
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

    // File-activity ticks (fs_usage + FSEvents).
    for (const e of state.events) {
      if (e._t < minT) continue;
      if (filter && !e.path.includes(filter)) continue;
      const rel = relFromFull(e.path);
      const y = yForPath(rel, rowYMap);
      if (y === null) continue;
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
    countersEl.textContent = `received ${state.counters.recv} · drawn ${drawn}`;
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

  function setRunning(running) {
    state.running = running;
    toggle.textContent = running ? "Stop" : "Start";
    toggle.classList.toggle("stopped", !running);
  }

  toggle.addEventListener("click", () => setRunning(!state.running));
  windowSel.addEventListener("change", () => {
    state.windowSec = parseInt(windowSel.value, 10);
  });
  filterInput.addEventListener("input", () => {
    state.filter = filterInput.value.trim();
  });
  refreshBtn.addEventListener("click", () => {
    refreshAllExpanded();
  });

  // ---------- Panel resizer ----------

  (function () {
    let dragStartY = 0;
    let dragStartH = 0;

    resizerEl.addEventListener("mousedown", (e) => {
      if (document.body.classList.contains("claude-collapsed")) return;
      dragStartY = e.clientY;
      dragStartH = parseFloat(getComputedStyle(document.documentElement)
        .getPropertyValue("--panel-h")) || 200;
      resizerEl.classList.add("dragging");
      document.body.style.userSelect = "none";
      document.body.style.cursor = "ns-resize";
      e.preventDefault();
    });

    document.addEventListener("mousemove", (e) => {
      if (!resizerEl.classList.contains("dragging")) return;
      const delta = dragStartY - e.clientY;
      const newH = Math.max(28, Math.min(window.innerHeight - 100, dragStartH + delta));
      document.documentElement.style.setProperty("--panel-h", newH + "px");
    });

    document.addEventListener("mouseup", () => {
      if (!resizerEl.classList.contains("dragging")) return;
      resizerEl.classList.remove("dragging");
      document.body.style.userSelect = "";
      document.body.style.cursor = "";
    });
  })();

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

  function scheduleRefresh(rel) {
    state.pendingRefresh.add(rel);
    if (state.refreshTimer) return;
    state.refreshTimer = setTimeout(() => {
      const dirs = Array.from(state.pendingRefresh);
      state.pendingRefresh.clear();
      state.refreshTimer = null;
      for (const d of dirs) refreshDir(d);
    }, 300);
  }

  function scheduleParentRefresh(fullPath) {
    const rel = relFromFull(fullPath);
    if (rel === null) return;
    scheduleRefresh(parentRel(rel));
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
    updateExpandButton();
  }

  function renderTreeRoot() {
    treeContent.innerHTML = "";
    const ul = document.createElement("ul");
    treeContent.appendChild(ul);
    // Register a synthetic root node so refreshAllExpanded can find it.
    state.nodes.set("", { li: null, childUl: ul, expanded: true, isDir: true });
    return fetchTree("").then((entries) => {
      for (const ent of entries) ul.appendChild(makeNode(ent, ""));
    }).catch((err) => {
      treeContent.textContent = "tree error: " + err.message;
    });
  }

  // Recursively expand every directory currently in the tree (BFS).
  // Each toggleDir is awaited because it lazy-fetches children, and
  // those children become candidates for expansion on the next pass.
  async function expandAll() {
    expandAllBtn.disabled = true;
    try {
      const queue = [];
      for (const [rel, node] of state.nodes) {
        if (rel !== "" && node.isDir && !node.expanded) {
          queue.push({ rel, node });
        }
      }
      while (queue.length > 0) {
        const { rel, node } = queue.shift();
        const tri = node.li && node.li.querySelector(":scope > .row > .triangle");
        if (!tri || node.expanded) continue;
        await toggleDir(rel, node, tri);
        if (node.childUl) {
          for (const li of node.childUl.children) {
            if (li.tagName !== "LI" || !li.classList.contains("dir")) continue;
            const childRel = li.dataset.rel;
            const childNode = state.nodes.get(childRel);
            if (childNode && !childNode.expanded) {
              queue.push({ rel: childRel, node: childNode });
            }
          }
        }
      }
    } finally {
      expandAllBtn.disabled = false;
      updateExpandButton();
    }
  }

  function collapseAll() {
    for (const [rel, node] of state.nodes) {
      if (rel === "" || !node.isDir || !node.expanded) continue;
      const tri = node.li && node.li.querySelector(":scope > .row > .triangle");
      if (!tri) continue;
      node.expanded = false;
      tri.textContent = "▸";
      if (node.childUl) node.childUl.style.display = "none";
    }
    updateExpandButton();
  }

  function isFullyExpanded() {
    let total = 0, expanded = 0;
    for (const [rel, node] of state.nodes) {
      if (rel !== "" && node.isDir) {
        total++;
        if (node.expanded) expanded++;
      }
    }
    return total > 0 && expanded === total;
  }

  function updateExpandButton() {
    const all = isFullyExpanded();
    expandAllBtn.textContent = all ? "Collapse all" : "Expand all";
    expandAllBtn.classList.toggle("active", all);
  }

  expandAllBtn.addEventListener("click", async () => {
    if (isFullyExpanded()) {
      collapseAll();
    } else {
      await expandAll();
    }
  });

  hideHiddenBtn.addEventListener("click", () => {
    const hide = !treeEl.classList.contains("hide-hidden");
    treeEl.classList.toggle("hide-hidden", hide);
    hideHiddenBtn.classList.toggle("active", hide);
    hideHiddenBtn.textContent = hide ? "Show hidden" : "Hide hidden";
  });

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

  // Sync state from form fields whose values the browser may have
  // restored on refresh (the DOM <option selected> default doesn't
  // override Firefox/Chrome's session-form-state restoration).
  state.windowSec = parseInt(windowSel.value, 10) || state.windowSec;
  state.filter = filterInput.value.trim();

  // Default UI state on load: hide dotfiles and fully expand the tree.
  treeEl.classList.add("hide-hidden");
  hideHiddenBtn.classList.add("active");
  hideHiddenBtn.textContent = "Show hidden";

  resizeCanvas();
  connectWS();
  requestAnimationFrame(loop);
})();
