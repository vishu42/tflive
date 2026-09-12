# tflive
#
# Run `make` or `make help` for the target list.

SPEC := docs/openapi.yaml

# Points at the Compose Postgres from docker-compose.yaml. Override to run the
# integration tests somewhere else:
#
#   make differential-test TEST_DSN=postgres://…
TEST_DSN ?= postgres://tflive:tflive@localhost:55432/tflive_test?sslmode=disable

# Redocly is pinned per target, deliberately.
#
# `preview-docs` was removed in Redocly CLI v2, and v2 has no live-reload
# preview for a plain description file -- `openapi preview` expects a Redocly
# project. So the preview stays on v1 until there is a v2 equivalent, while
# linting tracks v2, which is the version the spec is written against.
REDOCLY_PREVIEW := @redocly/cli@1
REDOCLY_LINT    := @redocly/cli@2

.DEFAULT_GOAL := help

.PHONY: help docs-preview docs-lint differential-test

help: ## List the available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

docs-preview: ## Serve the API reference (docs/openapi.yaml) with live reload
	npx $(REDOCLY_PREVIEW) preview-docs $(SPEC)

docs-lint: ## Validate docs/openapi.yaml against the OpenAPI spec
	npx $(REDOCLY_LINT) lint $(SPEC)

# internal/authorization/write.go is a transcription of OpenFGA's own write path
# with its BeginTx/Commit removed, so a tuple write can join our transaction.
# A transcription diverges silently: an upstream migration that adds a column
# keeps compiling here and starts writing rows that are subtly wrong.
#
# differential_test.go is the only thing that catches that, and it needs a real
# database -- without one it skips, and a skipped guard is no guard.
#
# Run it after every `go get github.com/openfga/openfga@...`.
differential-test: ## Run the authorization tests that need a real Postgres
	tflive_POSTGRES_TEST_DSN=$(TEST_DSN) go test ./internal/authorization/ -count=1 -v
