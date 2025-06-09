# shared/db/session.py  –  sane JSONB handling
# -------------------------------------------------------------

from __future__ import annotations
import contextlib
import logging
import os
import backoff
import json
from datetime import datetime
from typing import AsyncIterator

from sqlalchemy.exc import DBAPIError, SQLAlchemyError
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine
from sqlalchemy.orm import DeclarativeBase, MappedAsDataclass
from sqlalchemy.sql import text

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------
# Environment
# ---------------------------------------------------------------------
POSTGRES_USER = os.getenv("POSTGRES_USER", "default_user")
POSTGRES_PASSWORD = os.getenv("POSTGRES_PASSWORD", "default_pass")
POSTGRES_DB = os.getenv("POSTGRES_DB", "jobsearch")
POSTGRES_HOST = os.getenv("POSTGRES_HOST", "postgres")
POSTGRES_PORT = os.getenv("POSTGRES_PORT", "5432")

DATABASE_URL = (
    f"postgresql+asyncpg://{POSTGRES_USER}:{POSTGRES_PASSWORD}"
    f"@{POSTGRES_HOST}:{POSTGRES_PORT}/{POSTGRES_DB}"
)

# ---------------------------------------------------------------------
# Engine  – **no custom json_serializer**
# ---------------------------------------------------------------------
engine = create_async_engine(
    DATABASE_URL,
    echo=False,
    pool_pre_ping=True,
    pool_size=20,
    max_overflow=20,
    pool_timeout=30,
    pool_recycle=1800,
    connect_args={
        "command_timeout": 120,
        "timeout": 60,
        "server_settings": {
            "statement_timeout": "120000",
            "idle_in_transaction_session_timeout": "120000",
            "application_name": "job-scout",
        },
    },
)


class Base(MappedAsDataclass, DeclarativeBase):
    """Base class for all models with dataclass support."""

    pass


async_session_maker = async_sessionmaker(
    engine, expire_on_commit=False, autocommit=False, autoflush=False
)


# ---------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------
def _log_db_err(e: Exception) -> None:
    logger.error("Database error: %s", e, exc_info=True)
    if isinstance(e, DBAPIError):
        logger.error("Connection invalidated: %s", e.connection_invalidated)
    raise


@backoff.on_exception(
    backoff.expo, (SQLAlchemyError, ConnectionError), max_tries=3, max_time=30
)
@contextlib.asynccontextmanager
async def get_session() -> AsyncIterator[AsyncSession]:
    session = async_session_maker()
    try:
        yield session
        await session.commit()
    except SQLAlchemyError as e:
        await session.rollback()
        _log_db_err(e)
    finally:
        await session.close()


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
