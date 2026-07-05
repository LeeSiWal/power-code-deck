.PHONY: dev dev-server dev-client build build-client build-server build-windows run clean setup

# Development - run Go server and Vite dev server together
dev:
	@echo "Starting Go server..."
	cd server && go run . &
	@echo "Starting Vite dev server..."
	cd client && pnpm dev

dev-server:
	cd server && go run .

dev-client:
	cd client && pnpm dev

# Production build
build: build-client build-server

build-client:
	cd client && pnpm install && pnpm build

build-server: build-client
	@echo "Copying client dist to server/static..."
	rm -rf server/static
	cp -r client/dist server/static
	cd server && CGO_ENABLED=0 go build -o ../pcd .

# Native Windows binary (no WSL, no cgo) — pure-Go SQLite + go-pty/ConPTY.
build-windows: build-client
	@echo "Building native Windows binary (pcd.exe)..."
	rm -rf server/static
	cp -r client/dist server/static
	cd server && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o ../pcd.exe .

# Run production binary
run:
	./pcd

# Clean
clean:
	rm -f pcd pcd.exe agentdeck
	rm -rf client/dist
	rm -rf server/static

# Install dependencies
setup:
	cd client && pnpm install
	cd server && go mod tidy
