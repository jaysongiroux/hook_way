import createClient from "openapi-fetch";
import type { paths } from "./generated/replay";
import { clientOptions, type APIClientOptions } from "./options";

export function createReplayClient(options: APIClientOptions = {}) {
  return createClient<paths>(clientOptions(options));
}
