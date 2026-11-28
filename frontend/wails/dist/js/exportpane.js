/*
 * exportpane.js — the MP4 export pane (repair/run-46033044814).
 *
 * The form posts export.start through the facade; the Go side opens
 * the native save dialog, validates every argument and hands the job
 * to the bounded export lane. Progress is the REAL job snapshot from
 * /api/export (frames rendered / total, phase, result metadata) —
 * never synthesized. Cancel reaches the job's own Cancel.
 */

import { fetchJSON, postCommand, showAPIError, setStatus } from "./api.js";
import { $, setText, setBusy } from "./dom.js";
import { runCommand } from "./commands.js";

export async function loadExportStatus() {
    try {
        const doc = await fetchJSON("/api/export");
        renderExportStatus(doc);
    } catch (err) {
        showAPIError(err.message);
    }
}

function renderExportStatus(doc) {
    const stateEl = $("export-state");
    if (!stateEl) return;
    const st = String(doc.state || "idle");
    lastObservedState = st;
    stateEl.textContent = st;
    stateEl.dataset.state = st;

    const prog = $("export-progress");
    if (prog) prog.hidden = st !== "running";

    if (st === "running") {
        const pct = Number(doc.percent || 0);
        setText($("export-phase"), `phase: ${doc.phase || "rendering"}`);
        setText($("export-percent"), `${pct.toFixed(0)}%`);
        const fill = $("export-bar-fill");
        if (fill) fill.style.width = `${Math.max(2, Math.min(100, pct))}%`;
        const bar = $("export-bar");
        if (bar) bar.setAttribute("aria-valuenow", String(Math.round(pct)));
        setText($("export-result"),
            `frame ${Number(doc.framesRendered || 0)} / ${Number(doc.framesTotal || 0)} rendered`);
        setText($("export-error"), "");
    } else {
        setText($("export-result"), "");
        const errEl = $("export-error");
        if (errEl) errEl.hidden = st !== "failed";
        if (st === "done" && doc.result) {
            const r = doc.result;
            setText($("export-result"),
                `finished: ${r.outputPath} — ${r.width}x${r.height} @ ${r.fps} fps, ` +
                `${r.frames} frames, ${r.videoCodec} video / ${r.audioCodec} audio`);
            setText($("export-error"), "");
        } else if (st === "failed") {
            setText($("export-result"), "");
            setText($("export-error"), doc.error || "export failed (no detail)");
        } else {
            setText($("export-error"), "");
            setText($("export-result"), "");
        }
    }

    const startBtn = $("cmd-export-start");
    const cancelBtn = $("cmd-export-cancel");
    if (cancelBtn) cancelBtn.disabled = st !== "running";
    if (startBtn) startBtn.disabled = st === "running";
}

export function wireExportControls() {
    // Height follows the chosen resolution preset (the presets carry
    // data-height); the height select stays user-editable.
    const width = $("export-width");
    const height = $("export-height");
    if (width && height) {
        width.addEventListener("change", () => {
            const h = width.selectedOptions[0]?.dataset.height;
            if (h) height.value = h;
        });
    }
    const start = $("cmd-export-start");
    if (start) {
        start.addEventListener("click", () => {
            const args = {
                width: Number($("export-width")?.value || 1920),
                height: Number($("export-height")?.value || 1080),
                fps: Number($("export-fps")?.value || 30),
                videoCodec: String($("export-codec")?.value || ""),
                qualityPreset: String($("export-quality")?.value || ""),
            };
            startExport(args, start);
        });
    }
    const cancel = $("cmd-export-cancel");
    if (cancel) cancel.addEventListener("click", () => runCommand("export.cancel"));
}

async function startExport(args, btn) {
    setBusy(btn, true, "Starting...");
    try {
        await postCommand("export.start", args);
        setStatus("export started: pick the destination in the native dialog");
    } catch (err) {
        showAPIError(err.message);
        setStatus(`export.start failed — ${err.message}`);
    } finally {
        setBusy(btn, false);
        await loadExportStatus();
    }
}

/* The export pane polls at the transport tick cadence while a job is
 * running (or right after one finished, so the result row lands). The
 * module tracks the last observed state itself. */
let lastObservedState = "idle";

export function exportNeedsPolling() {
    return lastObservedState === "running" || lastObservedState === "done";
}
