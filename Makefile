.PHONY: build frontend backend test e2e run

build: frontend backend

frontend:
	cd web && pnpm install && pnpm build

backend:
	go build -o viberouter .

test:
	go vet ./... && go test ./...

e2e:
	bash tests/e2e/run_all.sh

run: build
	./viberouter
