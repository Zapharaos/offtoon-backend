# App
APP ?= offtoon-backend
GOOS ?=
TAG ?= v0.0.0
BUILD ?= 0
BUILD_DATE = $(shell date +%FT%T)

# Go variables
GO111MODULE ?= on
GOPROXY ?= "https://proxy.golang.org,direct"
GOSUMDB ?= "sum.golang.org"
CGO_ENABLED ?= 0
GOINSECURE ?=
GONOSUMDB ?=
GO_OPT=GOPROXY=$(GOPROXY) GOINSECURE=$(GOINSECURE) GONOSUMDB=$(GONOSUMDB) GOSUMDB=$(GOSUMDB) GO111MODULE=$(GO111MODULE) CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS)

# Docker
DOCKER_COMPOSE = docker compose
DOCKER_FILE = docker-compose.yml
DOCKER_IMAGE ?= $(APP):$(TAG)

# Swagger
SWAGGER_FILE = docs/swagger.yaml
SWAGGER_UI_PORT = 80

# Build the executable
build:
	$(GO_OPT) go build -a -trimpath -ldflags "-X main.Version=$(TAG)-$(BUILD) -X main.BuildDate=$(BUILD_DATE)" -o bin/$(APP)

run: # Run the executable
	bin/$(APP)

# Build services
docker-build:
ifeq ($(TAG),v0.0.0)
	$(DOCKER_COMPOSE) -f $(DOCKER_FILE) --progress plain build
else
	$(DOCKER_COMPOSE) -f $(DOCKER_FILE) --progress plain build --build-arg TAG=$(TAG)
endif

# Docker commands
docker-up:
	$(DOCKER_COMPOSE) -f $(DOCKER_FILE) up -d

docker-down:
	$(DOCKER_COMPOSE) -f $(DOCKER_FILE) down

docker-logs:
	$(DOCKER_COMPOSE) -f $(DOCKER_FILE) logs -f

docker-restart:
	$(DOCKER_COMPOSE) -f $(DOCKER_FILE) restart

docker-ps:
	$(DOCKER_COMPOSE) -f $(DOCKER_FILE) ps

# Swagger commands
swagger: swagger-init swagger-ui swagger-gen

swagger-init:
	swag init -ot yaml

swagger-ui:
	docker run --rm -d -p $(SWAGGER_UI_PORT):8080 -e SWAGGER_JSON=/tmp/swagger.yaml -v `pwd`/docs:/tmp swaggerapi/swagger-ui
	@echo "Swagger UI is available at http://localhost:$(SWAGGER_UI_PORT)"

swagger-gen:
	docker run --rm -v `pwd`:/local openapitools/openapi-generator-cli:latest generate -i /local/docs/swagger.yaml -g typescript-angular -o /local/docs/angular

# Help command to display usage
help:
	@echo "Usage:"
	@echo "  make build               \- Build Go executable"
	@echo "  make run                 \- Run Go executable"
	@echo ""
	@echo "Docker Commands:"
	@echo "  make docker-build        \- Build Docker services"
	@echo "  make docker-up           \- Start services"
	@echo "  make docker-down         \- Stop services"
	@echo "  make docker-logs         \- View logs"
	@echo "  make docker-restart      \- Restart services"
	@echo "  make docker-ps           \- List services"
	@echo ""
	@echo "Configuration:"
	@echo "  Set COMPOSE_FILE in .env (docker-compose.dev.yml or docker-compose.prod.yml)"
	@echo "  Or override: DOCKER_FILE=docker-compose.prod.yml make docker-up"
	@echo ""
	@echo "Swagger Commands:"
	@echo "  make swagger             \- Generate and serve Swagger documentation"
	@echo "  make swagger-init        \- Initialize Swagger documentation"
	@echo "  make swagger-ui          \- Serve Swagger UI"
	@echo "  make swagger-gen         \- Generate TypeScript Angular client from Swagger"
