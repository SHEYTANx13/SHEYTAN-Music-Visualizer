/*
 * commands.js — the command dispatcher and static wiring of the
 * SHEYTAN web shell.
 *
 * Honest failure: the exact backend message is surfaced, never a
 * silent no-op or a fabricated success. Settings-affecting commands
 * refresh the visible state after a successful run.
 */

import { postCommand, showAPIError, setStatus } from "./api.js";
import { $, setText } from "./dom.js";
import { state } from "./state.js";
import { loadSettings, loadTransport, setIntensityChangeHandler, setPresetApplyHandler } from "./loaders.js";

export async function runCommand(id, args) {
    try {
        await postCommand(id, args);
        setStatus(`command ok: ${id}`);
        // Settings-affecting commands refresh the visible state.
        if (id.startsWith("theme.")) await loadSettings();
        if (id === "transport.set-volume") await loadTransport();
        if (id === "visualizer.preset.apply") setStatus(`preset applied: ${args && args.name}`);
        if (id === "import.audio") setStatus("import: native dialog handled by the OS");
        if (id === "export.start") setStatus("export: destination chosen, job handed to the export lane");
        // Panes that show command-adjacent state refresh lazily via the
        // 2 s transport tick and their own loaders; nothing faked here.
    } catch (err) {
        showAPIError(err.message);
        setStatus(`command failed: ${id} — ${err.message}`);
    }
}

/* Apply buttons in the templates pane are rendered by loaders.js with
 * an injected handler; this wires that handler to the dispatcher. */
export function wireApplyPresetButtons() {
    setPresetApplyHandler((name) => runCommand("visualizer.preset.apply", { name }));
}

export function wireButtons() {
    const map = {
        "cmd-play-pause": () => runCommand("transport.play-pause"),
        "cmd-stop": () => runCommand("transport.stop"),
        "cmd-seek-back": () => runCommand("transport.seek-back", { seconds: 10 }),
        "cmd-seek-forward": () => runCommand("transport.seek-forward", { seconds: 10 }),
        "cmd-import": () => runCommand("import.audio"),
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
    const probe = $("network-probe");
    if (probe) probe.addEventListener("click", () => probeNetwork());
    const retry = $("error-retry");
    if (retry) retry.addEventListener("click", () => retryLoadAll());
}

/* The intensity radio row is rendered by loaders.js; the change handler
 * is injected here to avoid a circular import at module-eval time. */
export function wireIntensityOptions() {
    setIntensityChangeHandler((name) => runCommand("theme.intensity.set", { name }));
}

/* probeNetwork lives in system.js; wiring keeps its own lazy import so
 * this module stays free of pane-specific code paths. */
async function probeNetwork() {
    const mod = await import("./system.js");
    return mod.probeNetwork();
}

/* loadAll lives in boot.js; the retry button re-runs the full refresh
 * through the same lazy import (single source of truth for boot). */
async function retryLoadAll() {
    const mod = await import("./boot.js");
    return mod.loadAll();
}
