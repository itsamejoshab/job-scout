import asyncio
import json
from database import get_session
import logging
from datetime import datetime
from sqlalchemy import (ARRAY, Boolean, Column, DateTime, Enum, Float,
                        ForeignKey, Integer, Interval, PrimaryKeyConstraint,
                        String, Text, JSON, select)
import os
from db.models import SearchSettings

logger = logging.getLogger(__name__)

async def load_initial_data():
    try:
        file_path = os.path.join(os.path.dirname(__file__), "search_settings.json")
        with open(file_path, "r") as file:
            data = json.load(file)

        async with get_session() as session:
            result = await session.execute(select(SearchSettings))
            existing_entry = result.scalars().first()
            if existing_entry:
                logging.info("Search Settings already have data... skipping.")
                return
            
            # Create a new SearchSettings instance
            defaults = SearchSettings(
                search_queries=data["search_queries"],
                desc_include_words=data["desc_include_words"],
                desc_exclude_words=data["desc_exclude_words"],
                title_include=data["title_include"],
                title_exclude=data["title_exclude"],
                company_exclude=data["company_exclude"],
                non_remote_phrases=data["non_remote_phrases"],
                timespan=data["timespan"],
                pages_to_scrape=data["pages_to_scrape"],
                rounds=data["rounds"],
                created_at=datetime.now(),
                updated_at=datetime.now()
            )

            session.add(defaults)
            await session.flush()
            logger.info("Search Settings successfully initialized.")
    except json.JSONDecodeError:
        logger.error(f"Invalid JSON format in file: \n\n {data} \n\n")
    except Exception as e:
        logger.error(f"Error inserting SearchSettings data: {e}")

async def main():
    await load_initial_data()

if __name__ == "__main__":
    asyncio.run(main())