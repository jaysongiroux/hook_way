import React from "react";
import ReactDOM from "react-dom/client";
import { Alert, Button, Stack } from "@mantine/core";
import { BrowserRouter } from "react-router-dom";
import { UIProvider } from "../shared/ui/UIProvider";
import { configureEditorValidation } from "../shared/wasm/validationMode";
import { ReplayConsole } from "./ReplayConsole";
import { loadRuntimeConfig } from "./runtimeConfig";

configureEditorValidation("local");
const root = ReactDOM.createRoot(document.getElementById("root")!);
async function start() {
  try {
    const config = await loadRuntimeConfig();
    root.render(<React.StrictMode><UIProvider><BrowserRouter><ReplayConsole apiUrl={config.replayApiUrl} /></BrowserRouter></UIProvider></React.StrictMode>);
  } catch (error) {
    root.render(<UIProvider><Stack p="xl" align="flex-start"><Alert color="red" title="Product configuration unavailable">{error instanceof Error ? error.message : "Runtime configuration is unavailable."} Set REPLAY_API_URL on the frontend deployment, then retry.</Alert><Button onClick={() => void start()}>Retry configuration</Button></Stack></UIProvider>);
  }
}
void start();
