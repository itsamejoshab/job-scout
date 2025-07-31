#!/usr/bin/env python3
"""
Simple test script to verify the scraper functionality.
"""

import asyncio
import logging
from app.services.scrapers.scraper import ScraperService
from app.db.models import JobSource
from app.db.load import load_search_settings

# Configure logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


async def test_scraper():
    """Test the scraper functionality."""
    
    try:
        # Load search settings first
        logger.info("Loading search settings...")
        await load_search_settings()
        
        # Initialize scraper service
        logger.info("Initializing scraper service...")
        scraper_service = ScraperService()
        
        # Test LinkedIn scraper
        logger.info("Testing LinkedIn scraper...")
        result = await scraper_service.run_full_scrape(JobSource.LINKEDIN)
        
        logger.info(f"LinkedIn scrape result: {result}")
        
        # Test Indeed scraper (should return empty results for now)
        logger.info("Testing Indeed scraper...")
        result_indeed = await scraper_service.run_full_scrape(JobSource.INDEED)
        
        logger.info(f"Indeed scrape result: {result_indeed}")
        
        return True
        
    except Exception as e:
        logger.error(f"Test failed: {str(e)}")
        return False


if __name__ == "__main__":
    success = asyncio.run(test_scraper())
    if success:
        print("✅ Scraper test completed successfully!")
    else:
        print("❌ Scraper test failed!")
        exit(1) 