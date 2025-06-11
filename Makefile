# Load the .env file
include controller/.env
export $(shell sed 's/=.*//' .env)

# ---------- variables ----------
SERVICES = scrape compare alert
CONTROLLER = controller

# API endpoint configuration
API_HOST ?= localhost:8001
API_BASE_URL = http://$(API_HOST)/api/v0

# Default target
.DEFAULT_GOAL := help


# ---------- helper targets ----------
help:
	@echo "Usage: make <target>"
	@echo "compose-up          – build & start stack"
	@echo "compose-down        – stop stack"
	@echo "compose-restart     – restart all containers"
	@echo "compose-build       – build images only"
	@echo "compose-logs        – follow logs"
	@echo "clean               – down + prune volumes"
	@echo "install-<svc>       – pipenv install --dev in service folder"
	@echo "shell-<svc>         – pipenv shell in service folder"
	@echo "install-all         – run pipenv install --dev for every service"
	@echo "pkg-install-<svc>-<pkg> – install package in specific service"
	@echo "pkg-install-all-<pkg>   – install package in all services"


up:
	docker compose --env-file ./controller/.env up -d --build

down:
	docker compose down

restart:
	docker compose restart

build:
	docker compose --env-file ./controller/.env build

logs:
	docker compose logs -f

clean:
	docker compose down -v --remove-orphans
	docker system prune -f -a

# ---------- per‑service pipenv ----------
install-%:
	@if [ "$*" = "controller" ]; then \
		cd $* && pipenv install --dev; \
	else \
		cd services/$* && pipenv install --dev; \
	fi

shell-%:
	@if [ "$*" = "controller" ]; then \
		cd $* && pipenv shell; \
	else \
		cd services/$* && pipenv shell; \
	fi

# ---------- package installation ----------
pkg-install-%-%:
	@if [ "$(word 1,$(subst -, ,$*))" = "controller" ]; then \
		echo "Installing $(word 2,$(subst -, ,$*)) in controller"; \
		cd controller && pipenv install $(word 2,$(subst -, ,$*)); \
	elif [ -d "services/$(word 1,$(subst -, ,$*))" ]; then \
		echo "Installing $(word 2,$(subst -, ,$*)) in service $(word 1,$(subst -, ,$*))"; \
		cd services/$(word 1,$(subst -, ,$*)) && pipenv install $(word 2,$(subst -, ,$*)); \
	else \
		echo "Service $(word 1,$(subst -, ,$*)) not found"; \
		exit 1; \
	fi

pkg-install-all-%:
	@echo "Installing $* in controller"
	cd controller && pipenv install $*
	@for svc in $(SERVICES); do \
		echo "Installing $* in $$svc"; \
		cd services/$$svc && pipenv install $* && cd ../..; \
	done

# ---------- testing ----------


# ---------- local testing ----------


# ---------- bulk ----------
install-all:
	@echo "Installing controller"
	$(MAKE) install-controller
	@for svc in $(SERVICES); do \
		echo "Installing $$svc"; \
		$(MAKE) install-$$svc; \
	done

# ---------- run ruff for all services locally ----------
ruff:
	@echo "Running ruff for controller"
	ruff format controller
	ruff check controller --fix
	@for svc in $(SERVICES); do \
		echo "Running ruff for $$svc"; \
		ruff format services/$$svc; \
		ruff check services/$$svc --fix; \
	done


connect-db:
	docker compose exec -it postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB)

# Helper function to make curl requests
define curl_request
	@curl -X POST \
		-H "Content-Type: application/json" \
		$(if $(filter-out undefined,$(origin DATA)),-d '$(DATA)',) \
		$(API_BASE_URL)/$(1)
	@echo "\n"
endef

# ---------- database migrations ----------

# ---------- database export helpers ----------