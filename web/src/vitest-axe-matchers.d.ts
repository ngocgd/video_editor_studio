// vitest-axe 0.1.0 ships its `toHaveNoViolations` matcher type under a
// global `Vi.Assertion` namespace built for an older vitest major; vitest 5
// augments `vitest`'s own `Assertion` interface instead, so the matcher is
// declared locally here to match the version actually installed.
declare module "vitest" {
  interface Assertion<T = unknown> {
    toHaveNoViolations(): T;
  }
}

export {};
