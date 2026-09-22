.PHONY: up down build logs ps shell-panel shell-proxy apply clean

# Start all services
up:
	docker compose up -d --build

# Stop all services
down:
	docker compose down

# Build images without starting
build:
	docker compose build

# View logs
logs:
	docker compose logs -f

# View logs for specific service
logs-%:
	docker compose logs -f $*

# Show status
ps:
	docker compose ps

# Shell into panel
shell-panel:
	docker compose exec panel sh

# Shell into proxy
shell-proxy:
	docker compose exec proxy sh

# Apply proxy config (useful for debugging)
apply:
	curl -s -X POST http://localhost:8080/proxy/apply -b "panel_session=$(shell cat .session_cookie 2>/dev/null)" | cat

# Clean volumes (WARNING: deletes DB data)
clean:
	docker compose down -v

# Initialize env file
init:
	@if [ ! -f .env ]; then cp .env.example .env && echo "Created .env from .env.example. Edit it before running."; else echo ".env already exists"; fi

# View proxy config
config:
	docker compose exec proxy cat /etc/3proxy/3proxy.cfg

# View proxy log
proxy-log:
	docker compose exec proxy tail -100 /var/log/3proxy/3proxy.log
