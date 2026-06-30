// Client-side mirror of shared.ValidateSessionName (^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$).
// The hub + agent re-validate at their boundaries; this only gates UI affordances
// (the create-form submit button, the inline rename editor) for fast feedback.
export const NAME_RE = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/;

export const isValidSessionName = (name: string): boolean => NAME_RE.test(name);

// Shared human-readable hint shown when a name fails the rule.
export const SESSION_NAME_HINT =
  "Letters, digits, “_” and “-” only; must start with a letter or digit (max 64).";
