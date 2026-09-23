.PHONY: build frontend backend test run

build: frontend backend

frontend:
	cd web && pnpm install && pnpm build

backend:
	go build -o viberouter .

test:
	go vet ./... && go test ./...

run: build
	./viberouter
