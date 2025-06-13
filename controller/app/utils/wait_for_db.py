import logging
import time
import asyncio
from sqlalchemy import text
from sqlalchemy.ext.asyncio import create_async_engine
from app.core.config import settings

# Configure logging
logger = logging.getLogger(__name__)

async def wait_for_db(max_retries=30, retry_interval=1):

    db_url = settings.DATABASE_URL_ASYNC
    
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
