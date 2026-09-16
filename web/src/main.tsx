import React from "react";
import ReactDOM from "react-dom/client";
import { MantineProvider } from "@mantine/core";
import "@mantine/core/styles.css";
import { BrowserRouter } from "react-router-dom";
import { theme } from "./theme";
import { AppRoutes } from "./AppRoutes";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <MantineProvider theme={theme} forceColorScheme="light">
      <BrowserRouter>
        <AppRoutes />
      </BrowserRouter>
    </MantineProvider>
  </React.StrictMode>,
);
