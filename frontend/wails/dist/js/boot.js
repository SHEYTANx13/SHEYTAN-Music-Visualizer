/*
 * boot.js — staged boot, full refresh and the bounded polling loop of
 * the SHEYTAN web shell.
 *
 * Staged boot (tokens -> catalog -> surfaces): boot skeletons are
 * bounded loading placeholders shown ONLY while the first load is in
 * flight; they are removed the moment real data lands or the error
 * banner shows. The 2 s tick refreshes the transport snapshot, the
 * media-tools state, the Song DNA pane (only while an analysis can be
 * in flight) and the export job (only while it can be running) —
 * real progress at a bounded cadence, no request storm.
 */

import { isAPIHealthy, markAPIHealthy, showAPIError, setStatus } from "./api.js";
import { applyTokens } from "./tokens.js";
import { state } from "./state.js";
import { loadDestinations, renderDescriptions } from "./tabs.js";
import {
    loadBuildInfo, loadCommands, loadDiagnostics, loadLibrary,
    loadPresets, loadRenderer, loadSettings, loadTransport,
} from "./loaders.js";
import { loadMediaTools, wireMediaTools } from "./mediatools.js";
import { loadNetwork, loadSystem } from "./system.js";
import { dnaNeedsPolling, loadSongDNA } from "./songdna.js";
import { exportNeedsPolling, loadExportStatus, wireExportControls } from "./exportpane.js";
import { wireButtons, wireIntensityOptions, wireApplyPresetButtons } from "./commands.js";
import { wireDragScroll } from "./drag.js";

export async function loadAll() {
    showBootSkeletons();
    try {
        setStatus("loading design tokens...");
        await applyTokens();
        setStatus("loading workspace catalog...");
        await loadDestinations();
        renderDescriptions(state.destinations);
        setStatus("loading surfaces...");
        await Promise.all([
            loadBuildInfo(),
            loadTransport(),
            loadRenderer(),
            loadLibrary(),
            loadSettings(),
            loadPresets(),
            loadCommands(),
            loadDiagnostics(),
            loadSystem(),
            loadNetwork(),
            loadMediaTools(),
            loadSongDNA(),
            loadExportStatus(),
        ]);
        hideBootSkeletons();
        markAPIHealthy();
        setStatus("all surfaces refreshed");
    } catch (err) {
        hideBootSkeletons();
        showAPIError(err.message);
    }
}

/* Boot skeletons: bounded shimmer placeholders shown only while the
 * first load is in flight. They are LOADING states (never fake data):
 * they disappear the moment real data lands or the error banner shows. */
function showBootSkeletons() {
    for (const pane of document.querySelectorAll(".pane")) {
        if (pane.querySelector(".boot-skeleton")) continue;
        const sk = document.createElement("div");
        sk.className = "boot-skeleton";
        sk.setAttribute("aria-hidden", "true");
        for (let i = 0; i < 4; i++) {
            const line = document.createElement("div");
            line.className = "skeleton-line";
            sk.appendChild(line);
        }
        pane.prepend(sk);
    }
}

function hideBootSkeletons() {
    for (const sk of document.querySelectorAll(".boot-skeleton")) sk.remove();
}

export function startTransportPolling() {
    const tick = async () => {
        if (!isAPIHealthy()) return;
        try {
            await loadTransport();
            // The downloads pane polls at the same low cadence: real
            // progress bytes for an in-flight engine download.
            await loadMediaTools();
            // Song DNA lands while an analysis can still be running.
            if (dnaNeedsPolling()) await loadSongDNA();
            // Export progress lands while a job can be running (and one
            // extra beat after a completion so the result row appears).
            if (exportNeedsPolling()) await loadExportStatus();
        } catch (err) {
            showAPIError(err.message);
        }
    };
    setInterval(tick, 2000);
}

export function boot() {
    wireButtons();
    wireIntensityOptions();
    wireApplyPresetButtons();
    wireExportControls();
    wireMediaTools();
    wireDragScroll();
    loadAll();
    startTransportPolling();
}
