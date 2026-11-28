/*
 * system.js — the REAL machine snapshot and the secure network surface.
 *
 * System facts come from sysprofile via /api/system: OS, CPU, memory,
 * power, audio and renderer facts with explicit provenance. The
 * network policy block is served from the Go origin authority (ghreq).
 * The probe NEVER runs automatically: it fires only when the user
 * clicks the test button, is bounded by a hard backend timeout,
 * targets only the allowlisted origin and sends no credentials.
 */

import { fetchJSON, showAPIError, setStatus } from "./api.js";
import { $, setText, renderKV, setBusy, factText, quantityText } from "./dom.js";

export async function loadSystem() {
    const p = await fetchJSON("/api/system");
    renderKV($("system-kv"), [
        ["OS", factText(p.os && p.os.family) + " " + factText(p.os && p.os.version) + " (" + factText(p.os && p.os.arch) + ")"],
        ["CPU", factText(p.cpu && p.cpu.model)],
        ["Logical CPUs", quantityText(p.cpu && p.cpu.logicalCPUs)],
        ["Parallelism", quantityText(p.cpu && p.cpu.effectiveParallelism)],
        ["SIMD features", factText(p.cpu && p.cpu.features && p.cpu.features.present)],
        ["Memory total", p.memory && p.memory.totalBytes ? quantityText(p.memory.totalBytes) + " bytes" : "unknown"],
        ["Memory available", p.memory && p.memory.availableBytes ? quantityText(p.memory.availableBytes) + " bytes" : "unknown"],
        ["Power", factText(p.power && p.power.state)],
        ["Audio backend", factText(p.audio && p.audio.backend)],
        ["Renderer decision", (p.render && p.render.decision) || "unknown"],
        ["Renderer verified", p.render && p.render.verified ? "yes (frames executed and validated)" : "no (evidence only)"],
    ]);
    setText($("system-note"),
        "Collected " + (p.collectedAt ? new Date(p.collectedAt).toLocaleString() : "at an unknown time")
        + " by " + (p.collector || "unknown collector") + ". Facts unknown to this build state their exact reason instead of a guess.");
}

export async function loadNetwork() {
    const status = await fetchJSON("/api/network");
    const pol = status.policy || {};
    renderKV($("network-policy-kv"), [
        ["Transport security", pol.httpsOnly ? "HTTPS only" : "not enforced"],
        ["Allowed hosts", (pol.allowedHosts || []).join(", ") || "(none)"],
        ["Redirect limit", String(pol.maxRedirects) + " hops, same policy"],
        ["Credentials sent", pol.credentialsSent ? "YES" : "never"],
        ["Private addresses", pol.privateAddrsBlocked || "blocked"],
        ["Downloads", (pol.digestVerified ? "digest verified" : "digest unknown")
            + ", " + (pol.sizeBoundsApply ? "size bounded" : "unbounded")],
        ["Auto probe", pol.autoProbeEnabled ? "enabled" : "disabled (user-triggered only)"],
    ]);
    const chip = $("network-probe-state");
    if (chip) {
        chip.textContent = (status.probeStatus || "never-probed").replace(/-/g, " ");
        chip.dataset.state = String(status.probeStatus || "never-probed");
    }
    renderProbeResult(status.lastProbe);
    setText($("network-note"),
        "Every outbound request is HTTPS-only against the allowlisted hosts above; redirect hops follow the same policy and credentials are never forwarded. The connection test runs only when you ask for it.");
}

function renderProbeResult(probe) {
    const chip = $("network-probe-state");
    if (!probe) {
        renderKV($("network-probe-kv"), []);
        return;
    }
    if (chip) {
        chip.textContent = probe.reachable ? "reachable" : "unreachable";
        chip.dataset.state = probe.reachable ? "reachable" : "unreachable";
    }
    const rows = [
        ["Outcome", probe.reachable ? "origin reachable" : "origin unreachable"],
        ["Checked", probe.checkedAt ? new Date(probe.checkedAt).toLocaleTimeString() : "unknown"],
    ];
    if (probe.httpStatus) rows.push(["HTTP status", String(probe.httpStatus)]);
    if (probe.latencyMs) rows.push(["Latency", probe.latencyMs + " ms"]);
    if (probe.error) rows.push(["Detail", probe.error]);
    renderKV($("network-probe-kv"), rows);
}

export async function probeNetwork() {
    const btn = $("network-probe");
    setBusy(btn, true, "Testing...");
    try {
        const status = await fetchJSON("/api/network/probe", { method: "POST" });
        const chip = $("network-probe-state");
        if (chip) {
            chip.textContent = (status.probeStatus || "probed").replace(/-/g, " ");
            chip.dataset.state = String(status.probeStatus || "probed");
        }
        renderProbeResult(status.lastProbe);
        if (status.lastProbe && status.lastProbe.reachable) {
            setStatus("connection test: origin reachable");
        } else {
            setStatus("connection test: origin unreachable (" + (status.lastProbe && status.lastProbe.error ? status.lastProbe.error : "no detail") + ")");
        }
    } catch (err) {
        showAPIError(err.message);
        setStatus("connection test failed: " + err.message);
    } finally {
        setBusy(btn, false);
        await loadNetwork();
    }
}
