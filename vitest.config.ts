import { defineConfig } from "vitest/config";

// Single runner for the whole repository. Package tests stay beside their
// package; Pi extension/lib tests live under .pi/test; cross-cutting suites
// under tests/. Vendored trees and build output are never scanned.
export default defineConfig({
  test: {
    include: ["packages/**/test/**/*.test.ts", ".pi/test/**/*.test.ts", "tests/**/*.test.ts"],
    exclude: ["**/node_modules/**", "**/dist/**", "vendor/**", "artifacts/**"],
  },
});
