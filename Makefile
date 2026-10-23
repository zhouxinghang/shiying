.PHONY: build frontend run dev test clean

build: frontend
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/shiying ./cmd/shiying
	cp LICENSE NOTICE bin/

frontend:
	cd ui && npm ci --no-audit --no-fund && npm run build

run: build
	./bin/shiying

dev:
	go run ./cmd/shiying

test:
	go test -race ./...
	cd ui && npm run typecheck && npm test

clean:
	rm -rf bin internal/web/static/assets internal/web/static/index.html
