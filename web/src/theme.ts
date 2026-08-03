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

export const theme = createTheme({
  primaryColor: "brand",
  primaryShade: 6,
  colors: { brand },
  black: "#152033", // Hometap text color
  fontFamily:
    "system-ui, -apple-system, Segoe UI, Roboto, Helvetica, Arial, sans-serif",
});
