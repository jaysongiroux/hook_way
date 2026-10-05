// The deployed product has no editor API. The standalone marketing/Go editor
// explicitly retains its API fallback, while both use the same local WASM bridge.
let mode: "local" | "api-fallback" = "api-fallback";
export function configureEditorValidation(value: typeof mode) { mode = value; }
export function editorAPIFallbackEnabled() { return mode === "api-fallback"; }
export const VALIDATOR_RETRY_EVENT = "hookway:validator-retry";
