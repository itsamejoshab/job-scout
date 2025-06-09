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

logger = logging.getLogger(__name__)


# Type definitions
@dataclass
class WorkflowState:
    """State for the workflow."""

    status: str


class WorkflowResult(TypedDict, total=False):
    """Result type for workflow operations."""

    status: str


class PipelineResult(TypedDict, total=False):
    """Result type for the entire pipeline."""

    status: str
    error: Optional[str]


@runtime_checkable
class WorkflowProtocol(Protocol):
    """Protocol defining required workflow methods."""

    def get_progress(self) -> Dict[str, Any]: ...
    async def approve_segmenter(self) -> None: ...
    async def retry_segmenter(self) -> None: ...
    async def run(self, input_data: Dict[str, Any]) -> PipelineResult: ...


@workflow.defn
class MainPipelineWorkflow:
    """Main pipeline workflow"""

    def __init__(self) -> None:
        """Initialize workflow state."""
        self._state = WorkflowState()

    @workflow.query
    def get_progress(self) -> Dict[str, Any]:
        """Get current workflow progress."""
        return {
            "state": self._state.current_video_index,
        }

    @workflow.run
    async def run(self, input_data: Dict[str, Any]) -> PipelineResult:
        """Run the main pipeline workflow."""
        try:
            logger.info("The main workflow is running")

            return await True

        except Exception as e:
            logger.error(f"Workflow error: {e}")
            return {
                "status": "error",
                "error": str(e),
            }
