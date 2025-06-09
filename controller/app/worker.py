import asyncio
import logging
import os

from temporalio.client import Client
from temporalio.worker import Worker

from workflows.main_pipeline_workflow import MainPipelineWorkflow
from app.activities import (
    log_pipeline_error,
    check_service_processed,
)

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
)
logger = logging.getLogger(__name__)

# Constants
TEMPORAL_HOST = os.getenv("TEMPORAL_HOST", "temporal:7233")
TASK_QUEUE = "main-pipeline"


async def run_worker():
    """Run the Temporal worker for the main pipeline."""
    logger.info("Starting main pipeline worker...")

    # Connect to Temporal
    client = await Client.connect(TEMPORAL_HOST)
    logger.info(f"Connected to Temporal at {TEMPORAL_HOST}")

    worker = Worker(
        client,
        task_queue=TASK_QUEUE,
        workflows=[MainPipelineWorkflow],
        activities=[
            log_pipeline_error,
            check_service_processed,
        ],
    )

    logger.info(f"Worker started on task queue: {TASK_QUEUE}")

    try:
        # Run the worker
        await worker.run()
    except Exception as e:
        logger.error(f"Worker error: {str(e)}")
        raise


if __name__ == "__main__":
    asyncio.run(run_worker())
