import React from "react";
import { configureEditorValidation } from "./shared/wasm/validationMode";
configureEditorValidation("api-fallback");
import ReactDOM from "react-dom/client";
import { UIProvider } from "./shared/ui/UIProvider";
import { BrowserRouter } from "react-router-dom";
import { AppRoutes } from "./AppRoutes";
import { WebsiteAnalytics } from "./website/WebsiteAnalytics";
const root = document.getElementById("root")!;
const app = (
  <React.StrictMode>
    <UIProvider>
      <BrowserRouter>
        <WebsiteAnalytics>
          <AppRoutes />
        </WebsiteAnalytics>
      </BrowserRouter>
    </UIProvider>
  </React.StrictMode>
);
if (root.dataset.prerendered) ReactDOM.hydrateRoot(root, app);
else ReactDOM.createRoot(root).render(app);
