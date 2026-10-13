import { Document } from "yaml";

export function toYaml(config: unknown): string {
  return new Document(config).toString();
}
