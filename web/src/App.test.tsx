import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";

describe("application tenant context", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it("displays the configured tenant without an editable tenant input", async () => {
    vi.stubEnv("VITE_TFLIVE_TENANT_ID", "tenant_123");
    const { default: App } = await import("./App");

    const markup = renderToStaticMarkup(<App />);

    expect(markup).toContain('data-testid="tenant-context"');
    expect(markup).toContain(">tenant_123</span>");
    expect(markup).not.toContain('value="tenant_123"');
  });
});
