#!/bin/bash

set -e
set -o pipefail
set -u

# Default to development if APP_ENV is not set
APP_ENV=${APP_ENV:-development}

echo "Running in $APP_ENV environment"

# Add the app directory and root directory to PYTHONPATH
export PYTHONPATH=/app:/app/core:/app/utils:$PYTHONPATH

# Wait for database to be ready
echo "Waiting for database to be ready..."
python -m app.utils.wait_for_db

# migrate database
echo "Migrate database as required..."
alembic upgrade head

# Start the FastAPI server in the background
echo "Starting the FastAPI application..."
uvicorn app.main:app --host 0.0.0.0 --port 8000 --reload &
FASTAPI_PID=$!

# Start the Temporal worker
echo "Starting the Temporal worker..."
python -m app.worker

# If the worker exits, kill the FastAPI server
kill $FASTAPI_PID