import reactConfig from "@multica/eslint-config/react";
import { ICON_LIBRARY_IMPORT_PATTERNS } from "@multica/eslint-config/base";

export default [
  ...reactConfig,
  {
    files: ["**/*.{ts,tsx}"],
    rules: {
      "no-restricted-imports": ["error", {
        patterns: [
          ...ICON_LIBRARY_IMPORT_PATTERNS,
          {
            regex: "^(?:@multica/(?:ui|views)|(?:\\.\\./)+(?:ui|views)|react-dom)(?:/|$)",
            message: "Keep core independent of UI packages and React DOM.",
          },
        ],
      }],
    },
  },
];
