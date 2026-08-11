// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useInView } from "./useInView";

type ObserverCallback = (entries: { isIntersecting: boolean }[]) => void;

let callbacks: ObserverCallback[] = [];
let disconnected = 0;

beforeEach(() => {
  callbacks = [];
  disconnected = 0;
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      constructor(cb: ObserverCallback) {
        callbacks.push(cb);
      }
      observe() {}
      disconnect() {
        disconnected += 1;
      }
      unobserve() {}
    }
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function Probe() {
  const { ref, visible } = useInView<HTMLDivElement>();
  return (
    <div ref={ref} data-testid="probe" data-visible={visible}>
      content
    </div>
  );
}

describe("useInView", () => {
  it("starts not visible", () => {
    render(<Probe />);
    expect(screen.getByTestId("probe").dataset.visible).toBe("false");
  });

  it("becomes visible once the element intersects", () => {
    render(<Probe />);
    act(() => {
      callbacks[0]([{ isIntersecting: true }]);
    });
    expect(screen.getByTestId("probe").dataset.visible).toBe("true");
  });

  it("stays visible after the element leaves the viewport", () => {
    render(<Probe />);
    act(() => {
      callbacks[0]([{ isIntersecting: true }]);
    });
    act(() => {
      callbacks[0]([{ isIntersecting: false }]);
    });
    expect(screen.getByTestId("probe").dataset.visible).toBe("true");
  });

  it("disconnects the observer on unmount", () => {
    const view = render(<Probe />);
    view.unmount();
    expect(disconnected).toBeGreaterThan(0);
  });
});
