import { createTheme, type MantineColorsTuple } from "@mantine/core";

// Brand primary derived from Hometap Accent 1 (#366CED). Mantine requires a
// 10-shade tuple; index 6 is the base interactive shade.
const brand: MantineColorsTuple = [
  "#eef3ff",
  "#dce4f5",
  "#b6c6e8",
  "#8ea6db",
  "#6b8bd0",
  "#5479ca",
  "#366CED",
  "#2f5fd6",
  "#2954bf",
  "#2149a8",
];

// Mantine builds every dark surface from colors.dark. Its default ramp is
// neutral grey, which clashed with the navy canvas, so these steps map onto
// the tokens in src/tokens.ts: 6 is the hairline, 7 the card, 8 the body, 9
// the recessed band.
const dark: MantineColorsTuple = [
  "#E8EDF7",
  "#C6D0E3",
  "#A3B4D3",
  "#8FA3C7",
  "#6D82AB",
  "#4A5C80",
  "#2A3A57",
  "#1B2840",
  "#152033",
  "#0F1826",
];

export const theme = createTheme({
  primaryColor: "brand",
  primaryShade: 6,
  colors: { brand, dark },
  black: "#152033", // Hometap text color
  fontFamily:
    "system-ui, -apple-system, Segoe UI, Roboto, Helvetica, Arial, sans-serif",
});
