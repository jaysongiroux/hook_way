import React from "react";
import { configureEditorValidation } from "./shared/wasm/validationMode";
configureEditorValidation("api-fallback");
import ReactDOM from "react-dom/client";
import { UIProvider } from "./shared/ui/UIProvider";
import { BrowserRouter } from "react-router-dom";
import { AppRoutes } from "./AppRoutes";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <UIProvider>
      <BrowserRouter>
        <AppRoutes />
      </BrowserRouter>
    </UIProvider>
  </React.StrictMode>,
);
