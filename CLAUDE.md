# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a modern ticketing system built with Go (Gin) backend and React (TypeScript) frontend. The system supports multi-role permission control and real-time collaboration.

## Architecture

- **Backend**: Go 1.21+ with Gin framework, PostgreSQL database, Redis cache, JWT + OTP authentication
- **Frontend**: React 18+ with TypeScript, Vite build tool, shadcn/ui + TailwindCSS, TanStack Query for state management
- **Database**: PostgreSQL 15+ with GORM for ORM, Redis 7+ for caching
- **Deployment**: Docker & Docker Compose for containerization

## Development Commands

### Quick Start
```bash
# Start full development environment with Docker
make dev
# or
docker-compose up -d
```

### Backend (server/)
```bash
# Install dependencies
cd server && go mod tidy

# Run server in development mode
cd server && make run
# or with auto-migration enabled
cd server && make run-migrate

# Run database migrations
cd server && make migrate-all

# Run tests
cd server && make test
# or
cd server && go test ./...

# Build server
cd server && make build

# Format and lint
cd server && make fmt && make vet
```

### Frontend (web/)
```bash
# Install dependencies
cd web && npm install

# Start development server
cd web && npm run dev

# Build for production
cd web && npm run build

# Run linting
cd web && npm run lint
```

### Database Migration Commands
```bash
# Complete migration (tables + indexes + seed data)
cd server && make migrate-all

# Individual migration steps
cd server && make migrate-tables    # Tables only
cd server && make migrate-indexes   # Indexes only  
cd server && make migrate-seed      # Seed data only
```

## Code Structure

### Backend Structure (`server/`)
- `cmd/migrate/` - Database migration utilities
- `internal/auth/` - Authentication and authorization (JWT, OTP, email)
- `internal/config/` - Configuration management
- `internal/database/` - Database connection and migration
- `internal/handlers/` - HTTP request handlers
- `internal/middleware/` - Gin middlewares (CORS, JWT, rate limiting, security)
- `internal/models/` - GORM database models (User, Ticket, Category, etc.)
- `internal/services/` - Business logic services
- `main.go` - Application entry point

### Frontend Structure (`web/`)
- `src/features/` - Feature-based organization (auth, tickets, admin)
- `src/components/` - Reusable UI components with shadcn/ui
- `src/hooks/` - Custom React hooks
- `src/lib/` - Utility libraries (API client, retry logic)
- `src/types/` - TypeScript type definitions

### Key Patterns
- **Feature-based organization**: Both frontend and backend organize code by business features
- **Layer separation**: Models, services, handlers are separated in backend
- **Component composition**: Frontend uses shadcn/ui components with proper TypeScript typing
- **API integration**: TanStack Query for server state management
- **Authentication**: JWT with OTP verification, role-based access control

## Environment Setup

The project uses Docker Compose for development. Key services:
- PostgreSQL (port 5432)
- Redis (port 6379) 
- Backend API (port 8080)
- Frontend dev server (port 3000)

Configuration files:
- `server/.env` - Backend environment variables
- `docker-compose.yml` - Development environment setup

## Testing

Run all tests:
```bash
make test
```

Individual test suites:
```bash
cd server && go test ./...  # Backend tests
cd web && npm test          # Frontend tests (if configured)
```

## Key Features

- Multi-role authentication (Admin/Agent/User)
- Ticket lifecycle management with status transitions
- Real-time commenting system
- Email configuration management
- RESTful API design with proper error handling
- Responsive modern UI with dark/light theme support