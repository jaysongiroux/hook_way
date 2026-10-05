import type { PropsWithChildren } from "react";
import { MantineProvider } from "@mantine/core";
import "@mantine/core/styles.css";
import { theme, cssVariablesResolver } from "./theme";

export function UIProvider({ children }: PropsWithChildren) {
  return (
    <MantineProvider
      theme={theme}
      cssVariablesResolver={cssVariablesResolver}
      forceColorScheme="light"
    >
      {children}
    </MantineProvider>
  );
}
