from __future__ import annotations

from temporalio import workflow
from temporalio.common import RetryPolicy
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
from datetime import timedelta
import logging
from dataclasses import dataclass, field
from app.activities import scraper
logger = logging.getLogger(__name__)


class WorkflowState(TypedDict, total=False):
    """State for the workflow."""
    status: str
    job_source: str
    scraped_count: int
    saved_count: int

class WorkflowResult(TypedDict, total=False):
    """Result type for workflow operations."""
    status: str
    job_source: str
    scraped_count: int
    saved_count: int
    error: Optional[str]

@workflow.defn
class MainWorkflow:
    """Main workflow for job scraping"""

    def __init__(self) -> None:
        """Initialize workflow state."""
        logger.info("MainWorkflow Initializing...")
        self._state = WorkflowState()

    @workflow.run
    async def run(self, job_source: str = "LINKEDIN") -> WorkflowResult:
        """Run the main job scraping workflow."""
        try:
            logger.info(f"MainWorkflow: Starting job scraping for {job_source}")
            
            result = await workflow.execute_activity(
                scraper,
                job_source,
                start_to_close_timeout=timedelta(minutes=30),
                retry_policy=RetryPolicy(
                    initial_interval=timedelta(seconds=10),
                    maximum_interval=timedelta(minutes=5),
                    maximum_attempts=3,
                )
            )
            
            logger.info(f"MainWorkflow: Scraping completed successfully: {result}")
            
            return {
                "status": result.get("status", "unknown"),
                "job_source": job_source,
                "scraped_count": result.get("scraped_count", 0),
                "saved_count": result.get("saved_count", 0),
                "error": result.get("error")
            }

        except Exception as e:
            logger.error(f"MainWorkflow error: {e}")
            return {
                "status": "error",
                "job_source": job_source,
                "scraped_count": 0,
                "saved_count": 0,
                "error": str(e)
            }
