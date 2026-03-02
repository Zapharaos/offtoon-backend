![GitHub Release](https://img.shields.io/github/v/release/Zapharaos/offtoon-backend) ![GitHub Actions Workflow Status](https://img.shields.io/github/actions/workflow/status/Zapharaos/offtoon-backend/golang.yml) ![GitHub License](https://img.shields.io/github/license/Zapharaos/offtoon-backend) [![Go Report Card](https://goreportcard.com/badge/github.com/Zapharaos/offtoon-backend)](https://goreportcard.com/report/github.com/Zapharaos/offtoon-backend)

# Offtoon Backend

**Offtoon Backend** is a high-performance RESTful API service that powers the Offtoon application. Built with Go, it provides search, fetching, and downloading capabilities for webtoons and manga, integrating multiple external sources such as AsuraComic and NatoManga. It supports real-time progress streaming over WebSockets and exports chapters in PDF, CBZ, or raw image formats.

## 🎯 What is Offtoon Backend?

The backend service provides:
- **Search** for webtoons and manga across multiple sources
- **Fetch** full toon details including chapter lists
- **Download** selected chapters with real-time WebSocket progress updates
- **Export** chapters as PDF, CBZ, or raw image archives
- **Adaptive Rate Limiting** - Smart throttling to respect external source limits
- **Concurrent Processing** - Worker-pool-based chapter and image downloading
- **WebSocket Runtime** - Live progress packets during long-running downloads

## Sources Supported

- **AsuraComic** - A popular source for manhwa and manga

## 🛠️ Technologies

Built with modern Go technologies and best practices:

- **Go 1.26** - High-performance compiled language
- **Chi** - Lightweight HTTP router
- **Viper** - Configuration management
- **Zap** - Structured, high-performance logging
- **Gorilla WebSocket** - Real-time bidirectional communication
- **gopdf** - PDF generation for chapter exports
- **Swagger/OpenAPI** - API documentation and client generation
- **Docker** - Containerized deployment

### Key Features

- **Adaptive Rate Limiting** - Smart throttling based on source response times
- **Concurrent Processing** - Efficient goroutine-based image and chapter downloading
- **WebSocket Progress Streaming** - Real-time download progress via WebSocket runtime
- **Multi-format Export** - Chapter export as PDF, CBZ, or raw images in a ZIP archive
- **Multi-source Support** - Pluggable API client registry (currently: Asura, Nato)
- **Swagger Documentation** - Auto-generated API documentation

## 🚀 Getting Started

### Prerequisites

- **Go**: 1.26 or higher
- **Docker**: 20.x or higher (for containerized deployment)
- **Docker Compose**: 2.x or higher
- **Make**: For build automation (optional but recommended)

### Installation

1. Clone the repository:
```bash
git clone https://github.com/Zapharaos/offtoon-backend.git
cd offtoon-backend
```

2. Install Go dependencies:
```bash
go mod download
```

3. Install build tools:

```bash
# Swagger CLI for API documentation
go install github.com/swaggo/swag/cmd/swag@latest
```

4. Configure environment:
```bash
# Copy example environment file
cp .env.example .env

# Edit .env with your configuration
# Set COMPOSE_FILE, APP_ENV, and BACKEND_PORT
```

## 💻 Development

### Production (with Docker)

This project uses Docker. Get started [here](https://www.docker.com/get-started).

#### Build

To build the project:
```bash
make docker-build
```

#### Start

To start the whole project:
```bash
make docker-up
# or directly:
docker compose -f docker-compose.dev.yml up
# add -d to run in detached mode
# add --build to rebuild the images
```

**Configuration:** Set your environment in `.env` file (see `.env.example`).

---

### Development (without Docker)

#### IDE - GoLand (recommended)

We recommend using GoLand for debugging. See [Run/debug configuration](https://www.jetbrains.com/help/go/run-debug-configuration.html).

- Create a new `Go build` configuration
- Set `Package path` to `github.com/Zapharaos/offtoon-backend`
- Enable `Run after build`
- Set `Working directory` to the root of the project `offtoon-backend`
- Add `Environment variables` to override any config variables you need
  - For production mode: `APP_ENV=prod`
  - Any other overrides: `OFFTOON_*`

#### Command line (not recommended)

You can run with the following command:
```bash
go run .
```

---

### Manual Build

```bash
# Build executable
make build

# Run in development (default)
./bin/offtoon-backend

# Run in production
APP_ENV=prod ./bin/offtoon-backend
```

---

### Configuration

**Configuration Files:**
- **`config/config.yaml`** - Default configuration (development)
- **`config/config.prod.yaml`** - Production overrides (loaded when `APP_ENV=prod`)

**Docker Compose Files:**
- **`docker-compose.dev.yml`** - Development setup
- **`docker-compose.prod.yml`** - Production setup with VPS network

**Environment Variables (in .env):**
- `COMPOSE_FILE` - Which docker-compose file to use (`docker-compose.dev.yml` or `docker-compose.prod.yml`)
- `APP_ENV` - Set to `prod` for production, leave empty for development
- `BACKEND_PORT` - Backend port (default: 3000)

**Port Reference:**

| Environment | COMPOSE_FILE | APP_ENV | BACKEND_PORT |
|-------------|--------------|---------|--------------|
| **Development** | `docker-compose.dev.yml` | _(empty)_ | 3000 |
| **Production** | `docker-compose.prod.yml` | `prod` | 3000 |

---

### Swagger Generation

Generate the swagger file (reused by frontend):

```bash
make swagger
```

## 🔧 Configuration Files

| File | Purpose |
|------|---------|
| `config/config.yaml` | Base configuration (development defaults) |
| `config/config.prod.yaml` | Production-specific overrides |
| `docker-compose.dev.yml` | Development Docker setup |
| `docker-compose.prod.yml` | Production Docker setup with VPS network |
| `Dockerfile` | Container image definition |
| `Makefile` | Build automation scripts |
| `go.mod` | Go module dependencies |

## 📜 Scripts

```bash
make build           # Build Go executable
make run             # Run compiled executable
make docker-build    # Build Docker services
make docker-up       # Start services
make docker-down     # Stop services
make docker-logs     # View logs
make docker-restart  # Restart services
make docker-ps       # List services
make swagger         # Generate and serve Swagger documentation
make swagger-init    # Initialize Swagger YAML
make swagger-ui      # Serve Swagger UI
make swagger-gen     # Generate TypeScript Angular client
make help            # Show all available commands
```

## 🤝 Contributing

Contributions are welcome! Please follow these guidelines:

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

### Code Style

- Follow Go best practices and [Effective Go](https://go.dev/doc/effective_go)
- Use `gofmt` to format code
- Run `go vet` to check for common issues
- Write unit tests for new features
- Ensure all tests pass before submitting PR (`go test ./...`)
- Update Swagger documentation for API changes

### Testing

Run tests with:
```bash
go test ./...
```

Run tests with coverage:
```bash
go test -cover ./...
```

## 📄 License

This project is licensed under the **MIT License** - see the [LICENSE](LICENSE) file for details.

Copyright (c) 2026 Matthieu FREITAG

## 📚 Additional Resources

- [Go Documentation](https://go.dev/doc/)
- [Chi Router](https://github.com/go-chi/chi)
- [Viper Configuration](https://github.com/spf13/viper)
- [Zap Logging](https://github.com/uber-go/zap)
- [Gorilla WebSocket](https://github.com/gorilla/websocket)
- [Swagger/OpenAPI](https://swagger.io/docs/)
