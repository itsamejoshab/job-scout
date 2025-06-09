import logging
import time
from contextlib import asynccontextmanager
from typing import Dict

from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from prometheus_fastapi_instrumentator import Instrumentator
from shared.db.session import async_session_maker
from sqlalchemy.exc import SQLAlchemyError
from sqlalchemy import text

from app.api import router as controller_router
from app.core.config import settings
from shared.db.session import close_db

# Configure logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


@asynccontextmanager
async def lifespan(app: FastAPI):
    """
    Function that handles startup and shutdown events.
    """
    yield
    if async_session_maker._engine is not None:
        # Close the DB connection
        await async_session_maker.close()


app = FastAPI(
    title=settings.PROJECT_NAME,
    version=settings.VERSION,
    description="Controller Service API",
    docs_url="/controller/docs",
    redoc_url="/controller/redoc",
    openapi_url="/controller/openapi.json",
)

# CORS middleware
app.add_middleware(
    CORSMiddleware,
    allow_origins=settings.ALLOWED_ORIGINS,
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

# Instrument the app to expose metrics
instrumentator = Instrumentator()
instrumentator.instrument(app).expose(app)


# Request timing middleware
@app.middleware("http")
async def add_process_time_header(request: Request, call_next):
    start_time = time.time()
    response = await call_next(request)
    process_time = time.time() - start_time
    response.headers["X-Process-Time"] = str(process_time)
    return response


# Error handlers
@app.exception_handler(RequestValidationError)
async def validation_exception_handler(request: Request, exc: RequestValidationError):
    logger.error(f"Validation error: {exc.errors()}")

    # Process errors to ensure they're JSON serializable
    processed_errors = []
    for error in exc.errors():
        processed_error = {
            "type": error.get("type"),
            "loc": error.get("loc"),
            "msg": error.get("msg"),
            "input": error.get("input"),
        }
        # Handle context if it exists
        if "ctx" in error and error["ctx"]:
            processed_error["ctx"] = {}
            for key, value in error["ctx"].items():
                if isinstance(value, Exception):
                    processed_error["ctx"][key] = str(value)
                else:
                    processed_error["ctx"][key] = value
        processed_errors.append(processed_error)

    return JSONResponse(
        status_code=422,
        content={
            "detail": processed_errors,
            "message": "Input validation error",
        },
    )


@app.exception_handler(SQLAlchemyError)
async def sqlalchemy_exception_handler(request: Request, exc: SQLAlchemyError):
    logger.error(f"Database error: {str(exc)}")
    return JSONResponse(
        status_code=500,
        content={
            "message": "Internal server error",
            "detail": "A database error occurred",
        },
    )


# Health check endpoints
@app.get("/health", tags=["health"])
async def health_check() -> Dict:
    """Basic health check endpoint"""
    return {
        "status": "healthy",
        "service": settings.PROJECT_NAME,
        "version": settings.VERSION,
    }


@app.get("/health/detailed", tags=["health"])
async def detailed_health_check() -> Dict:
    """Detailed health check including database connection"""
    health_status = {
        "service": {
            "status": "healthy",
            "name": settings.PROJECT_NAME,
            "version": settings.VERSION,
        },
        "database": {"status": "unhealthy"},
    }

    try:
        # Test database connection using the session maker
        async with async_session_maker() as session:
            await session.execute(text("SELECT 1"))
            await session.commit()
        health_status["database"]["status"] = "healthy"
    except Exception as e:
        logger.error(f"Database health check failed: {str(e)}")
        health_status["service"]["status"] = "unhealthy"

    return health_status


# Startup and shutdown events
@app.on_event("startup")
async def startup_event():
    logger.info("Starting up Controller Service")


@app.on_event("shutdown")
async def shutdown_event():
    logger.info("Shutting down Controller Service")
    await close_db()


# Include routers
app.include_router(controller_router, prefix="/api/v0", tags=["controller"])


# Optional: Root endpoint
@app.get("/")
async def root():
    """Root endpoint with API information"""
    return {
        "service": settings.PROJECT_NAME,
        "version": settings.VERSION,
        "docs_url": "/controller/docs",
        "redoc_url": "/controller/redoc",
    }


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=8000, log_level=settings.LOG_LEVEL)
