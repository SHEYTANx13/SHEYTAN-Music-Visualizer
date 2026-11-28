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
    // Editing tools (run 46168717789): the shared format args flow into
    // BOTH export.start and export.preview; the subtitle attachment is
    // native-dialog state owned by Go (the web layer sees the state
    // chip only, never a path).
    const subtitleBtn = $("cmd-export-subtitle");
    if (subtitleBtn) {
        subtitleBtn.addEventListener("click", async () => {
            setBusy(subtitleBtn, true, "Opening...");
            try {
                await postCommand("export.subtitle", {});
                subtitleAttached = true;
                setText($("export-subtitle-state"), "subtitles attached (burned into the export)");
                const clear = $("cmd-export-subtitle-clear");
                if (clear) clear.hidden = false;
                setStatus("subtitles: file picked — it will be burned into exports and previews");
            } catch (err) {
                showAPIError(err.message);
            } finally {
                setBusy(subtitleBtn, false);
            }
        });
    }
    const clearBtn = $("cmd-export-subtitle-clear");
    if (clearBtn) {
        clearBtn.addEventListener("click", async () => {
            try {
                await postCommand("export.subtitle.clear", {});
                subtitleAttached = false;
                setText($("export-subtitle-state"), "no subtitles attached");
                clearBtn.hidden = true;
                setStatus("subtitles removed");
            } catch (err) {
                showAPIError(err.message);
            }
        });
    }
    const previewBtn = $("cmd-export-preview");
    if (previewBtn) {
        previewBtn.addEventListener("click", () => {
            startExport(buildSharedExportArgs({ preview: 8 }), previewBtn, "export.preview", "preview started: pick the destination, the sample encodes with your exact settings");
        });
    }
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
            startExport(buildSharedExportArgs(), start);
        });
    }
    const cancel = $("cmd-export-cancel");
    if (cancel) cancel.addEventListener("click", () => runCommand("export.cancel"));
}

/* The format + editing args shared by export.start and export.preview:
 * one builder, so a preview is EXACTLY what the full export will do. */
function buildSharedExportArgs(extra = {}) {
    return {
        width: Number($("export-width")?.value || 1920),
        height: Number($("export-height")?.value || 1080),
        fps: Number($("export-fps")?.value || 30),
        videoCodec: String($("export-codec")?.value || ""),
        qualityPreset: String($("export-quality")?.value || ""),
        trimStart: Number($("export-trim-start")?.value || 0),
        trimDuration: Number($("export-trim-duration")?.value || 0),
        ...extra,
    };
}

async function startExport(args, btn, command = "export.start", okMessage = "export started: pick the destination in the native dialog") {
    setBusy(btn, true, "Starting...");
    try {
        await postCommand(command, args);
        setStatus(okMessage);
    } catch (err) {
        showAPIError(err.message);
        setStatus(`${command} failed — ${err.message}`);
    } finally {
        setBusy(btn, false);
        await loadExportStatus();
    }
}

/* Subtitle attachment state mirrors the Go-owned native-dialog state;
 * the web layer never receives the path itself. */
let subtitleAttached = false;

/* The export pane polls at the transport tick cadence while a job is
 * running (or right after one finished, so the result row lands). The
 * module tracks the last observed state itself. */
let lastObservedState = "idle";

export function exportNeedsPolling() {
    return lastObservedState === "running" || lastObservedState === "done";
}
