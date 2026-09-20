import { Box } from "@mantine/core";
import type { ReactNode } from "react";
import { MARKETING_CSS } from "./marketingStyles";
import { SiteFooter } from "./SiteFooter";
import { SiteHeader } from "./SiteHeader";
import { DARK } from "../tokens";

/** Shared chrome for marketing routes: tokens CSS, sticky header, footer. */
export function MarketingShell({ children }: { children: ReactNode }) {
  return (
    <Box style={{ background: DARK.bg, minHeight: "100vh" }}>
      <style>{MARKETING_CSS}</style>
      <SiteHeader />
      <main>{children}</main>
      <SiteFooter />
    </Box>
  );
}
