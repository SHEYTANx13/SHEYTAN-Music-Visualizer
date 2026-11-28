/*
 * mediatools.js — app-managed FFmpeg/FFprobe downloads and the
 * requirements mirror.
 *
 * Run 45532045379 contract: the panes show the download FILE SIZE,
 * the downloaded/total progress and the transfer speed straight from
 * the Go mediatools manager (pinned manifest, digest verification,
 * allowlisted hosts). The UI never invents progress: no bytes field
 * is faked, an unreachable API renders an explicit error state.
 */

import { fetchJSON, showAPIError, setStatus } from "./api.js";
import { $, setText, renderKV, setBusy, fmtBytes } from "./dom.js";
import { state } from "./state.js";

const MEDIA_ACTIVE_STATES = new Set([
    "downloading", "verifying", "installing",
]);

export function renderMediaTools(snap) {
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

    const st = String(snap.state || "unknown");
    stateEl.textContent = st.replace(/-/g, " ");
    stateEl.dataset.state = st.replace(/-/g, "_");

    const active = MEDIA_ACTIVE_STATES.has(st);
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
        || st === "unsupported-platform"
        || (!snap.installed && st !== "not-installed" && st !== "failed" && st !== "repair-needed" && st !== "ready");
    if (cancelBtn) cancelBtn.disabled = !active;
}

export async function loadMediaTools() {
    const snap = await fetchJSON("/api/mediatools");
    state.media = snap;
    renderMediaTools(snap);
    renderRequirementsMirror(snap);
}

/* Requirements pane mirrors the SAME real manager snapshot the
 * downloads pane consumes (one fetch, one source of truth). */
export function renderRequirementsMirror(snap) {
    const stateEl = $("req-mediatools-state");
    if (!stateEl) return;
    const st = String(snap.state || "unknown");
    stateEl.textContent = st.replace(/-/g, " ");
    stateEl.dataset.state = st.replace(/-/g, "_");
    const rows = [
        ["Installed", snap.installed ? "yes" : "no"],
        ["Pinned pack", snap.pack && snap.pack.version ? String(snap.pack.version) : (snap.pack ? "pinned" : "unknown")],
        ["Pinned size", snap.pack && snap.pack.sizeBytes ? fmtBytes(Number(snap.pack.sizeBytes)) : "unknown"],
    ];
    if (snap.state === "downloading" && Number(snap.totalBytes) > 0) {
        rows.push(["Progress", fmtBytes(Number(snap.downloadedBytes)) + " / " + fmtBytes(Number(snap.totalBytes))]);
    }
    renderKV($("req-mediatools-kv"), rows);
    setText($("req-mediatools-detail"), snap.lastError ? "error: " + snap.lastError : (snap.detail || ""));
}

async function mediaAction(path, btn, busyLabel) {
    setBusy(btn, true, busyLabel);
    try {
        const snap = await fetchJSON(path, { method: "POST" });
        state.media = snap;
        renderMediaTools(snap);
        renderRequirementsMirror(snap);
        setStatus("media tools: " + (snap.state || "updated"));
    } catch (err) {
        showAPIError(err.message);
        setStatus("media tools action failed: " + err.message);
    } finally {
        setBusy(btn, false);
        // The manager state owns the real button availability; refresh
        // the disabled states from the snapshot instead of leaving the
        // busy-reset guesses in place.
        if (state.media) renderMediaTools(state.media);
    }
}

export function wireMediaTools() {
    const check = $("mediatools-check");
    const install = $("mediatools-install");
    const cancel = $("mediatools-cancel");
    if (check) check.addEventListener("click", () => mediaAction("/api/mediatools/check", check, "Checking..."));
    if (install) install.addEventListener("click", () => mediaAction("/api/mediatools/install", install, "Starting..."));
    if (cancel) cancel.addEventListener("click", () => mediaAction("/api/mediatools/cancel", cancel, "Cancelling..."));
}
