import { resolve } from "path";
import { configDefaults, defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": resolve(__dirname, "src/renderer/src"),
    },
  },
  test: {
    globals: true,
    include: ["src/**/*.test.{ts,tsx}", "scripts/**/*.test.mjs"],
    exclude: [...configDefaults.exclude, "scripts/package.test.mjs"],
    environment: "jsdom",
    setupFiles: ["./test/setup.ts"],
    passWithNoTests: true,
  },
});
