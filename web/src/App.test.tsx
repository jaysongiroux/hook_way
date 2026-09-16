import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "./theme";
import App from "./App";

function renderApp() {
  return render(
    <MantineProvider theme={theme}>
      <App />
    </MantineProvider>,
  );
}

test("renders the shell header and palette node items", () => {
  renderApp();
  expect(screen.getByText("Hookway Config")).toBeInTheDocument();
  expect(screen.getByText("http trigger")).toBeInTheDocument();
  expect(screen.getByText("filter")).toBeInTheDocument();
  expect(screen.getByText("publish")).toBeInTheDocument();
  expect(screen.getByText("Inspector")).toBeInTheDocument();
});
