# Build stage
FROM python:3.12-slim-bookworm AS builder

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1 \
    PYTHONPATH=/app

WORKDIR /app

# Install build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

# Install pipenv
RUN pip install --no-cache-dir pipenv

# Copy only the necessary shared package files
# First copy setup.py and __init__.py to create the package structure
COPY shared/setup.py shared/__init__.py /app/shared/
# Then copy only the Python modules that are actually used
COPY shared/db /app/shared/db/

# Install shared package in editable mode for development
RUN pip install -e /app/shared

# Copy service dependencies
COPY controller/Pipfile controller/Pipfile.lock ./
COPY controller/setup.py ./

# Install dependencies
RUN pipenv install --system --deploy --ignore-pipfile

# Copy application code, migrations, workflows, and setup script
COPY controller/app ./app
COPY controller/workflows ./workflows
COPY controller/setup.sh ./
COPY controller/.env ./

# Make setup script executable
RUN chmod +x setup.sh

# Final stage
FROM python:3.12-slim-bookworm

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONPATH=/app

WORKDIR /app

# Copy Python packages from builder
COPY --from=builder /usr/local/lib/python3.12/site-packages/ /usr/local/lib/python3.12/site-packages/
COPY --from=builder /usr/local/bin/ /usr/local/bin/

# Copy application code, migrations, workflows, and setup script
COPY --from=builder /app/app ./app
COPY --from=builder /app/workflows ./workflows
COPY --from=builder /app/setup.sh ./
COPY --from=builder /app/.env ./

# Make setup script executable
RUN chmod +x setup.sh

# Run the setup script
CMD ["./setup.sh"]
