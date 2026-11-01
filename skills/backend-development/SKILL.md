# Skill: Go Backend Development

Before coding, inspect the related architecture, API contract and ADRs. Implement a modular Go service using thin HTTP handlers, application services, explicit repositories and typed domain errors. Prefer standard library; use chi, pgx and slog only where needed. Add migrations, unit tests, integration tests, OpenAPI updates, structured redaction and audit coverage. Use context deadlines and graceful shutdown. Run `gofmt`, `go test ./...`, `go vet ./...` and `go test -race ./...` when supported. Never run arbitrary host commands from request data.
