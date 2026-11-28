/*
 * state.js — the shared client state of the SHEYTAN web shell.
 *
 * One module-owned store; every pane module reads from it after its
 * loader resolved. No cross-module mutable exports beyond this object.
 */

export const state = {
    destinations: [],
    commands: null,
    settings: null,
    presets: [],
    transport: null,
    build: null,
    library: null,
    media: null,
    dna: null,
    worlds: null,
    exportJob: null,
};
