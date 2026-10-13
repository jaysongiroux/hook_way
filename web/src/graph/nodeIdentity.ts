/**
 * Node fields shared by every type. The Go loader strips these before a
 * NodeSpec decodes its options (see identityKeys in internal/graph/model.go),
 * so they must never appear in the options form or be exported twice.
 *
 * This is a dependency-free leaf module on purpose: the Playwright helpers
 * import it directly, and they cannot load the JSON schema modules that the
 * rest of the graph code pulls in.
 */
export const NODE_IDENTITY_KEYS = new Set(["id", "type", "description"]);
