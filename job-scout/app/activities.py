from temporalio import activity
from sqlalchemy import text, select
#from shared.db.database import get_session
import logging
import json
import time
import backoff
from sqlalchemy.exc import SQLAlchemyError, OperationalError
from typing import Dict, Any

logger = logging.getLogger(__name__)

@activity.defn
async def test_scrape_activity(job_source: str) -> Dict[str, Any]:
    """
    Simple test activity to verify workflow-activity communication works.
    """
    logger.info(f"Test activity called with job_source: {job_source}")
    
    return {
        "status": "success",
        "job_source": job_source,
        "scraped_count": 0,
        "saved_count": 0,
        "message": "Test activity completed successfully"
    }

@activity.defn
async def scraper(job_source: str) -> Dict[str, Any]:
    """
    Activity to scrape jobs from the specified source.
    This runs outside the workflow sandbox and can access environment variables and database.
    """
    try:
        logger.info(f"Starting scraping ACTIVITY for {job_source}")
        

        from app.services.scrapers.scraper import ScraperService
        from app.db.models import JobSource
        from app.db.load import load_search_settings
        
        try:
            source_enum = JobSource(job_source)
            logger.info(f"{source_enum}")
        except ValueError:
            return {
                "status": "error",
                "job_source": job_source,
                "scraped_count": 0,
                "saved_count": 0,
                "error": f"Invalid job source: {job_source}"
            } 

        await load_search_settings()
        
        scraper_service = ScraperService()
        
        result = await scraper_service.run_full_scrape(source_enum)
        
        logger.info(f"Scraping activity completed: {result}")
        return result
        
    except Exception as e:
        logger.error(f"Error in scraping activity: {str(e)}")
        return {
            "status": "error",
            "job_source": job_source,
            "scraped_count": 0,
            "saved_count": 0,
            "error": str(e)
        }

@activity.defn
async def test():
    config_path = 'app/search_config.json'

    with open(config_path, "r") as file:
        search_config = json.load(file)

    logger.info(search_config['search_queries'])

    return True

@activity.defn
async def finder():
    time.sleep(10)
    return True

@activity.defn
async def duplicate_remover():
    time.sleep(10)
    return True

@activity.defn
async def basic_filter():
    time.sleep(10)
    return True

@activity.defn
async def detailer():
    time.sleep(10)
    return True

@activity.defn
async def advanced_filter():
    time.sleep(10)
    return True

@activity.defn
async def smart_filter():
    time.sleep(10)
    return True

@activity.defn
async def notifier():
    time.sleep(10)
    return True