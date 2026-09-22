import { expect, test } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { DEST_TYPES } from "../graph/nodeSchema";
import { theme } from "../theme";
import { DestinationMarquee } from "./DestinationMarquee";
import { DESTINATION_PRESENTATIONS, destinationPresentation } from "./destinationIdentity";

function wrap() {
  return render(
    <MantineProvider theme={theme}>
      <DestinationMarquee />
    </MantineProvider>,
  );
}

test("the marquee is driven by the complete destination registry", () => {
  wrap();
  const region = screen.getByRole("region", { name: `Declare once in YAML. Publish to ${DEST_TYPES.length} destination types.` });
  const list = within(region).getByRole("list");
  expect(within(list).getAllByRole("listitem")).toHaveLength(DEST_TYPES.length);
  for (const type of DEST_TYPES) {
    expect(within(list).getByText(destinationPresentation(type).label)).toBeInTheDocument();
  }
});

test("every current destination has a deliberate icon and label", () => {
  expect(Object.keys(DESTINATION_PRESENTATIONS).sort()).toEqual(DEST_TYPES);
  for (const type of DEST_TYPES) {
    expect(destinationPresentation(type).label.length).toBeGreaterThan(0);
    expect(destinationPresentation(type).icon).toBeTypeOf("function");
  }
});

test("each AWS destination is told apart by its icon, not just its label", () => {
  const aws = ["lambda", "s3", "sns", "sqs"];
  const icons = new Set(aws.map((type) => destinationPresentation(type).icon));
  expect(icons.size, "AWS destinations share a generic mark").toBe(aws.length);
});

test("the repeated track is hidden from assistive technology", () => {
  const { container } = wrap();
  const duplicate = container.querySelector('[aria-hidden="true"].hookway-marquee-group');
  expect(duplicate).toBeInTheDocument();
  expect(container.querySelectorAll("svg")).toHaveLength(DEST_TYPES.length * 2);
});
