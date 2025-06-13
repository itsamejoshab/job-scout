from fastapi import FastAPI
import asyncio
from temporalio.client import Client

app = FastAPI()

# Connect to Temporal server (make sure it's running locally)
async def get_temporal_client():
    return await Client.connect("localhost:7233")

@app.get("/")
async def root():
    return {"message": "FastAPI is running!"}

@app.post("/trigger-workflow")
async def trigger_workflow():
    # Connect to Temporal server
    client = await get_temporal_client()

    # Start the workflow
    handle = await client.start_workflow(
        workflow_class="DummyWorkflow",  # Workflow name to trigger
        task_queue="test-task-queue",   # Task queue your worker is listening on
        args=[],                        # Arguments to pass to the workflow (empty for now)
        id="dummy-workflow-id",         # Workflow ID (must be unique)
    )

    return {"workflow_id": handle.id, "run_id": handle.run_id}

