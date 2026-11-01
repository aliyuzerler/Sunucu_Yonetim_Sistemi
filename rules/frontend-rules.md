# Frontend Rules

- React + TypeScript strict mode; reusable accessible components.
- UI is not a security boundary; server must authorize every operation.
- Display node scope, action impact, task state and correlation ID.
- Do not store secret values in localStorage/sessionStorage. Do not log tokens.
- Use API client generated/validated from OpenAPI where practical.
- Include loading, empty, error, unsupported, stale and partial states.
- Destructive operations require explicit confirmation dialog with target details.
- All copy uses locale keys; do not hardcode translated strings inside components.
