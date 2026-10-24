import js from "@eslint/js";
import tseslint from "typescript-eslint";

export default tseslint.config(
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["src/**/*.ts", "test/**/*.ts"],
    rules: {
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_" }],
      "@typescript-eslint/no-explicit-any": "error",
      "no-console": "error",
      eqeqeq: ["error", "always"],
      curly: ["error", "multi-line"],
    },
  },
  {
    ignores: ["dist/**", "out/**", "node_modules/**", "*.mjs"],
  }
);
