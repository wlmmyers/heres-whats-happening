import js from '@eslint/js';
import globals from 'globals';
import tseslint from 'typescript-eslint';
import { defineConfig, globalIgnores } from 'eslint/config';

export default defineConfig([
  globalIgnores(['dist', '.mastra']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [js.configs.recommended, tseslint.configs.recommended],
    languageOptions: {
      globals: globals.node,
    },
    rules: {
      // Several handlers take parameters they don't use but can't drop, because
      // the position is fixed by an external signature (awslambda's
      // streamifyResponse, HttpResponseStream.from). Underscore marks those
      // deliberate.
      '@typescript-eslint/no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
        },
      ],
    },
  },
  {
    // Test doubles stand in for SDK clients and Mastra run results whose real
    // types are large, generated, and beside the point of the assertion. `any`
    // is the shape of a stub here, not sloppiness in shipped code.
    files: ['**/*.test.ts'],
    rules: {
      '@typescript-eslint/no-explicit-any': 'off',
    },
  },
]);
