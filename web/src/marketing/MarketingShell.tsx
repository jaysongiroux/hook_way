import { Box } from "@mantine/core";
import type { ReactNode } from "react";
import { MARKETING_CSS } from "./marketingStyles";
import { SiteFooter } from "./SiteFooter";
import { SiteHeader } from "./SiteHeader";
import { MarketingMotion } from "./MarketingMotion";
import { DARK } from "../tokens";

/** Shared chrome for marketing routes: tokens CSS, sticky header, footer. */
export function MarketingShell({
  children,
  animated = false,
}: {
  children: ReactNode;
  animated?: boolean;
}) {
  return (
    <Box
      className={`hookway-site-shell${animated ? " hookway-marketing-page" : ""}`}
      style={{ background: DARK.bg, minHeight: "100vh" }}
    >
      <style>{MARKETING_CSS}</style>
      <SiteHeader />
      <MarketingMotion enabled={animated}>{children}</MarketingMotion>
      <SiteFooter />
    </Box>
  );
}
