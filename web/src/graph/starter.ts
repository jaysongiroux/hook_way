import productionYaml from "../../../examples/showcase/production.yaml?raw";
import { fromYaml } from "./fromYaml";
import type { GraphDocument } from "./model";

/** The production showcase loaded when the builder has no saved draft. */
export const PRODUCTION_YAML = productionYaml;

export function starterDocument(): GraphDocument {
  return fromYaml(PRODUCTION_YAML);
}
