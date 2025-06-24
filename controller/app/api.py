from fastapi import APIRouter, HTTPException
import logging
from workflows.workflow import MainWorkflow
from .client import start_workflow

#from typing import List, Optional, Dict, Any, Tuple
#from sqlalchemy import select
#from sqlalchemy.ext.asyncio import AsyncSession
#import json
#from pydantic import BaseModel, Field, field_validator, model_validator
#from shared.db.session import get_session
#from shared.models.pipeline_run import PipelineRun, RunStatus, ServiceType, utc_now
#from shared.schemas.pipeline_run import PipelineRun as PipelineRunSchema
#from shared.models.clip import Clip, Show
#from shared.clients.minio_client import MinioClient
#from shared.utils.content_hash import generate_input_hash


logger = logging.getLogger(__name__)

router = APIRouter()

@router.post("/run")
async def run():
    """Test the endpoint to prove its running."""
    try:
        logger.info(f"RUN attempting...")
        
        result = await start_workflow(MainWorkflow, workflow_id=1, task_queue="main-workflow", args=None)

        logger.info(f"RUN was successful")

        return result

    except Exception as e:
        # Handle errors and update run status
        logger.error(f"Error running test: {str(e)}")

        raise HTTPException(status_code=500, detail=str(e))

@router.post("/test")
async def test():
    """Test"""
    try:
        logger.info(f"TEST was successful")
    except Exception as e:
        logger.error(f"Error sending alert: {str(e)}")
        raise HTTPException(status_code=500, detail=str(e))