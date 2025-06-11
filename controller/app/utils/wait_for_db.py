import logging
import time
import asyncio
from sqlalchemy import text
from sqlalchemy.ext.asyncio import create_async_engine
import os

# Configure logging
logger = logging.getLogger(__name__)


async def wait_for_db(max_retries=30, retry_interval=1):
    """Wait for database to be ready.

    Args:
        max_retries (int): Maximum number of connection attempts
        retry_interval (int): Seconds to wait between attempts

    Returns:
        None

    Raises:
        Exception: If database connection fails after max_retries
    """
    # Ensure we're using asyncpg driver
    usr = os.getenv("POSTGRES_USER", "defaultx")
    pwd = os.getenv("POSTGRES_PASSWORD", "defaultx")
    hst = os.getenv("POSTGRES_HOST", "postgres-db")
    prt = os.getenv("POSTGRES_PORT", 5432)
    dbb = os.getenv("POSTGRES_DB", "dbx")

    db_url = f"postgresql+asyncpg://{usr}:{pwd}@{hst}:{prt}/{dbb}"

    if not db_url.startswith("postgresql+asyncpg"):
        db_url = db_url.replace("postgresql://", "postgresql+asyncpg://")

    engine = create_async_engine(db_url)

    for attempt in range(max_retries):
        try:
            async with engine.connect() as conn:
                await conn.execute(text("SELECT 1"))
                logger.info("Successfully connected to database")
                logger.info("Database is ready for connections")
                return
        except Exception as e:
            if attempt == max_retries - 1:
                raise
            logger.warning(
                f"Database not ready (attempt {attempt + 1}/{max_retries}): {e}"
            )
            time.sleep(retry_interval)


def main():
    """Entry point for running the script directly."""
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
    )
    asyncio.run(wait_for_db())


if __name__ == "__main__":
    main()
