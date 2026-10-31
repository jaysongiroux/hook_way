import { createContext, useContext } from "react";

export type TelemetryEvent =
  | "builder_opened"
  | "quick_start_copied"
  | "config_exported"
  | "github_opened";
export type TelemetryProperties = {
  location?: "header" | "footer" | "hero" | "quick_start" | "builder";
  method?: "copy" | "download";
  format?: "yaml" | "zip";
};
export const TelemetryContext = createContext<
  (event: TelemetryEvent, properties?: TelemetryProperties) => void
>(() => {});
export const useTelemetry = () => useContext(TelemetryContext);
