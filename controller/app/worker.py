import logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)
logger.info("Worker.py is running!")

import asyncio
from temporalio.worker import Worker
from temporalio.client import Client
from temporalio import workflow
import os

# Constants
TEMPORAL_ADDRESS = os.getenv("TEMPORAL_ADDRESS", "temporal:7233")
TASK_QUEUE = "main-pipeline"

# Define a dummy workflow

# Define a dummy workflow with the required decorator
@workflow.defn
class DummyWorkflow:
    @workflow.run
    async def run(self) -> str:
        return "Hello, Temporal!"

async def test():
    # Connect to the Temporal server (default localhost:7233)
    client = await Client.connect(TEMPORAL_ADDRESS)

    # Create a worker that listens to a task queue
    worker = Worker(
        client,
        task_queue=TASK_QUEUE,
        workflows=[DummyWorkflow],  # Register the dummy workflow
    )

    print("Starting worker...")

    # Run the worker (this will block until the worker is stopped)
    await worker.run()

if __name__ == "__main__":
    asyncio.run(test())
