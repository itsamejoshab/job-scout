import logging
from typing import (
    Dict,
    Any,
    List,
    Optional,
    TypedDict,
    cast,
    Protocol,
    runtime_checkable,
)
import asyncio
from temporalio.worker import Worker
from temporalio.client import Client
from temporalio import workflow
import os
from app.activities import (
    test_scrape_activity,
    scraper,
    finder,
    duplicate_remover,
    basic_filter,
    detailer,
    advanced_filter,
    smart_filter,
    notifier
)
from app.workflow import MainWorkflow

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
)
logger = logging.getLogger(__name__)

# Constants
TEMPORAL_HOST = os.getenv("TEMPORAL_ADDRESS", "temporal:7233")
TASK_QUEUE = "main-task-queue"

class DummyResult(TypedDict, total=False):
    """Result type for the entire pipeline."""
    status: str


# this just registers the worker with the workflow activities
# it does not actually run them
async def register_worker():
    """Run the Temporal worker for the main pipeline."""
    logger.info("Worker.py: run_worker(): Starting main pipeline worker...")

    # Connect to Temporal
    client = await Client.connect(TEMPORAL_HOST)
    logger.info(f"Connected to Temporal at {TEMPORAL_HOST}")

    worker = Worker(
        client,
        task_queue=TASK_QUEUE,
        workflows=[MainWorkflow],
        activities=[    
            test_scrape_activity,
            scraper,
            finder,
            duplicate_remover,
            basic_filter,
            detailer,
            advanced_filter,
            smart_filter,
            notifier
        ],
    )

    try:
        await worker.run()
    except Exception as e:
        logger.error(f"Worker error! {str(e)}")
        raise

if __name__ == "__main__":
    asyncio.run(register_worker())
