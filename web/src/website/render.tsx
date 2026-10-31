import { StrictMode } from "react";
import { renderToString } from "react-dom/server";
import { StaticRouter } from "react-router-dom";
import { UIProvider } from "../shared/ui/UIProvider";
import { AppRoutes } from "../AppRoutes";
import { WebsiteAnalytics } from "./WebsiteAnalytics";
export function renderPage(path: string) {
  return renderToString(
    <StrictMode>
      <UIProvider>
        <StaticRouter location={path}>
          <WebsiteAnalytics>
            <AppRoutes />
          </WebsiteAnalytics>
        </StaticRouter>
      </UIProvider>
    </StrictMode>,
  );
}
