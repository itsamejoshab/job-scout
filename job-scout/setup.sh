#!/bin/bash

# Function to get current timestamp in desired format
timestamp() {
    echo "$(date '+%Y-%m-%d %H:%M:%S,%3N')"
}

set -e
set -o pipefail
set -u

# Default to development if APP_ENV is not set
APP_ENV=${APP_ENV:-development}

echo "$(timestamp) - setup.sh - ECHO => Running in $APP_ENV environment"

# Add the app directory and root directory to PYTHONPATH
export PYTHONPATH=/app/app:/app/app/db:$PYTHONPATH

# Wait for database to be ready
echo "$(timestamp) - setup.sh - ECHO ==> Waiting for database to be ready..."
python -m app.utils.wait_for_db

# migrate database
echo "$(timestamp) - setup.sh - ECHO ===> Migrate database as required..."
alembic upgrade head

# Loading database
echo "$(timestamp) - setup.sh - ECHO ======> Loading initial database..."
python -m app.db.load

# Start the FastAPI server in the background
echo "$(timestamp) - setup.sh - ECHO =======> Starting the FastAPI application..."
uvicorn app.main:app --host 0.0.0.0 --port 8000 --reload &
FASTAPI_PID=$!

# Start the Temporal worker
echo "$(timestamp) - setup.sh - ECHO ========> Starting the Temporal worker..."
python -m app.worker

# If the worker exits, kill the FastAPI server
echo "$(timestamp) - setup.sh - ECHO ! ಠ_ಠ => worker.py exited, so we kill the fastAPI server"
kill $FASTAPI_PID