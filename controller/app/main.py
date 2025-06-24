from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
import logging
from contextlib import asynccontextmanager
import asyncio
from temporalio.client import Client
from app.api import router
from app.core.config import settings
from prometheus_fastapi_instrumentator import Instrumentator
from sqlalchemy.exc import SQLAlchemyError
from sqlalchemy import text
from typing import Dict
from shared.db.database import async_session_maker

# Configure logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

app = FastAPI(
    title=settings.PROJECT_NAME,
    version=settings.VERSION,
    description="Controller Service API",
    docs_url="/controller/docs",
    redoc_url="/controller/redoc",
    openapi_url="/controller/openapi.json",
)


###############
# include routers
app.include_router(router, prefix="/api/v0", tags=["controller"])
###############



if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=8000, log_level=settings.LOG_LEVEL)