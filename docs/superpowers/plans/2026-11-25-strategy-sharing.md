# Strategy publication and copying

User request: label default strategies, expose publication controls, and let other users copy public strategies.

- Distinguish per-user built-in presets from account-active/default selection and global system templates. Preserve ownership and existing defaults.
- Provide a publication-only owner endpoint so changing visibility never rewrites trading parameters or requires stopping a trader. Public listing and copies strip typed data-provider credentials. A strategy is cross-user copyable only when public and configuration-visible.
- Copy into a new private, inactive, non-preset strategy owned by the caller. Do not share executions, accounts, credentials or live bindings. Keep private strategy access denied.
- Add a strategy-market view and publication card in Strategies with existing visual styles. Public rows expose copy only for visible configurations; reload copied strategy into the personal editor. All defaults get explicit badges.
- Verify access controls, redaction, publish/unpublish, hidden-config rejection and independent copy ownership with backend tests; verify controls and payloads with frontend tests. Review, commit/push, deploy with backup and health checks.
