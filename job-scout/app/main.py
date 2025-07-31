
import logging
from api import router
from core.config import settings
from fastapi import FastAPI

# Configure logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

app = FastAPI(
    title=settings.PROJECT_NAME,
    version=settings.VERSION,
    description="Job-Scout Service API",
    docs_url="/job-scout/docs",
    redoc_url="/job-scout/redoc",
    openapi_url="/job-scout/openapi.json",
)

###############
# include routers
app.include_router(router, prefix="/api/v0", tags=["job-scout"])
###############

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000, log_level=settings.LOG_LEVEL)