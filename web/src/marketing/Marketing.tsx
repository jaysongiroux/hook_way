import { useDocumentTitle } from "@mantine/hooks";
import { CodeBand } from "./CodeBand";
import { CtaBand } from "./CtaBand";
import { DestinationMarquee } from "./DestinationMarquee";
import { DocsLinks } from "./DocsLinks";
import { Features } from "./Features";
import { FlowDiagram } from "./FlowDiagram";
import { Hero } from "./Hero";
import { MarketingShell } from "./MarketingShell";
import { Production } from "./Production";

// Marketing is the public overview served at /. It explains what Hookway is
// and hands off to the builder or to the docs on GitHub; it never restates
// them. Bands alternate between the canvas and the recessed surface so the
// page has a rhythm rather than one repeated card grid.
export function Marketing() {
  useDocumentTitle("Hookway — Receive, verify, and route webhooks.");
  return (
    <MarketingShell>
      <Hero />
      <DestinationMarquee />
      <Features />
      <CodeBand />
      <FlowDiagram />
      <Production />
      <DocsLinks />
      <CtaBand />
    </MarketingShell>
  );
}
