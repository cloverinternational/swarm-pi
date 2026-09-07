import { describe, expect, it } from "vitest";
import {
  configuredExtraTools,
  gateActiveTools,
  REQUIRED_PI_EXTENSION_TOOLS,
  swarmSurfaceFor,
} from "../../lib/runtime/swarm-tool-gating.ts";

describe("explicit Pi extension tool gating", () => {
  it("always allows required policy tools and keeps other extras explicit", () => {
    expect(REQUIRED_PI_EXTENSION_TOOLS).toEqual(["change_context"]);
    expect([
      ...configuredExtraTools({
        PI_SWARM_EXTRA_TOOLS: " local_a ,,local_a ",
      }),
    ]).toEqual(["local_a"]);

    const env = { PI_SWARM_EXTRA_TOOLS: "missing_local" };
    const surface = swarmSurfaceFor({
      interactive: true,
      home: "/nonexistent",
      env,
    });
    expect(surface.has("change_context")).toBe(true);
    expect(surface.has("missing_local")).toBe(true);
    expect(surface.has("unlisted_local")).toBe(false);

    // The global allowlist can only retain tools that Pi actually registered.
    expect(
      gateActiveTools(
        ["Bash", "change_context", "unlisted_local"],
        { interactive: true, home: "/nonexistent", env },
        env,
      ),
    ).toEqual(["Bash", "change_context"]);

    // A persisted /tools or prompt-context allowlist cannot strand mutation
    // tools without the registered policy tool they require.
    expect(
      gateActiveTools(
        ["Bash"],
        { interactive: true, home: "/nonexistent", env: {} },
        {},
        ["Bash", "change_context"],
      ),
    ).toEqual(["Bash", "change_context"]);
    expect(
      gateActiveTools(
        ["unlisted_local"],
        { interactive: true, home: "/nonexistent", env: {} },
        {},
        ["unlisted_local", "change_context"],
      ),
    ).toEqual(["change_context"]);
    expect(
      gateActiveTools(
        ["Bash"],
        { interactive: true, home: "/nonexistent", env: {} },
        { PI_SWARM_TOOL_SURFACE: "all" },
        ["Bash", "change_context"],
      ),
    ).toEqual(["Bash", "change_context"]);
    // Do not fabricate the policy tool when no extension registered it.
    expect(
      gateActiveTools(
        ["Bash"],
        { interactive: true, home: "/nonexistent", env: {} },
        {},
        ["Bash"],
      ),
    ).toBeUndefined();
  });
});
