.PHONY: dev dev-server dev-client build build-client build-server run clean

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
	cd server && CGO_ENABLED=1 go build -o ../pcd .

# Run production binary
run:
	./pcd

# Clean
clean:
	rm -f pcd agentdeck
	rm -rf client/dist
	rm -rf server/static

# Install dependencies
setup:
	cd client && pnpm install
	cd server && go mod tidy
