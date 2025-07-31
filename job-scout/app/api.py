from fastapi import APIRouter, HTTPException
import logging
from datetime import datetime
from sqlalchemy import select, func
from app.workflow import MainWorkflow
from app.client import start_workflow, connect_with_retry
from app.db.database import get_session
from app.db.models import Jobs, SearchSettings, ScraperSettings, JobSource
from app.core.config import settings
from app.services.scrapers.scraper import ScraperService

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
async def run(job_source: str = "LINKEDIN"):
    """Run the job scraping workflow for the specified job source."""
    try:
        logger.info(f"{'-' * 10} RUN attempting for {job_source} {'-' * 10}")
        
        # Validate job source
        try:
            from app.db.models import JobSource
            source_enum = JobSource(job_source.upper())
        except ValueError:
            raise HTTPException(
                status_code=400, 
                detail=f"Invalid job source: {job_source}. Available sources: {[s.value for s in JobSource]}"
            )
        
        result = await start_workflow(
            MainWorkflow, 
            workflow_id=f"scrape-{job_source}-{datetime.now().strftime('%Y%m%d-%H%M%S')}", 
            task_queue="main-task-queue",
            args=[job_source]
        )

        return {
            "status": "workflow_started",
            "job_source": job_source,
            "workflow_id": result.id
        }

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error running workflow: {str(e)}")
        raise HTTPException(status_code=500, detail=str(e))

@router.post("/test")
async def test():
    """Test"""
    try:
        logger.info(f"TEST was successful")
    except Exception as e:
        logger.error(f"Error sending alert: {str(e)}")
        raise HTTPException(status_code=500, detail=str(e))

@router.post("/scrape")
async def scrape_jobs(job_source: str = "linkedin"):
    """
    Scrape jobs from the specified job source and save to database.
    
    Args:
        job_source: Job source to scrape (linkedin, indeed)
    """
    try:
        logger.info(f"{'-' * 10} SCRAPE attempting for {job_source} {'-' * 10}")
        
        # Validate job source
        try:
            source_enum = JobSource(job_source.lower())
        except ValueError:
            raise HTTPException(
                status_code=400, 
                detail=f"Invalid job source: {job_source}. Available sources: {[s.value for s in JobSource]}"
            )
        
        # Initialize scraper service
        scraper_service = ScraperService()
        
        # Run full scrape
        result = await scraper_service.run_full_scrape(source_enum)
        
        if result["status"] == "error":
            raise HTTPException(status_code=500, detail=result["error"])
        
        logger.info(f"Scrape completed successfully: {result}")
        return result

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error during scraping: {str(e)}")
        raise HTTPException(status_code=500, detail=str(e))

@router.get("/health")
async def health_check():
    """Basic health check endpoint."""
    return {"status": "healthy", "timestamp": datetime.now().isoformat()}

@router.get("/db-test")
async def test_database():
    """Test database connection and basic operations."""
    try:
        async with get_session() as session:
            # Test basic query
            result = await session.execute(select(SearchSettings))
            settings = result.scalars().first()
            return {
                "status": "connected",
                "search_settings_count": 1 if settings else 0,
                "timestamp": datetime.now().isoformat()
            }
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Database error: {str(e)}")

@router.get("/search-settings")
async def get_search_settings():
    """Get universal search settings (shared across all scrapers)."""
    try:
        async with get_session() as session:
            result = await session.execute(select(SearchSettings))
            settings = result.scalar_one_or_none()
            
            if settings:
                return settings.to_dict()
            else:
                return {"message": "No universal search settings found"}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@router.get("/scraper-settings")
async def get_scraper_settings(job_source: str = "linkedin"):
    """Get scraper-specific settings for the specified job source."""
    try:
        from app.db.models import JobSource
        
        # Validate job source
        try:
            source_enum = JobSource(job_source.lower())
        except ValueError:
            raise HTTPException(
                status_code=400, 
                detail=f"Invalid job source: {job_source}. Available sources: {[s.value for s in JobSource]}"
            )
        
        async with get_session() as session:
            result = await session.execute(
                select(ScraperSettings).where(ScraperSettings.job_source == source_enum)
            )
            settings = result.scalar_one_or_none()
            
            if settings:
                return settings.to_dict()
            else:
                return {"message": f"No scraper settings found for {job_source}"}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@router.get("/scraper-settings/all")
async def get_all_scraper_settings():
    """Get scraper settings for all job sources."""
    try:
        async with get_session() as session:
            result = await session.execute(select(ScraperSettings))
            settings = result.scalars().all()
            
            if settings:
                return {
                    setting.job_source.value: setting.to_dict() 
                    for setting in settings
                }
            else:
                return {"message": "No scraper settings found"}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@router.get("/jobs")
async def get_jobs(limit: int = 10, offset: int = 0):
    """Get list of jobs with pagination."""
    try:
        async with get_session() as session:
            result = await session.execute(
                select(Jobs).limit(limit).offset(offset)
            )
            jobs = result.scalars().all()
            return [job.to_dict() for job in jobs]
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@router.get("/jobs/stats")
async def get_job_stats():
    """Get job statistics."""
    try:
        async with get_session() as session:
            total = await session.execute(select(func.count(Jobs.id)))
            new_jobs = await session.execute(
                select(func.count(Jobs.id)).where(Jobs.new == True)
            )
            relevant_jobs = await session.execute(
                select(func.count(Jobs.id)).where(Jobs.relevant == True)
            )
            
            return {
                "total_jobs": total.scalar(),
                "new_jobs": new_jobs.scalar(),
                "relevant_jobs": relevant_jobs.scalar(),
                "timestamp": datetime.now().isoformat()
            }
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@router.get("/temporal-test")
async def test_temporal():
    """Test Temporal server connection."""
    try:
        client = await connect_with_retry()
        return {"status": "connected", "temporal_address": "temporal:7233"}
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Temporal error: {str(e)}")

@router.get("/workflow/{workflow_id}")
async def get_workflow_status(workflow_id: str):
    """Get workflow status by ID."""
    try:
        client = await connect_with_retry()
        handle = client.get_workflow_handle(workflow_id)
        desc = await handle.describe()
        return {
            "workflow_id": workflow_id,
            "status": desc.status.name,
            "run_id": desc.run_id
        }
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@router.get("/config")
async def get_config():
    """Get current application configuration."""
    return {
        "project_name": settings.PROJECT_NAME,
        "version": settings.VERSION,
        "database_url": settings.DATABASE_URL.split("@")[-1] if settings.DATABASE_URL else None,
        "temporal_address": settings.TEMPORAL_ADDRESS,
        "log_level": settings.LOG_LEVEL
    }