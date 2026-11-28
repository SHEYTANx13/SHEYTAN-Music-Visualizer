/*
 * loaders.js — the per-surface data loaders of the SHEYTAN web shell.
 *
 * Each loader fetches ONE surface and renders it verbatim from the
 * backend: no state is upgraded, no value invented, unavailable
 * things say exactly why.
 */

import { fetchJSON } from "./api.js";
import { $, setText, renderKV, formatDuration } from "./dom.js";
import { state } from "./state.js";
import { applyIntensityDefaults, tokenDocOrNull } from "./tokens.js";
import { loadWorlds } from "./worlds.js";

export async function loadBuildInfo() {
    state.build = await fetchJSON("/api/build-info");
    // Run 46168717789 product decision: the commit identifier is no
    // longer part of the product surface - the version label is the
    // single user-facing build identity.
    setText($("build-version"), state.build.version);
    renderKV($("home-build"), [
        ["Version", state.build.version],
        ["Built", `${state.build.date} by ${state.build.builtBy}`],
        ["Go", state.build.goVersion],
    ]);
}

export async function loadTransport() {
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

export async function loadRenderer() {
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
        "Renderer preference is controlled by the SHEYTAN_GPU_PREFERENCE " +
        "environment and the settings file; this shell reports evidence " +
        "only and never upgrades a GPU state.");
}

export async function loadLibrary() {
    state.library = await fetchJSON("/api/library");
    const lib = state.library;
    const formats = Array.isArray(lib.formats) ? lib.formats : [];
    renderKV($("library-summary"), [
        ["Track loaded", lib.trackLoaded ? "yes" : "no"],
        ["Track", lib.trackName || "(none)"],
        ["Duration", lib.trackLoaded ? `${Number(lib.trackDurationSeconds).toFixed(1)} s` : "(none)"],
        ["Project", lib.projectPath || "(no project open)"],
        ["Formats", formats.map((f) => f.container).join(", ") || "(none registered)"],
        ["Importing", lib.importing ? "in progress" : "idle"],
    ]);
    setText($("studio-track-note"), lib.trackLoaded
        ? `Current track: ${lib.trackName}`
        : "No track loaded. Press Import Music to pick a file through the native dialog; the Song DNA is derived from the measured analysis.");
}

export async function loadSettings() {
    state.settings = await fetchJSON("/api/settings");
    renderIntensityOptions();
    const motion = $("opt-reduced-motion");
    const glow = $("opt-reduced-glow");
    if (motion) motion.checked = !!state.settings.reducedMotion;
    if (glow) glow.checked = !!state.settings.reducedGlow;
    applyIntensityDefaults();
}

/* renderIntensityOptions builds the canonical radio row from the token
 * document; the change binding is injected by commands.js (wireModule). */
let intensityChangeHandler = null;
let presetApplyHandler = null;

export function setIntensityChangeHandler(fn) {
    intensityChangeHandler = fn;
}

export function setPresetApplyHandler(fn) {
    presetApplyHandler = fn;
}

function renderIntensityOptions() {
    const wrap = $("intensity-options");
    const doc = tokenDocOrNull();
    if (!wrap || !doc || !doc.intensityPresets) return;
    const clear = (node) => {
        while (node && node.firstChild) node.removeChild(node.firstChild);
    };
    clear(wrap);
    for (const name of Object.keys(doc.intensityPresets)) {
        const label = document.createElement("label");
        label.className = "check-row";
        const input = document.createElement("input");
        input.type = "radio";
        input.name = "intensity";
        input.value = name;
        input.checked = state.settings && state.settings.intensity === name;
        input.addEventListener("change", () => {
            if (intensityChangeHandler) intensityChangeHandler(name);
        });
        label.appendChild(input);
        label.appendChild(document.createTextNode(name));
        wrap.appendChild(label);
    }
}

export async function loadPresets() {
    const data = await fetchJSON("/api/presets");
    state.presets = data.presets || [];
    setText($("templates-note"),
        "Applying a preset reconfigures the shared visualizer catalog the renderers consume. The live frame preview and user-authored 3D template packs stay native-shell surfaces; this pane edits nothing silently.");
    const list = $("preset-list");
    if (!list) return;
    const clear = (node) => {
        while (node && node.firstChild) node.removeChild(node.firstChild);
    };
    clear(list);
    for (const p of state.presets) {
        const li = document.createElement("li");
        li.className = "preset-item";
        const head = document.createElement("div");
        head.className = "preset-head";
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
        btn.addEventListener("click", () => {
            if (presetApplyHandler) presetApplyHandler(p.name);
        });
        head.appendChild(name);
        head.appendChild(mode);
        head.appendChild(btn);
        li.appendChild(head);
        // Real preset parameters, verbatim from the backend catalog:
        // scalar config values rendered as text, nothing invented.
        const params = document.createElement("div");
        params.className = "preset-params";
        params.textContent = presetParamTextLocal(p.config);
        li.appendChild(params);
        list.appendChild(li);
    }
}

/* presetParamText renders the real scalar fields of a preset config as
 * one bounded text line ("key=value" pairs). Objects and arrays are
 * summarized as "(composed)" so the line never explodes. */
function presetParamTextLocal(config) {
    if (!config || typeof config !== "object") return "";
    const parts = [];
    for (const [k, v] of Object.entries(config)) {
        if (k === "name") continue;
        if (v === null || v === undefined) continue;
        if (typeof v === "object") {
            parts.push(k + "=(composed)");
        } else {
            parts.push(k + "=" + String(v));
        }
        if (parts.length >= 6) break;
    }
    return parts.join("  ");
}

export async function loadCommands() {
    state.commands = await fetchJSON("/api/commands");
    // Buttons for supported commands get enabled; unsupported surfaces
    // keep their honest static notes. Unknown IDs never get buttons.
    const supported = new Set((state.commands.supported || []).map((c) => c.id));
    for (const btn of document.querySelectorAll("[data-command]")) {
        btn.disabled = !supported.has(btn.dataset.command);
        if (!supported.has(btn.dataset.command)) {
            btn.title = "Command not wired to this web shell";
        }
    }
}

export async function loadDiagnostics() {
    const d = await fetchJSON("/api/diagnostics");
    const list = $("selfchecks");
    if (list) {
        const clear = (node) => {
            while (node && node.firstChild) node.removeChild(node.firstChild);
        };
        clear(list);
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
