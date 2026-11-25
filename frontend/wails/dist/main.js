/*
 * main.js — SHEYTAN Music Visualizer BETA web shell (Wails v3 slice).
 *
 * Vanilla JavaScript, offline-first: no frameworks, no CDN assets. All
 * dynamic data comes from the Go-side /api/* endpoints served by the
 * shell facade (internal/transport/wails); design tokens come from the
 * byte-identical tokens.json copy next to this file.
 *
 * Honesty rules implemented here:
 *   - When the API is unreachable, every affected surface shows a
 *     visible error state (never a fake "ok").
 *   - Unavailable destinations render their catalog emptyState text.
 *   - Unavailable commands render as disabled with their reason.
 *   - Renderer evidence is displayed verbatim; no state is upgraded.
 */

"use strict";

/* ------------------------------------------------------------------ */
/* Small utilities                                                     */
/* ------------------------------------------------------------------ */

const $ = (id) => document.getElementById(id);

async function fetchJSON(path, options) {
    const res = await fetch(path, options);
    const text = await res.text();
    let body = null;
    try {
        body = text ? JSON.parse(text) : null;
    } catch (err) {
        throw new Error(`${path}: invalid JSON response (HTTP ${res.status})`);
    }
    if (!res.ok) {
        const msg = body && body.error ? body.error : `HTTP ${res.status}`;
        throw new Error(`${path}: ${msg}`);
    }
    return body;
}

async function postCommand(id, args) {
    return fetchJSON("/api/command", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id, args: args || {} }),
    });
}

function setText(node, text) {
    if (node) node.textContent = text;
}

function clearNode(node) {
    while (node && node.firstChild) node.removeChild(node.firstChild);
}

/* Renders {key: value} rows into a <dl>. Every value becomes text
 * content — no HTML is ever built from API data. */
function renderKV(dl, pairs) {
    if (!dl) return;
    clearNode(dl);
    for (const [key, value] of pairs) {
        const dt = document.createElement("dt");
        dt.textContent = key;
        const dd = document.createElement("dd");
        dd.textContent = value;
        dl.appendChild(dt);
        dl.appendChild(dd);
    }
}

function formatDuration(nanos) {
    const total = Math.max(0, Math.floor(nanos / 1e9));
    const m = Math.floor(total / 60);
    const s = total % 60;
    return `${m}:${String(s).padStart(2, "0")}`;
}

/* ------------------------------------------------------------------ */
/* Error surface (honest: unreachable API is always visible)           */
/* ------------------------------------------------------------------ */

let apiHealthy = false;

function showAPIError(message) {
    apiHealthy = false;
    const banner = $("error-banner");
    const state = $("api-state");
    setText($("error-banner-text"), `Backend API unreachable: ${message}`);
    if (banner) banner.hidden = false;
    if (state) {
        state.textContent = "API error";
        state.classList.remove("api-state-ok", "api-state-pending");
        state.classList.add("api-state-error");
    }
    setStatus(`last refresh failed: ${message}`);
}

function markAPIHealthy() {
    if (apiHealthy) return;
    apiHealthy = true;
    const banner = $("error-banner");
    const state = $("api-state");
    if (banner) banner.hidden = true;
    if (state) {
        state.textContent = "API connected";
        state.classList.remove("api-state-error", "api-state-pending");
        state.classList.add("api-state-ok");
    }
}

function setStatus(message) {
    setText($("status-message"), message);
    setText($("status-checked"), `checked ${new Date().toLocaleTimeString()}`);
}

/* ------------------------------------------------------------------ */
/* Design tokens                                                       */
/* ------------------------------------------------------------------ */

/* Injects the canonical tokens as CSS custom properties. Fallbacks in
 * sheytan.css keep the page readable when the token fetch fails. */
async function applyTokens() {
    let doc;
    try {
        const res = await fetch("tokens.json");
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        doc = await res.json();
    } catch (err) {
        showAPIError(`design tokens: ${err.message}`);
        return;
    }
    const root = document.documentElement.style;
    const c = doc.color || {};
    const set = (name, value) => {
        if (value) root.setProperty(name, value);
    };
    for (const [k, v] of Object.entries(c.surface || {})) set(`--c-surface-${k}`, v);
    for (const [k, v] of Object.entries(c.accent || {})) set(`--c-accent-${k}`, v);
    for (const [k, v] of Object.entries(c.secondary || {})) set(`--c-secondary-${k}`, v);
    for (const [k, v] of Object.entries(c.text || {})) set(`--c-text-${k}`, v);
    for (const [k, v] of Object.entries(c.line || {})) set(`--c-line-${k}`, v);
    for (const [k, v] of Object.entries(c.status || {})) set(`--c-status-${k}`, v);
    const motion = doc.motion || {};
    for (const [k, v] of Object.entries(motion.durationMs || {})) set(`--motion-${k}`, `${v}ms`);
    for (const [k, v] of Object.entries(motion.easing || {})) set(`--motion-easing-${k}`, v);
    const focus = (doc.accessibility || {}).focusRing || {};
    set("--focus-ring-color", focus.color);
    set("--focus-ring-width", focus.widthDp ? `${focus.widthDp}px` : null);
    set("--focus-ring-offset", focus.offsetDp ? `${focus.offsetDp}px` : null);
    const radius = doc.radius || {};
    for (const [k, v] of Object.entries(radius)) set(`--radius-${k}`, `${v}px`);
    const bp = doc.breakpoints || {};
    if (bp.tablet && bp.tablet.minDp) set("--bp-tablet", `${bp.tablet.minDp}px`);
    if (bp.desktop && bp.desktop.minDp) set("--bp-desktop", `${bp.desktop.minDp}px`);
    if (bp.wide && bp.wide.minDp) set("--bp-wide", `${bp.wide.minDp}px`);
    window.__sheytanTokens = doc;
    applyIntensityDefaults(doc);
}

/* Seeds intensity-dependent CSS variables from the canonical presets. */
function applyIntensityDefaults(doc) {
    const d = doc || window.__sheytanTokens;
    if (!d || !d.intensityPresets) return;
    const intensity = (state.settings && state.settings.intensity) || d.defaultIntensity;
    const preset = d.intensityPresets[intensity];
    if (!preset) return;
    const root = document.documentElement.style;
    root.setProperty("--decoration-opacity", String(preset.decorationOpacity));
    root.setProperty("--glow-strength", String(preset.glowStrength));
    document.body.classList.toggle("no-embers", preset.embers === false);
    document.body.classList.toggle("reduced-motion", !!(state.settings && state.settings.reducedMotion));
    document.body.classList.toggle("reduced-glow", !!(state.settings && state.settings.reducedGlow));
}

/* ------------------------------------------------------------------ */
/* State                                                               */
/* ------------------------------------------------------------------ */

const state = {
    destinations: [],
    commands: null,
    settings: null,
    presets: [],
    transport: null,
    build: null,
    library: null,
};

/* ------------------------------------------------------------------ */
/* Loaders / renderers                                                 */
/* ------------------------------------------------------------------ */

async function loadDestinations() {
    const data = await fetchJSON("/api/destinations");
    state.destinations = data.destinations || [];
    renderTabs();
}

function renderTabs() {
    const strip = $("tabstrip");
    if (!strip) return;
    clearNode(strip);
    const panes = document.querySelectorAll(".pane");
    for (const dest of state.destinations) {
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "tab";
        btn.dataset.dest = String(dest.id);
        btn.textContent = String(dest.title);
        if (!dest.available) btn.classList.add("tab-unavailable");
        btn.title = dest.available
            ? String(dest.description || "")
            : String(dest.emptyState || dest.description || "");
        btn.addEventListener("click", () => activateTab(String(dest.id)));
        strip.appendChild(btn);
    }
    // Empty-state text for unavailable destinations comes verbatim from
    // the catalog — never invented by the frontend.
    for (const dest of state.destinations) {
        if (dest.available) continue;
        const empty = $(`empty-${String(dest.id).replace(/[^a-z-]/g, "")}`);
        if (empty) empty.textContent = String(dest.emptyState || "Not available in this build.");
    }
    // Keep the visible pane consistent with the (re)rendered strip.
    const active = strip.querySelector(".tab.active");
    if (active) {
        activateTab(active.dataset.dest, { keepActive: true });
    } else {
        const first = state.destinations.find((d) => d.available);
        if (first) activateTab(String(first.id));
    }
    void panes; // panes are static in index.html; kept for clarity
}

function activateTab(destID, options) {
    const strip = $("tabstrip");
    if (!strip) return;
    for (const btn of strip.querySelectorAll(".tab")) {
        btn.classList.toggle("active", btn.dataset.dest === destID);
    }
    for (const pane of document.querySelectorAll(".pane")) {
        pane.hidden = pane.dataset.dest !== destID;
    }
    if (!options || !options.keepActive) {
        setStatus(`viewing ${destID}`);
    }
}

async function loadBuildInfo() {
    state.build = await fetchJSON("/api/build-info");
    setText($("build-version"), `${state.build.version} (commit ${state.build.commit})`);
    renderKV($("home-build"), [
        ["Version", state.build.version],
        ["Commit", state.build.commit],
        ["Built", `${state.build.date} by ${state.build.builtBy}`],
        ["Go", state.build.goVersion],
    ]);
}

async function loadTransport() {
    state.transport = await fetchJSON("/api/transport");
    const t = state.transport;
    const playing = t.state === "playing" || t.state === "priming";
    setText($("transport-state"), `state: ${t.state}`);
    setText($("transport-position"), `position: ${formatDuration(Number(t.position))}`);
    setText($("transport-total"), `total: ${formatDuration(Number(t.total))}`);
    setText($("transport-device"), `device: ${t.deviceReal ? "real output proven" : "no real device session"}`);
    renderKV($("home-transport"), [
        ["State", t.state],
        ["Position", formatDuration(Number(t.position))],
        ["Total", formatDuration(Number(t.total))],
        ["Latency", `${(Number(t.latency) / 1e9).toFixed(2)} s`],
        ["Device", t.deviceReal ? "real" : "not proven"],
        ["Session", String(t.session)],
        ["Volume", String(t.volume)],
    ]);
    // Volume slider follows the backend only while the user is not
    // dragging it (no fighting between timer and input).
    const slider = $("volume");
    if (slider && !slider.matches(":active")) {
        slider.value = String(t.volume);
        setText($("volume-out"), Number(t.volume).toFixed(2));
    }
    const playBtn = $("cmd-play-pause");
    if (playBtn) playBtn.textContent = playing ? "Pause" : "Play";
}

async function loadRenderer() {
    const r = await fetchJSON("/api/renderer");
    renderKV($("home-renderer"), [
        ["Backend", r.backend],
        ["GPU state", r.gpuState],
        ["Backend API", r.backendApi || "(none)"],
        ["Adapter", r.adapter || "(none)"],
        ["Device type", r.deviceType || "(unknown)"],
        ["Device class", r.deviceClass || "(unknown)"],
    ]);
    setText($("home-renderer-note"), r.detail || "");
    setText($("renderer-pref-note"),
        "Renderer preference is controlled by the native shell and the " +
        "SHEYTAN_GPU_PREFERENCE environment; this web slice reports " +
        "evidence only and never upgrades a GPU state.");
}

async function loadLibrary() {
    state.library = await fetchJSON("/api/library");
    const lib = state.library;
    const formats = Array.isArray(lib.formats) ? lib.formats : [];
    renderKV($("library-summary"), [
        ["Track loaded", lib.trackLoaded ? "yes" : "no"],
        ["Track", lib.trackName || "(none)"],
        ["Duration", lib.trackLoaded ? `${Number(lib.trackDurationSeconds).toFixed(1)} s` : "(none)"],
        ["Project", lib.projectPath || "(no project open)"],
        ["Formats", formats.map((f) => f.container).join(", ") || "(none registered)"],
    ]);
    setText($("studio-track-note"), lib.trackLoaded
        ? `Current track: ${lib.trackName}`
        : "No track loaded. Import happens in the native shell in this slice; transport commands report honest no-track errors.");
}

async function loadSettings() {
    state.settings = await fetchJSON("/api/settings");
    renderIntensityOptions();
    const motion = $("opt-reduced-motion");
    const glow = $("opt-reduced-glow");
    if (motion) motion.checked = !!state.settings.reducedMotion;
    if (glow) glow.checked = !!state.settings.reducedGlow;
    applyIntensityDefaults();
}

function renderIntensityOptions() {
    const wrap = $("intensity-options");
    const doc = window.__sheytanTokens;
    if (!wrap || !doc || !doc.intensityPresets) return;
    clearNode(wrap);
    for (const name of Object.keys(doc.intensityPresets)) {
        const label = document.createElement("label");
        label.className = "check-row";
        const input = document.createElement("input");
        input.type = "radio";
        input.name = "intensity";
        input.value = name;
        input.checked = state.settings && state.settings.intensity === name;
        input.addEventListener("change", () => runCommand("theme.intensity.set", { name }));
        label.appendChild(input);
        label.appendChild(document.createTextNode(name));
        wrap.appendChild(label);
    }
}

async function loadPresets() {
    const data = await fetchJSON("/api/presets");
    state.presets = data.presets || [];
    const list = $("preset-list");
    if (!list) return;
    clearNode(list);
    for (const p of state.presets) {
        const li = document.createElement("li");
        li.className = "preset-item";
        const name = document.createElement("span");
        name.className = "preset-name";
        name.textContent = String(p.name);
        const mode = document.createElement("span");
        mode.className = "preset-mode";
        mode.textContent = p.config && p.config.mode ? String(p.config.mode) : "";
        const btn = document.createElement("button");
        btn.type = "button";
        btn.textContent = "Apply";
        btn.dataset.command = "visualizer.preset.apply";
        btn.addEventListener("click", () => runCommand("visualizer.preset.apply", { name: p.name }));
        li.appendChild(name);
        li.appendChild(mode);
        li.appendChild(btn);
        list.appendChild(li);
    }
}

async function loadCommands() {
    state.commands = await fetchJSON("/api/commands");
    // Buttons for supported commands get enabled; unsupported surfaces
    // keep their honest static notes. Unknown IDs never get buttons.
    const supported = new Set(state.commands.supported || []);
    for (const btn of document.querySelectorAll("[data-command]")) {
        btn.disabled = !supported.has(btn.dataset.command);
        if (!supported.has(btn.dataset.command)) {
            btn.title = "Command not wired to this web shell";
        }
    }
}

async function loadDiagnostics() {
    const d = await fetchJSON("/api/diagnostics");
    const list = $("selfchecks");
    if (list) {
        clearNode(list);
        for (const c of d.selfChecks || []) {
            const li = document.createElement("li");
            li.className = c.passed ? "check-pass" : "check-fail";
            li.textContent = `${c.passed ? "PASS" : "FAIL"} ${c.name}${c.detail ? ` — ${c.detail}` : ""}`;
            list.appendChild(li);
        }
    }
    const view = $("diagnostics-json");
    if (view) view.textContent = JSON.stringify(d, null, 2);
    renderKV($("home-data"), [
        ["Mode", d.dataMode],
        ["Root", d.dataRoot],
        ["Log", d.logPath || "(unresolved)"],
        ["Checked at", new Date(d.checkedAt).toLocaleString()],
    ]);
}

function renderDescriptions(destinations) {
    const byID = new Map(destinations.map((d) => [String(d.id), d]));
    const pairs = [
        ["home-desc", "home"],
        ["studio-desc", "studio"],
        ["templates-desc", "templates"],
        ["library-desc", "library"],
        ["export-desc", "export"],
        ["requirements-desc", "requirements"],
        ["systemfiles-desc", "system-files"],
        ["settings-desc", "settings"],
        ["diagnostics-desc", "diagnostics"],
    ];
    for (const [nodeID, destID] of pairs) {
        const dest = byID.get(destID);
        if (dest) setText($(nodeID), String(dest.description || ""));
    }
}

/* ------------------------------------------------------------------ */
/* Command wiring                                                      */
/* ------------------------------------------------------------------ */

async function runCommand(id, args) {
    try {
        await postCommand(id, args);
        setStatus(`command ok: ${id}`);
        // Settings-affecting commands refresh the visible state.
        if (id.startsWith("theme.")) await loadSettings();
        if (id === "transport.set-volume") await loadTransport();
        if (id === "visualizer.preset.apply") setStatus(`preset applied: ${args && args.name}`);
    } catch (err) {
        // Honest failure: the exact backend message is surfaced, never
        // a silent no-op or a fabricated success.
        showAPIError(err.message);
        setStatus(`command failed: ${id} — ${err.message}`);
    }
}

function wireButtons() {
    const map = {
        "cmd-play-pause": () => runCommand("transport.play-pause"),
        "cmd-stop": () => runCommand("transport.stop"),
        "cmd-seek-back": () => runCommand("transport.seek-back", { seconds: 10 }),
        "cmd-seek-forward": () => runCommand("transport.seek-forward", { seconds: 10 }),
    };
    for (const [id, fn] of Object.entries(map)) {
        const btn = $(id);
        if (btn) btn.addEventListener("click", fn);
    }
    const volume = $("volume");
    if (volume) {
        volume.addEventListener("input", () => {
            setText($("volume-out"), Number(volume.value).toFixed(2));
        });
        volume.addEventListener("change", () => {
            runCommand("transport.set-volume", { volume: Number(volume.value) });
        });
    }
    const motion = $("opt-reduced-motion");
    if (motion) {
        motion.addEventListener("change", () =>
            runCommand("theme.reduced-motion.set", { enabled: motion.checked }));
    }
    const glow = $("opt-reduced-glow");
    if (glow) {
        glow.addEventListener("change", () =>
            runCommand("theme.reduced-glow.set", { enabled: glow.checked }));
    }
    const retry = $("error-retry");
    if (retry) retry.addEventListener("click", () => loadAll());
}

/* ------------------------------------------------------------------ */
/* Downloads: app-managed media tools (REAL bytes, REAL states)        */
/*                                                                     */
/* Run 45532045379: the downloads pane shows the download FILE SIZE,   */
/* the downloaded/total progress and the transfer speed straight from  */
/* the Go mediatools manager (pinned manifest, digest verification,    */
/* allowlisted hosts). The UI never invents progress: no bytes field   */
/* is faked, an unreachable API renders an explicit error state.       */
/* ------------------------------------------------------------------ */

const MEDIA_ACTIVE_STATES = new Set([
    "downloading", "verifying", "installing",
]);

function fmtBytes(n) {
    if (!Number.isFinite(n) || n <= 0) return "\u2014";
    const units = ["B", "KiB", "MiB", "GiB"];
    let v = n;
    let i = 0;
    while (v >= 1024 && i < units.length - 1) {
        v /= 1024;
        i += 1;
    }
    return (i === 0 ? v : v.toFixed(1)) + " " + units[i];
}

function renderMediaTools(snap) {
    const stateEl = $("mediatools-state");
    const stageEl = $("mediatools-stage");
    const progEl = $("mediatools-progress");
    const fill = $("mediatools-fill");
    const bar = $("mediatools-bar");
    const sizeEl = $("mediatools-size");
    const bytesEl = $("mediatools-bytes");
    const speedEl = $("mediatools-speed");
    const detailEl = $("mediatools-detail");
    const checkBtn = $("mediatools-check");
    const installBtn = $("mediatools-install");
    const cancelBtn = $("mediatools-cancel");
    if (!stateEl) return;

    const state = String(snap.state || "unknown");
    stateEl.textContent = state.replace(/-/g, " ");
    stateEl.dataset.state = state.replace(/-/g, "_");

    const active = MEDIA_ACTIVE_STATES.has(state);
    const showProgress = active && Number(snap.totalBytes) > 0;
    if (progEl) progEl.hidden = !showProgress;
    if (stageEl) {
        stageEl.hidden = !(active && snap.stage);
        setText(stageEl, active && snap.stage ? "stage: " + snap.stage : "");
    }

    if (showProgress) {
        const pct = Math.max(0, Math.min(100, (snap.downloadedBytes / snap.totalBytes) * 100));
        if (fill) fill.style.width = pct.toFixed(1) + "%";
        if (bar) bar.setAttribute("aria-valuenow", String(Math.round(pct)));
        setText(sizeEl, "total " + fmtBytes(Number(snap.totalBytes)));
        setText(bytesEl, fmtBytes(Number(snap.downloadedBytes)) + " / " + fmtBytes(Number(snap.totalBytes))
            + " (" + pct.toFixed(0) + "%)");
        setText(speedEl, snap.bytesPerSecond > 0 ? fmtBytes(Number(snap.bytesPerSecond)) + "/s" : "\u2014");
    } else {
        setText(sizeEl, snap.pack && snap.pack.sizeBytes ? "pinned " + fmtBytes(Number(snap.pack.sizeBytes)) : "\u2014");
        setText(bytesEl, "\u2014");
        setText(speedEl, "\u2014");
    }

    setText(detailEl, snap.lastError ? "error: " + snap.lastError : (snap.detail || ""));

    if (checkBtn) checkBtn.disabled = active;
    if (installBtn) installBtn.disabled = active
        || state === "unsupported-platform"
        || (!snap.installed && state !== "not-installed" && state !== "failed" && state !== "repair-needed" && state !== "ready");
    if (cancelBtn) cancelBtn.disabled = !active;
}

async function loadMediaTools() {
    const snap = await fetchJSON("/api/mediatools");
    renderMediaTools(snap);
}

async function mediaAction(path) {
    try {
        const snap = await fetchJSON(path, { method: "POST" });
        renderMediaTools(snap);
        setStatus("media tools: " + (snap.state || "updated"));
    } catch (err) {
        showAPIError(err.message);
        setStatus("media tools action failed: " + err.message);
    }
}

function wireMediaTools() {
    const check = $("mediatools-check");
    const install = $("mediatools-install");
    const cancel = $("mediatools-cancel");
    if (check) check.addEventListener("click", () => mediaAction("/api/mediatools/check"));
    if (install) install.addEventListener("click", () => mediaAction("/api/mediatools/install"));
    if (cancel) cancel.addEventListener("click", () => mediaAction("/api/mediatools/cancel"));
}

/* ------------------------------------------------------------------ */
/* Drag-to-scroll: every section pans with finger, pen and mouse drag  */
/* ------------------------------------------------------------------ */

function wireDragScroll() {
    // Pointer-based drag scrolling for every scrollable pane. Vertical
    // finger scrolling stays native (touch-action: pan-y); this adds
    // mouse-drag panning and pointer capture so a drag that leaves the
    // pane keeps scrolling until release.
    for (const el of document.querySelectorAll(".pane")) {
        let dragging = false;
        let startY = 0;
        let startScroll = 0;
        let moved = false;
        el.addEventListener("pointerdown", (ev) => {
            if (ev.pointerType === "touch") return; // native scroll for touch
            if (ev.button !== 0) return;
            dragging = true;
            moved = false;
            startY = ev.clientY;
            startScroll = el.scrollTop;
        });
        el.addEventListener("pointermove", (ev) => {
            if (!dragging) return;
            const dy = ev.clientY - startY;
            if (!moved && Math.abs(dy) > 4) {
                moved = true;
                el.classList.add("drag-scrolling");
                try { el.setPointerCapture(ev.pointerId); } catch (err) { /* pane may be gone */ }
            }
            if (moved) {
                el.scrollTop = startScroll - dy;
                ev.preventDefault();
            }
        });
        const endDrag = (ev) => {
            if (!dragging) return;
            dragging = false;
            el.classList.remove("drag-scrolling");
            if (moved && ev.pointerId !== undefined) {
                try { el.releasePointerCapture(ev.pointerId); } catch (err) { /* already released */ }
            }
        };
        el.addEventListener("pointerup", endDrag);
        el.addEventListener("pointercancel", endDrag);
    }
}

/* ------------------------------------------------------------------ */
/* Full load + polling                                                 */
/* ------------------------------------------------------------------ */

async function loadAll() {
    try {
        await applyTokens();
        await loadDestinations();
        renderDescriptions(state.destinations);
        await Promise.all([
            loadBuildInfo(),
            loadTransport(),
            loadRenderer(),
            loadLibrary(),
            loadSettings(),
            loadPresets(),
            loadCommands(),
            loadDiagnostics(),
            loadMediaTools(),
        ]);
        markAPIHealthy();
        setStatus("all surfaces refreshed");
    } catch (err) {
        showAPIError(err.message);
    }
}

/* The 2 s timer refreshes ONLY the transport snapshot, per the slice
 * contract; heavier surfaces refresh on demand. */
function startTransportPolling() {
    const tick = async () => {
        if (!apiHealthy) return;
        try {
            await loadTransport();
            // The downloads pane polls at the same low cadence: real
            // progress bytes for an in-flight engine download.
            await loadMediaTools();
        } catch (err) {
            showAPIError(err.message);
        }
    };
    setInterval(tick, 2000);
}

function boot() {
    wireButtons();
    wireMediaTools();
    wireDragScroll();
    loadAll();
    startTransportPolling();
}

document.addEventListener("DOMContentLoaded", boot);
