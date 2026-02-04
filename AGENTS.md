# Repository Guidelines

## Project Structure & Module Organization
- Go backend lives at the repo root with feature folders like `api/`, `auth/`, `decision/`, `manager/`, `market/`, `trader/`, `pool/`, `migrations/`, and `logger/`.
- Frontend lives in `web/` (React + TypeScript + Vite). Source is in `web/src/`, assets in `web/public/`.
- Configuration examples are in `config.json.example` and `config/`. Secrets and keys are kept out of VCS under `secrets/`.
- Docs and operational notes live in `docs/`, `docker/`, and `nginx/`.

## Build, Test, and Development Commands
- `make test`: run all tests (Go + frontend).
- `make test-backend`: Go tests only (`go test -v ./...`).
- `make test-frontend`: frontend tests only (`cd web && npm run test`).
- `make build`: build the Go backend.
- `make build-frontend`: build the web UI.
- `make fmt`: format Go code (`go fmt ./...`).
- `make lint`: run `golangci-lint` (install required).
- `go build -o nofx`: local backend binary.
- `cd web && npm run dev`: run the frontend dev server.

## Coding Style & Naming Conventions
- Go: follow standard Go idioms; keep names meaningful; handle errors explicitly. Run `go fmt` and `go vet` before committing.
- TypeScript/React: use strict typing, define interfaces, avoid `any`, prefer functional components with hooks.
- Frontend formatting and linting: `cd web && npm run lint`, `npm run format`.

## Testing Guidelines
- Backend tests: `go test ./...` (uses Go’s testing framework and `testify`).
- Frontend tests: `cd web && npm run test` (Vitest + Testing Library).
- Name tests to match the target package/component; keep new features covered.

## Commit & Pull Request Guidelines
- Commits follow Conventional Commits: `feat(exchange): add OKX integration`.
- Keep the subject line imperative and ≤ 72 chars. Use scopes when useful.
- PRs should include a clear description, linked issue (if any), and notes on manual testing.
- Pre‑PR checklist: `go build`, `npm run build`, `go test ./...`, `go fmt`, `go vet`, `npm run lint`.

## Configuration & Security Notes
- Do not commit API keys. Use `config.json` from `config.json.example` and store secrets in `secrets/`.
- TA‑Lib is required for backend indicators (macOS: `brew install ta-lib`).
