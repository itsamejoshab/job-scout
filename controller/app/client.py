import asyncio
import logging
from temporalio.client import Client

logger = logging.getLogger(__name__)


async def connect_with_retry(max_retries=5, retry_delay=5):
    """Connect to Temporal server with retry logic."""
    logger.info("Connecting to Temporal server")
    for attempt in range(max_retries):
        try:
            return await Client.connect("temporal:7233")
        except Exception as e:
            logger.error(
                f"Failed to connect to Temporal server (attempt {attempt + 1}/{max_retries}): {e}"
            )
            if attempt == max_retries - 1:
                raise
            logger.warning(
                f"Failed to connect to Temporal server (attempt {attempt + 1}/{max_retries}): {e}"
            )
            await asyncio.sleep(retry_delay)


async def start_workflow(workflow_type, workflow_id, task_queue, args=None):
    """Start a workflow with the given parameters."""
    logger.info(f"Starting workflow: {workflow_id}")
    client = await connect_with_retry()
    return await client.start_workflow(
        workflow_type, id=workflow_id, task_queue=task_queue, args=args or []
    )
