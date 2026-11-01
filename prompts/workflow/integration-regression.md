# Cross-phase Integration Regression Prompt

Run a regression review of all completed phases against their contracts. Check OpenAPI and Protobuf compatibility, DB migrations, Agent/server version negotiation, RBAC, audit trails, module capability gating, UI unsupported/error states, security boundaries and upgrade/rollback. Run unit, integration, contract, E2E and security-negative tests available. Identify cross-module coupling and duplicated behavior. Do not introduce new features. Report PASS/FAIL/NOT RUN/BLOCKED with evidence and remediation order. No production host changes.
