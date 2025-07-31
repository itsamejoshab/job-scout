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

def load_json_file(file_path: str) -> dict:
    """Load data from a JSON file."""
    try:
        with open(file_path, 'r', encoding='utf-8') as f:
            return json.load(f)
    except FileNotFoundError:
        logger.error(f"JSON file not found: {file_path}")
        raise
    except json.JSONDecodeError as e:
        logger.error(f"Invalid JSON in file {file_path}: {e}")
        raise

async def load_search_settings():
    """Load initial search settings into the database from JSON files and provider classes."""
    
    current_dir = Path(__file__).parent
    
    universal_settings_path = current_dir / "search_settings.json"
    universal_settings = load_json_file(str(universal_settings_path))
    
    valid_search_settings_fields = {
        "desc_include_words",
        "desc_exclude_words",
        "title_include",
        "title_exclude",
        "company_exclude",
        "non_remote_phrases",
        "created_at",
        "updated_at"
    }
    universal_settings = {
        key: value for key, value in universal_settings.items() if key in valid_search_settings_fields
    }
        
    try:
        async with get_session() as session:

            result = await session.execute(select(SearchSettings))
            existing_universal = result.scalar_one_or_none()
            
            if not existing_universal:
                new_universal = SearchSettings(**universal_settings)
                session.add(new_universal)
                logger.info("Created universal search settings from JSON")
            else:
                logger.info("Universal search settings already exist in database")
             
            await session.commit()
            logger.info("Successfully loaded all search settings from JSON files and provider classes")
            
    except Exception as e:
        logger.error(f"Error loading search settings: {str(e)}")
        raise

if __name__ == "__main__":
    asyncio.run(load_search_settings())
