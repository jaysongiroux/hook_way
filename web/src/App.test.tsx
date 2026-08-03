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
  expect(screen.getByText("Endpoint")).toBeInTheDocument();
  expect(screen.getByText("Filter")).toBeInTheDocument();
  expect(screen.getByText("Destination")).toBeInTheDocument();
  expect(screen.getByText("Inspector")).toBeInTheDocument();
});
