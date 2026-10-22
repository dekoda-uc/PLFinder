(function () {
  "use strict";

  const $ = (id) => document.getElementById(id);

  const btnStart = $("btnStart");
  const btnPause = $("btnPause");
  const btnStop = $("btnStop");
  const btnSave = $("btnSave");
  const btnSaveResource = $("btnSaveResource");
  const btnCopy = $("btnCopy");
  const statusBadge = $("statusBadge");
  const resultsBody = $("resultsBody");
  const emptyMsg = $("emptyMsg");
  const selAll = $("selAll");
  const toast = $("toast");
  const progressFill = $("progressFill");
  const statTotalWrap = $("statTotalWrap");

  const modal = $("resourceModal");
  const modalTitle = $("modalTitle");
  const modalMeta = $("modalMeta");
  const modalBody = $("modalBody");
  const modalClose = $("modalClose");
  const modalSave = $("modalSave");

  let paused = false;
  let rowCount = 0;
  let modalCurrent = null; // {id, path}

  function showToast(msg) {
    toast.textContent = msg;
    toast.classList.add("show");
    setTimeout(() => toast.classList.remove("show"), 2500);
  }

  function setStatus(running, isPaused) {
    btnStart.disabled = running;
    btnPause.disabled = !running;
    btnStop.disabled = !running;
    paused = isPaused;
    btnPause.textContent = isPaused ? "Resume" : "Pause";

    statusBadge.classList.remove("badge-idle", "badge-running", "badge-paused");
    if (!running) {
      statusBadge.textContent = "Stopped";
      statusBadge.classList.add("badge-idle");
    } else if (isPaused) {
      statusBadge.textContent = "Paused";
      statusBadge.classList.add("badge-paused");
    } else {
      statusBadge.textContent = "Running";
      statusBadge.classList.add("badge-running");
    }
  }

  function formatElapsed(sec) {
    if (sec < 60) return Math.round(sec) + "s";
    const m = Math.floor(sec / 60);
    const s = Math.round(sec % 60);
    return `${m}m ${s}s`;
  }

  function updateStats(stats) {
    $("statScanned").textContent = stats.scanned;
    $("statOpen").textContent = stats.open;
    $("statMatches").textContent = stats.matches;
    $("statElapsed").textContent = formatElapsed(stats.elapsedSec || 0);
    $("statCurrent").textContent = stats.running ? (stats.current || "—") : "—";

    if (stats.total && stats.total > 0) {
      statTotalWrap.style.display = "inline";
      $("statTotal").textContent = stats.total;
      const pct = Math.min(100, (stats.scanned / stats.total) * 100);
      progressFill.style.width = pct.toFixed(1) + "%";
    } else {
      statTotalWrap.style.display = "none";
      progressFill.style.width = stats.running ? "100%" : "0%";
    }

    setStatus(stats.running, stats.paused);
  }

  function buildResourceCell(res) {
    const cell = document.createElement("div");
    cell.className = "rcell";
    const entries = Object.entries(res.resources || {});
    if (entries.length === 0) return cell;

    entries.forEach(([path, info]) => {
      const chip = document.createElement("span");
      chip.className = "res-chip";
      if (info.ok) chip.classList.add("ok");

      const pathSpan = document.createElement("span");
      pathSpan.className = "res-path";
      pathSpan.textContent = path;

      const statusSpan = document.createElement("span");
      statusSpan.className = "res-status";
      statusSpan.textContent = info.status || "?";

      chip.append(pathSpan, statusSpan);

      // The "View resource" button only shows up if the resource exists (OK status)
      if (info.ok && info.hasBody) {
        const viewBtn = document.createElement("button");
        viewBtn.textContent = "View resource";
        viewBtn.addEventListener("click", () => viewResource(res.id, path));
        chip.appendChild(viewBtn);
      }

      cell.appendChild(chip);
    });
    return cell;
  }

  function addRow(res) {
    if (rowCount === 0) emptyMsg.style.display = "none";
    rowCount++;

    const row = document.createElement("div");
    row.className = "results-row";
    row.dataset.id = res.id;
    row.dataset.ip = res.ip;
    row.dataset.port = res.port;
    row.dataset.scheme = res.scheme || "";
    if (res.serverMatch && res.resourceHit) {
      row.classList.add("match-both"); // server match AND resource found: strong color
    } else if (res.serverMatch || res.resourceHit) {
      row.classList.add("match-partial"); // only one of the two: lighter color
    }

    const cellSel = document.createElement("div");
    cellSel.className = "rcell";
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.className = "rowSel";
    cellSel.appendChild(cb);

    const cellIp = document.createElement("div");
    cellIp.className = "rcell";
    cellIp.textContent = res.ip;

    const cellPort = document.createElement("div");
    cellPort.className = "rcell";
    cellPort.textContent = res.port;

    const cellScheme = document.createElement("div");
    cellScheme.className = "rcell";
    cellScheme.textContent = res.scheme || "-";

    const cellServer = document.createElement("div");
    cellServer.className = "rcell";
    cellServer.textContent = res.server || "-";

    const cellMatch = document.createElement("div");
    cellMatch.className = "rcell";
    if (res.serverMatch) {
      const tag = document.createElement("span");
      tag.className = "tag-match";
      tag.textContent = "MATCH";
      cellMatch.appendChild(tag);
    }
    if (res.resourceHit) {
      const tag2 = document.createElement("span");
      tag2.className = "tag-resource";
      tag2.textContent = "RESOURCE";
      cellMatch.appendChild(tag2);
    }

    const cellRes = buildResourceCell(res);

    row.append(cellSel, cellIp, cellPort, cellScheme, cellServer, cellMatch, cellRes);
    resultsBody.appendChild(row);
  }

  function clearTable() {
    resultsBody.innerHTML = "";
    rowCount = 0;
    emptyMsg.style.display = "block";
  }

  // ---------- "View resource" modal ----------
  function openModal() {
    modal.classList.remove("hidden");
  }
  function closeModal() {
    modal.classList.add("hidden");
    modalBody.innerHTML = "";
    modalCurrent = null;
  }
  modalClose.addEventListener("click", closeModal);
  modal.addEventListener("click", (e) => {
    if (e.target === modal) closeModal();
  });

  // Guesses a short content-type label (JSON/HTML/TXT/...) from the
  // Content-Type header, or from the resource's file extension as a fallback.
  function guessTypeLabel(contentType, path) {
    const ct = (contentType || "").toLowerCase();
    const ext = (path.split(".").pop() || "").toLowerCase();
    if (ct.includes("json") || ext === "json") return "JSON";
    if (ct.includes("html") || ext === "html" || ext === "htm") return "HTML";
    if (ct.includes("xml") || ext === "xml") return "XML";
    if (ct.includes("javascript") || ext === "js") return "JS";
    if (ct.includes("css") || ext === "css") return "CSS";
    if (ct.startsWith("text/") || ext === "txt") return "TXT";
    if (ct.startsWith("image/")) return "IMG";
    return ext ? ext.toUpperCase() : "DATA";
  }

  async function viewResource(id, path) {
    const url = `/api/resource-content?id=${id}&path=${encodeURIComponent(path)}`;
    let resp;
    try {
      resp = await fetch(url);
    } catch (e) {
      showToast("Could not load the resource");
      return;
    }
    if (!resp.ok) {
      showToast("Resource not available (" + resp.status + ")");
      return;
    }

    const ct = resp.headers.get("Content-Type") || "";
    const truncated = resp.headers.get("X-Truncated") === "true";
    const status = resp.headers.get("X-Status-Code") || "";
    const typeLabel = guessTypeLabel(ct, path);

    // Always shown as text/code, whether it's JSON, HTML, TXT, etc.
    let text;
    try {
      text = await resp.text();
    } catch (e) {
      showToast("Could not read the resource content");
      return;
    }

    modalTitle.textContent = path;
    modalMeta.innerHTML = "";
    const badge = document.createElement("span");
    badge.className = "type-badge";
    badge.textContent = typeLabel;
    modalMeta.appendChild(badge);
    const metaText = document.createElement("span");
    metaText.textContent = ` Status: ${status} · Content-Type: ${ct || "unknown"}` +
      (truncated ? " · (content truncated to 1 MiB)" : "");
    modalMeta.appendChild(metaText);

    modalBody.innerHTML = "";
    modalCurrent = { id, path };

    const pre = document.createElement("pre");
    const code = document.createElement("code");
    code.textContent = text.length > 0 ? text : "(empty content)";
    pre.appendChild(code);
    modalBody.appendChild(pre);

    openModal();
  }

  modalSave.addEventListener("click", () => {
    if (!modalCurrent) return;
    const { id, path } = modalCurrent;
    const url = `/api/resource-content?id=${id}&path=${encodeURIComponent(path)}`;
    const a = document.createElement("a");
    a.href = url;
    a.download = path.replace(/^\//, "") || "resource";
    document.body.appendChild(a);
    a.click();
    a.remove();
  });

  // ---------- Live event connection (SSE) ----------
  function connectEvents() {
    const es = new EventSource("/api/events");
    es.addEventListener("result", (e) => addRow(JSON.parse(e.data)));
    es.addEventListener("stats", (e) => updateStats(JSON.parse(e.data)));
    es.addEventListener("done", () => {
      fetch("/api/stats").then((r) => r.json()).then(updateStats);
    });
    es.onerror = () => {
      // el navegador/webview reconecta solo
    };
  }
  connectEvents();

  // ---------- Botones ----------
  btnStart.addEventListener("click", async () => {
    clearTable();
    const cfg = {
      ranges: $("ranges").value,
      ports: $("ports").value,
      connections: parseInt($("connections").value || "400", 10),
      timeoutSec: parseInt($("timeout").value || "4", 10),
      scheme: $("scheme").value,
      match: $("match").value,
      resources: $("resources").value,
      okCodes: $("okCodes").value,
      tcpOnly: $("tcpOnly").checked,
    };
    if (!cfg.ranges.trim()) {
      showToast("Enter at least one IP range");
      return;
    }
    const resp = await fetch("/api/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(cfg),
    });
    if (!resp.ok) {
      const err = await resp.json().catch(() => ({}));
      showToast(err.error || "Could not start the scan");
      return;
    }
    setStatus(true, false);
  });

  btnPause.addEventListener("click", async () => {
    const endpoint = paused ? "/api/resume" : "/api/pause";
    const resp = await fetch(endpoint, { method: "POST" });
    const stats = await resp.json();
    updateStats(stats);
  });

  btnStop.addEventListener("click", async () => {
    await fetch("/api/stop", { method: "POST" });
  });

  selAll.addEventListener("change", () => {
    document.querySelectorAll(".rowSel").forEach((cb) => (cb.checked = selAll.checked));
  });

  function getScope() {
    return document.querySelector('input[name="scope"]:checked').value;
  }

  function getSelectedIds() {
    return Array.from(document.querySelectorAll(".rowSel:checked")).map((cb) =>
      parseInt(cb.closest(".results-row").dataset.id, 10)
    );
  }

  btnSave.addEventListener("click", async () => {
    const scope = getScope();
    const ids = scope === "selected" ? getSelectedIds() : [];
    if (scope === "selected" && ids.length === 0) {
      showToast("No rows selected");
      return;
    }
    const resp = await fetch("/api/export", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ scope, ids }),
    });
    const blob = await resp.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "results.csv";
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  });

  btnSaveResource.addEventListener("click", async () => {
    const scope = getScope();
    const ids = scope === "selected" ? getSelectedIds() : [];
    if (scope === "selected" && ids.length === 0) {
      showToast("No rows selected");
      return;
    }
    const resp = await fetch("/api/export-resources", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ scope, ids }),
    });
    if (!resp.ok) {
      const msg = await resp.text().catch(() => "");
      showToast(msg || "No saved resources for this selection");
      return;
    }
    const blob = await resp.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "resources.zip";
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  });

  btnCopy.addEventListener("click", async () => {
    const scope = getScope();
    let rows;
    if (scope === "selected") {
      rows = Array.from(document.querySelectorAll(".rowSel:checked")).map((cb) => cb.closest(".results-row"));
    } else {
      rows = Array.from(document.querySelectorAll("#resultsBody .results-row"));
    }
    if (rows.length === 0) {
      showToast("No rows to copy");
      return;
    }
    const lines = rows.map((tr) => {
      const scheme = tr.dataset.scheme || "http";
      return `${scheme}://${tr.dataset.ip}:${tr.dataset.port}`;
    });
    try {
      await navigator.clipboard.writeText(lines.join("\n"));
      showToast(`${lines.length} URL(s) copied to clipboard`);
    } catch (e) {
      showToast("Could not copy (browser permission)");
    }
  });

  // Preloads configuration passed via the command line (-ranges, -ports, ...).
  // Only fills in the form, never starts the scan automatically.
  function applyInitialConfig(cfg) {
    if (!cfg || !cfg.hasConfig) return;
    if (cfg.ranges) $("ranges").value = cfg.ranges;
    if (cfg.ports) $("ports").value = cfg.ports;
    if (cfg.connections) $("connections").value = cfg.connections;
    if (cfg.timeoutSec) $("timeout").value = cfg.timeoutSec;
    if (cfg.scheme) $("scheme").value = cfg.scheme;
    if (cfg.match) $("match").value = cfg.match;
    if (cfg.resources) $("resources").value = cfg.resources;
    if (cfg.okCodes) $("okCodes").value = cfg.okCodes;
    if (cfg.tcpOnly) $("tcpOnly").checked = true;
    showToast("Configuration imported from the command line");
  }

  // Estado inicial
  fetch("/api/stats").then((r) => r.json()).then(updateStats).catch(() => {});
  fetch("/api/initial-config").then((r) => r.json()).then(applyInitialConfig).catch(() => {});
})();
