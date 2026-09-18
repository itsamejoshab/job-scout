# Load env (for connect-db / curl helpers)
-include job-scout/.env
export

# API endpoint configuration
API_HOST ?= localhost:8001
API_BASE_URL = http://$(API_HOST)/api/v0

GO_DIR = job-scout

.DEFAULT_GOAL := help

help:
	@echo "Usage: make <target>"
	@echo "up            - build & start the whole stack (postgres, temporal, api, worker)"
	@echo "down          - stop the stack"
	@echo "restart       - restart all containers"
	@echo "build         - build images only"
	@echo "logs          - follow logs"
	@echo "clean         - down + prune volumes/images"
	@echo "go-build      - compile the Go binary locally"
	@echo "go-test       - run Go tests"
	@echo "fmt           - gofmt the module"
	@echo "vet           - go vet the module"
	@echo "connect-db    - psql into the postgres container"
	@echo "run           - POST /api/v0/run"
	@echo "notify        - POST /api/v0/notify"

# ---------- docker stack ----------
up:
	docker compose --env-file ./job-scout/.env up -d --build

down:
	docker compose down

restart:
	docker compose restart

build:
	docker compose --env-file ./job-scout/.env build

logs:
	docker compose logs -f

clean:
	docker compose down -v --remove-orphans
	rm -rf ./data/postgres
	docker system prune -f -a

# ---------- Go (local) ----------
go-build:
	cd $(GO_DIR) && go build ./...

go-test:
	cd $(GO_DIR) && go test ./...

fmt:
	cd $(GO_DIR) && gofmt -w .

vet:
	cd $(GO_DIR) && go vet ./...

# ---------- database ----------
connect-db:
	docker compose exec -it postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB)

# ---------- workflow trigger ----------
run:
	@curl -s -X POST "$(API_BASE_URL)/run?job_source=$(or $(SOURCE),LINKEDIN)"
	@echo ""

notify:
	@curl -s -X POST "$(API_BASE_URL)/notify"
	@echo ""
