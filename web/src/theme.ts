import {
  createTheme,
  type MantineColorsTuple,
  type MantineTheme,
  type CSSVariablesResolver,
  type AlertProps,
} from "@mantine/core";
import { DARK } from "./tokens";

const brand: MantineColorsTuple = [
  "#eef3f8",
  "#d4e0ed",
  "#a9c0d8",
  "#7a9dc0",
  "#527eab",
  "#366696",
  "#0C447C",
  "#0a3a69",
  "#083056",
  "#062443",
];

const dark: MantineColorsTuple = [
  "#14181F",
  "#3E4A59",
  "#5C6778",
  "#7A8494",
  "#9AA3B0",
  "#C5CBD4",
  "#D8DDE4",
  "#EBEEF2",
  "#F4F5F7",
  "#FFFFFF",
];

export const theme = createTheme({
  primaryColor: "brand",
  primaryShade: 6,
  colors: { brand, dark },
  black: DARK.text,
  white: DARK.surface,
  defaultRadius: "sm",
  fontFamily: '"Public Sans", system-ui, -apple-system, "Segoe UI", sans-serif',
  fontFamilyMonospace: '"Source Code Pro", ui-monospace, Menlo, monospace',
  headings: {
    fontFamily:
      '"Public Sans", system-ui, -apple-system, "Segoe UI", sans-serif',
    fontWeight: "650",
  },
  components: {
    Input: {
      styles: {
        input: { "--input-placeholder-color": DARK.muted },
      },
    },
    InputWrapper: {
      styles: {
        description: { color: DARK.muted },
        error: { color: "#c92a2a" },
        required: { color: "#c92a2a" },
      },
    },
    Alert: {
      styles: (_theme: MantineTheme, props: AlertProps) => ({
        title: {
          color:
            props.color === "red"
              ? "#c92a2a"
              : props.color === "yellow"
                ? "#7d4b00"
                : undefined,
        },
      }),
    },
    NumberInput: {
      defaultProps: { hideControls: true },
    },
    Button: {
      defaultProps: { fw: 600, radius: "sm" },
    },
    Modal: {
      defaultProps: {
        radius: "sm",
        overlayProps: { backgroundOpacity: 0.35, blur: 0 },
      },
      styles: {
        header: { borderBottom: `1px solid ${DARK.border}` },
        title: { fontWeight: 600 },
      },
    },
    Menu: {
      defaultProps: { withinPortal: true, zIndex: 400, radius: "sm" },
    },
  },
});

// Mantine applies c="dimmed" as an inline variable, so component styles cannot
// override it. Set the shared light-scheme variable at its source.
export const cssVariablesResolver: CSSVariablesResolver = () => ({
  variables: {},
  light: {
    "--mantine-color-dimmed": DARK.muted,
    "--mantine-color-red-text": "#c92a2a",
  },
  dark: {},
});
