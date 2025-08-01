import asyncio
import json
import logging
import os
from pathlib import Path
from sqlalchemy import select
from app.db.database import get_session
from app.db.models import SearchSettings, ScraperSettings, JobSource
from app.services.scrapers.providers.linkedin import LinkedInScraper
from app.services.scrapers.providers.indeed import IndeedScraper

logger = logging.getLogger(__name__)

def load_json_file(file_path: str):
    """Load data from a JSON file."""
    try:
        with open(file_path, 'r', encoding='utf-8') as f:
            data = json.load(f)
            if isinstance(data, dict):
                return data
            elif isinstance(data, list):
                return data
            else:
                logger.error(f"Unexpected JSON structure in file {file_path}. Expected a dictionary or list, got {type(data).__name__}.")
                raise ValueError(f"Unexpected JSON structure: {type(data).__name__}")
    except FileNotFoundError:
        logger.error(f"JSON file not found: {file_path}")
        raise
    except json.JSONDecodeError as e:
        logger.error(f"Invalid JSON in file {file_path}: {e}")
        raise

async def load_search_settings():
    """Load initial search settings into the database from JSON files and provider classes."""
    
    current_dir = Path(__file__).parent
    
    universal_settings_path = current_dir / "universal_settings.json"
    universal_settings = load_json_file(str(universal_settings_path))

    scraper_settings_path = current_dir / "scraper_settings.json"
    scraper_settings = load_json_file(str(scraper_settings_path))[0] #TODO: This could be a list if there are multiple providers!
    
    valid_universal_settings_fields = {
        "desc_include_words",
        "desc_exclude_words",
        "title_include",
        "title_exclude",
        "company_exclude",
        "non_remote_phrases",
        "created_at",
        "updated_at"
    }

    #TODO: Validate scraper settings? Using the JobSource enum?
    #
    #

    universal_settings = {
        key: value for key, value in universal_settings.items() if key in valid_universal_settings_fields
    }
        
    try:
        async with get_session() as session:

            result_universal = await session.execute(select(SearchSettings))
            existing_universal = result_universal.scalar_one_or_none()
            
            result_scraper = await session.execute(select(ScraperSettings))
            existing_scraper = result_scraper.scalar_one_or_none()
            
            if not existing_universal:
                new_universal = SearchSettings(**universal_settings)
                session.add(new_universal)
                logger.info("Created universal search settings from JSON")
            else:
                logger.info("Universal search settings already exist in database")
             
            await session.commit()
            logger.info("Successfully loaded all search settings from JSON files and provider classes")

            if not existing_scraper:
                new_scraper = ScraperSettings(**scraper_settings)
                session.add(new_scraper)
                logger.info("Created scraper settings from JSON")
            else:
                logger.info("Scraper settings already exist in database")
             
            await session.commit()
            logger.info("Successfully loaded all scraper settings from JSON files and provider classes")

    except Exception as e:
        logger.error(f"Error loading search settings: {str(e)}")
        raise

if __name__ == "__main__":
    asyncio.run(load_search_settings())
