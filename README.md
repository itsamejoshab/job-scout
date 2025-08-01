# job-scout

Job Scout is yet another homegrown application designed to monitor job sites for job postings, filter, and send alerts. What makes this one different from others is that it is built on distributed microservices. Why? Because I already have a python script to monitor for job changes, and I wanted to experiment and learn with Temporal orchestration.

This may seem like overkill, but my hope is that with this type of architecture, it might be easier for more contributors to add features in the future.

**Future Ideas**
- A UI
- Custom LLM resume, customized for each job
- LLM filtering step (not everything can be simple rules, though simple rules are _fast_)

Inspiration: 
- [JobScout by Krishna](https://github.com/krishnavalliappan/JobScout)
- [JobScout.ai by abhinav-m22 ](https://github.com/abhinav-m22/JobScout.ai) | [App](https://jobscout-ai.vercel.app/)

## Architecture Overview

The system consists of several key components:

### Core Services

- **Job-Scout**: The main API and workflow orchestrator is in `job-scout/app`
- **Temporal**: Workflow orchestration and state management
- **PostgreSQL**: Primary data store

### Processing Services

1. **Scraper**: Scrapes sites for job postings, mainly high level information: e.g. Company, Title, Location, URL
2. **Duplicate Remover**: Dedupes jobs that may be cross posted or repeated results
3. **Basic Filter**: Eliminates jobs based on obvious filters such as keyword matching in Title or Company
4. **Job Detailer**: Scrapes more detail about each job, gets the entire description
5. **Advanced Filter**: Further eliminates jobs based on keyword matching in the desription itself.
6. **Smart Filter**: Uses AI to further eliminate jobs which don't match what the user wants
7. **Notifier**: Sends notifications to a webhook. 

## Project Structure

```bash
.
├── data/                            # data folder containing the local database (Postgres)
├── job-scout/                       # Main API and workflow orchestrator
│   ├── app/                         # Main Job-Scout App
|   │   ├── services/                # Modular services that run via workflow/activities with Temporal
|   |   │   ├── scrapers/            # Custom scrapers for each job source (LINKEDIN, INDEED, etc)
|   |   │   ├── duplicate_remover/   # Custom scrapers for each job source (LINKEDIN, INDEED, etc)
|   |   │   ├── duplicate_remover/   # Dedupes jobs (Duplicate Remover service)
|   |   │   ├── basic_filter/        # Filters jobs based on basic criteria (Basic Filter service)
|   |   │   ├── job_detailer/        # Scrapes detailed job descriptions (Job Detailer service)
|   |   │   ├── advanced_filter/     # Filters jobs based on description (Advanced Filter service)
|   |   │   ├── smart_filter/        # AI-powered filtering (Smart Filter service)
|   |   │   ├── notifier/            # Sends notifications to webhooks (Notifier service)
|   │   ├── db/                      # Database layer - schemas, models, etc
|   │   ├── workflow.py              # Main workflow logic (Temporal)
|   │   ├── activities.py            # Main activities logic (Temporal)
|   │   └── api.py                   # Fast-API server for routing requests
├── config/                          # Configuration files
└── docker-compose.yml               # Service orchestration
```

## Prerequisites

- Docker and Docker Compose
- Python 3.12+
- Make (for using Makefile commands)

## Quick Start (User)

1. Clone the repository

2. Configure the search settings manually (would love to develop a UI to manage the database)
   a. Universal Search Settings: `app/db/universal_settings.json`
   b. Scraper specific search settings: `app/db/scraper_settings.json`

3. Configure `.env` file with personal settings, like notifier webhook, etc.

4. Run the app with `make up` 

5. Externally hit `localhost:8001/api/v0/run` whenever you want the search to happen (i.e. every 5 mins, etc)

## Quick Start (Developer)

1. Clone the repository

2. Set up environment variables:
```bash
   cp controller/.env.example controller/.env
   # Edit .env with your configuration
```

3. Start the services:
```bash
   make up
 ```

4. Run database migrations:
```bash
   make migrate
```

## Make Commands

The project includes several useful make commands for development and operations:

### Service Management
```bash
make up              # Build and start all services
make down            # Stop all services
make restart         # Restart all services
make build           # Build service images
make logs            # Follow service logs
make clean           # Stop services and clean up volumes/images
```

### Development Environment
```bash
make install-<svc>           # Install dependencies for a specific service
make shell-<svc>             # Open a shell for a specific service
make install-all             # Install dependencies for all services
make pkg-install-<svc>-<pkg> # Install a package in a specific service
make pkg-install-all-<pkg>   # Install a package in all services
```

### Code Quality
```bash
make ruff                   # Run ruff formatter and linter for all services
```

### Database
```bash
make connect-db             # Connect to PostgreSQL database
```

### Database Migrations

The project uses Alembic for database migrations. Here are the available migration commands:

#### Basic Migration Commands
```bash
# Generate a new migration
make migrate-new
# You'll be prompted to enter a migration message
# Example: "add clip metadata table"

# Apply all pending migrations
make migrate-up

# Rollback the last migration
make migrate-down

# Show current migration status and history
make migrate-status
```

#### Advanced Migration Commands
```bash
# Reset all migrations (WARNING: This will delete all data!)
make migrate-reset
# You'll be prompted to confirm the action

# Stamp the database with a specific revision
make migrate-stamp
# You'll be prompted to enter the revision ID
```

#### Migration Workflow Examples

1. Creating a new migration:
```bash
# 1. Make your model changes in the code
# 2. Generate a new migration
make migrate-new
# Enter message: "add user preferences table"

# 3. Review the generated migration file in controller/alembic/versions/
# 4. Apply the migration
make migrate-up
```

2. Rolling back changes:
```bash
# If you need to undo the last migration
make migrate-down

# To check the current state
make migrate-status
```

3. Development workflow:
```bash
# 1. Start fresh (WARNING: deletes all data)
make migrate-reset

# 2. Create new migration
make migrate-new
# Enter message: "add clip processing status"

# 3. Apply migration
make migrate-up

# 4. Verify status
make migrate-status
```

4. Stamping a specific version:
```bash
# Useful when setting up a new environment
# or syncing with a specific database state
make migrate-stamp
# Enter revision: "a1b2c3d4e5f6"
```

> **Note**: Always backup your database before running migration commands, especially `migrate-reset` which will delete all data.

### Workflow Triggers

tbd

### API Configuration
You can customize the API endpoint by setting the API_HOST variable:
```bash
make API_HOST=other-host:8001 <workflow>
```
### Adding a New Service

1. Create a new service directory in `services/`
2. Copy the service template structure
3. Add the service to `docker-compose.yml`
4. Update the controller workflow and activities as needed

## API Documentation

Once the services are running, access the API documentation at:
- Swagger UI: http://localhost:8001/controller/docs
- ReDoc: http://localhost:8001/controller/redoc

## Monitoring

- Temporal UI: http://localhost:8082

## Contributing

1. Fork the repository
2. Create a feature branch
3. Commit your changes
4. Push to the branch
5. Create a Pull Request


---

## 🚀 Quick Start (local, CPU) using curl

tbd