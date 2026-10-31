import { CodeBand } from "./CodeBand";
import { CtaBand } from "./CtaBand";
import { DestinationMarquee } from "./DestinationMarquee";
import { DocsLinks } from "./DocsLinks";
import { Explore } from "./Explore";
import { Features } from "./Features";
import { FlowDiagram } from "./FlowDiagram";
import { Hero } from "./Hero";
import { MarketingShell } from "./MarketingShell";
import { MCPGateway } from "./MCPGateway";
import { HTTPGateway } from "./HTTPGateway";
import { Production } from "./Production";
import { ReplayOperations } from "./ReplayOperations";

// Marketing is the public overview served at /. It explains what Hookway is
// and hands off to the builder or to the docs on GitHub; it never restates
// them. Bands alternate between the canvas and the recessed surface so the
// page has a rhythm rather than one repeated card grid.
export function Marketing() {
  return (
    <MarketingShell animated>
      <Hero />
      <DestinationMarquee />
      <HTTPGateway />
      <MCPGateway />
      <Features />
      <CodeBand />
      <FlowDiagram />
      <Production />
      <ReplayOperations />
      <Explore />
      <DocsLinks />
      <CtaBand />
    </MarketingShell>
  );
}
