import "fake-indexeddb/auto";
import "@testing-library/jest-dom/vitest";
import { vi } from "vitest";

// Mantine reads window.matchMedia and ResizeObserver at render time; jsdom
// provides neither, so stub them for the test environment.
Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }),
});

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
window.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;

// Mantine 9 listens for font loading to resize autosizing textareas.
// jsdom has no FontFaceSet; keep real event registration in the test DOM.
Object.defineProperty(document, "fonts", { value: new EventTarget() });
