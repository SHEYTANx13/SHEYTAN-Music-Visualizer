/*
 * api.js — the /api/* client and the honest error surface.
 *
 * When the API is unreachable, every affected surface shows a visible
 * error state (never a fake "ok"). The banner and the topbar chip are
 * owned here; pane modules only call showAPIError/markAPIHealthy.
 */

import { $, setText } from "./dom.js";

let apiHealthy = false;

export function isAPIHealthy() {
    return apiHealthy;
}

export function resetAPIHealth() {
    apiHealthy = false;
}

export async function fetchJSON(path, options) {
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

export async function postCommand(id, args) {
    return fetchJSON("/api/command", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id, args: args || {} }),
    });
}

export function showAPIError(message) {
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

export function markAPIHealthy() {
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

export function setStatus(message) {
    setText($("status-message"), message);
    setText($("status-checked"), `checked ${new Date().toLocaleTimeString()}`);
}
