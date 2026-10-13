import createClient from "openapi-fetch";
import type { paths } from "./generated/editor";
import { clientOptions, type APIClientOptions } from "./options";

export function createEditorClient(options: APIClientOptions = {}) {
  return createClient<paths>(clientOptions(options));
}
