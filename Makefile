.PHONY: build test test-integration vet fmt-check generate frontend-install frontend-check frontend-test frontend-build compose-config

build:
	docker build --target binary -f Dockerfile.backend -t varyaone-backend-build .

test:
	docker run --rm -v "$(CURDIR):/src" -w /src golang:1.26.6-alpine go test ./...

test-integration:
	docker compose --profile test run --rm backend-tests

vet:
	docker run --rm -v "$(CURDIR):/src" -w /src golang:1.26.6-alpine go vet ./...

fmt-check:
	docker run --rm -v "$(CURDIR):/src" -w /src golang:1.26.6-alpine sh -c 'files=$$(gofmt -l cmd internal); test -z "$$files" || { echo "$$files"; exit 1; }'

generate:
	docker run --rm -v "$(CURDIR):/src" -w /src golang:1.26.6-alpine go generate ./...

frontend-install:
	cd web && npm ci

frontend-check:
	cd web && npm run check && npm run lint

frontend-test:
	cd web && npm run test

frontend-build:
	cd web && npm run build

compose-config:
	docker compose config --quiet
