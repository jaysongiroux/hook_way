import { Box } from "@mantine/core";
import { useDocumentTitle } from "@mantine/hooks";
import { CodeBand } from "./CodeBand";
import { CtaBand } from "./CtaBand";
import { Destinations } from "./Destinations";
import { DocsLinks } from "./DocsLinks";
import { Features } from "./Features";
import { FlowDiagram } from "./FlowDiagram";
import { Hero } from "./Hero";
import { MARKETING_CSS } from "./marketingStyles";
import { Production } from "./Production";
import { SiteFooter } from "./SiteFooter";
import { SiteHeader } from "./SiteHeader";
import { StatRow } from "./StatRow";
import { DARK } from "../tokens";

// Marketing is the public overview served at /. It explains what Hookway is
// and hands off to the builder or to the docs on GitHub; it never restates
// them. Bands alternate between the canvas and the recessed surface so the
// page has a rhythm rather than one repeated card grid.
export function Marketing() {
  useDocumentTitle("Hookway — Webhooks in, anywhere out.");
  return (
    <Box style={{ background: DARK.bg, minHeight: "100vh" }}>
      <style>{MARKETING_CSS}</style>
      <SiteHeader />
      <main>
        <Hero />
        <StatRow />
        <Features />
        <CodeBand />
        <FlowDiagram />
        <Production />
        <Destinations />
        <DocsLinks />
        <CtaBand />
      </main>
      <SiteFooter />
    </Box>
  );
}
