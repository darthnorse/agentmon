import "@testing-library/jest-dom/vitest";
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

// @testing-library/react auto-cleanup requires globals:true in vitest config.
// Since we don't use globals, wire it explicitly here.
afterEach(cleanup);

// jsdom lacks matchMedia; default to "not matched" (mobile-first components decide).
if (!window.matchMedia) {
  // @ts-ignore minimal shim
  window.matchMedia = (query: string) => ({
    matches: false, media: query, onchange: null,
    addEventListener: () => {}, removeEventListener: () => {},
    addListener: () => {}, removeListener: () => {}, dispatchEvent: () => false,
  });
}

// jsdom lacks ResizeObserver (used by the terminal fit logic).
if (!globalThis.ResizeObserver) {
  // @ts-ignore minimal shim
  globalThis.ResizeObserver = class {
    observe() {} unobserve() {} disconnect() {}
  };
}
