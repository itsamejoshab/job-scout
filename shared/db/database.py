from __future__ import annotations
import contextlib
import logging
import os
from typing import AsyncIterator
import backoff
import json
from datetime import datetime
from sqlalchemy.exc import DBAPIError, SQLAlchemyError
from sqlalchemy.ext.asyncio import (AsyncSession, async_sessionmaker,
                                    create_async_engine)
from sqlalchemy.orm import declarative_base, MappedAsDataclass
from sqlalchemy.sql import text


logger = logging.getLogger(__name__)

# Asynchronous Database URL
POSTGRES_USER='default_user'
POSTGRES_PASSWORD='default_pass'
POSTGRES_HOST='postgres'
POSTGRES_PORT=5432
POSTGRES_DB='jobdb'

SQLALCHEMY_DATABASE_URL =f"postgresql+asyncpg://{POSTGRES_USER}:{POSTGRES_PASSWORD}@{POSTGRES_HOST}:{POSTGRES_PORT}/{POSTGRES_DB}"

# Create engine with optimized configurations
engine = create_async_engine(
    SQLALCHEMY_DATABASE_URL,
    echo=False,  # Set to False in production for better performance
    pool_pre_ping=True,  # Enable connection health checks
    pool_size=20,  # Number of connections to maintain
    max_overflow=20,  # Maximum number of connections to create above pool_size
    pool_timeout=30,  # Seconds to wait before giving up on getting a connection
    pool_recycle=1800,  # Recycle connections after 30 minutes
    connect_args={
        "command_timeout": 120,  # Command timeout in seconds
        "timeout": 60,  # Connection timeout in seconds
        "server_settings": {
            "statement_timeout": "120000",  # 120 seconds in milliseconds
            "idle_in_transaction_session_timeout": "120000",
            "application_name": "datadive_app",  # For better monitoring
        },
    },
)

# Base class for declarative class definitions
Base = declarative_base()

# Create a sessionmaker with optimized settings
async_session_maker = async_sessionmaker(
    engine, expire_on_commit=False, autocommit=False, autoflush=False
)

def handle_db_error(e: Exception):
    """Handle database errors with appropriate logging"""
    logger.error(f"Database error occurred: {str(e)}", exc_info=True)
    if isinstance(e, DBAPIError):
        logger.error(
            f"Database API Error - Connection details: {e.connection_invalidated}"
        )
    raise

@backoff.on_exception(
    backoff.expo, (SQLAlchemyError, ConnectionError), max_tries=3, max_time=30
)
@contextlib.asynccontextmanager
async def get_db() -> AsyncIterator[AsyncSession]:
    """
    Async context manager for database sessions with retry logic.
    Creates a new session for each context with proper error handling and connection management.
    """
    session = async_session_maker()
    try:
        logger.debug("Creating new database session")
        yield session
        await session.commit()  # Automatic commit if no exceptions
        logger.debug("Database session committed successfully")
    except SQLAlchemyError as e:
        await session.rollback()
        logger.error("Rolling back database session due to error")
        handle_db_error(e)
    except Exception as e:
        await session.rollback()
        logger.error(f"Unexpected error in database session: {str(e)}")
        raise
    finally:
        logger.debug("Cleaning up database session")
        await session.close()

def _log_db_err(e: Exception) -> None:
    logger.error("Database error: %s", e, exc_info=True)
    if isinstance(e, DBAPIError):
        logger.error("Connection invalidated: %s", e.connection_invalidated)
    raise

# ---------------------------------------------------------------------
# Optional self-test – run once at startup
# ---------------------------------------------------------------------
async def _json_roundtrip() -> None:
    async with engine.begin() as conn:
        # Convert UTC datetime to naive for storage
        val = {"now": datetime.now().isoformat(), "ok": True}
        json_val = json.dumps(val)  # Convert dict to JSON string
        result = await conn.execute(
            text("SELECT cast(:value AS jsonb) as result"), {"value": json_val}
        )
        out = result.scalar_one()
        assert out == val, f"JSON round-trip failed → {out}"
        logger.info("JSONB round-trip OK")


async def init_db():
    async with engine.begin() as conn:
        await conn.run_sync(Base.metadata.create_all)
    await _json_roundtrip()


async def close_db():
    await engine.dispose()
