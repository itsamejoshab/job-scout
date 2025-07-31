import logging
from typing import List, Dict, Any, Optional
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession
import asyncio
from datetime import datetime

from app.db.database import get_session
from app.db.models import Jobs, SearchSettings, JobSource, ScraperSettings
from .providers.base import BaseScraper, JobData
from .providers.linkedin import LinkedInScraper
from .providers.indeed import IndeedScraper

logger = logging.getLogger(__name__)


class ScraperService:
    """Main scraper service that manages different job site scrapers."""
    
    def __init__(self):
        self.scrapers = {
            JobSource.LINKEDIN: LinkedInScraper,
            JobSource.INDEED: IndeedScraper,
        }
    
    def get_scraper(self, job_source: JobSource, search_settings: Dict[str, Any]) -> BaseScraper:
        """
        Get the appropriate scraper for the job source.
        
        Args:
            job_source: JobSource enum value
            search_settings: Search configuration
            
        Returns:
            BaseScraper instance
        """
        scraper_class = self.scrapers.get(job_source)
        if not scraper_class:
            raise ValueError(f"No scraper available for job source: {job_source}")
        
        return scraper_class(search_settings)
    
    async def get_search_settings(self, job_source: JobSource) -> Dict[str, Any]:
        """
        Get search settings from database for the specific job source.
        Combines universal search settings with scraper-specific settings.
        
        Args:
            job_source: JobSource enum value
            
        Returns:
            Dictionary with combined search configuration
        """
        async with get_session() as session:
            # Get universal search settings
            result = await session.execute(select(SearchSettings))
            universal_settings = result.scalar_one_or_none()
            
            if not universal_settings:
                raise ValueError("No universal search settings found in database")
            
            # Get scraper-specific settings
            result = await session.execute(
                select(ScraperSettings).where(ScraperSettings.job_source == job_source)
            )
            scraper_settings = result.scalar_one_or_none()
            
            if not scraper_settings:
                raise ValueError(f"No scraper settings found in database for job source: {job_source.value}")
            
            # Combine universal and scraper-specific settings
            combined_settings = universal_settings.to_dict()
            combined_settings.update(scraper_settings.to_dict())
            
            return combined_settings
    
    async def scrape_jobs(self, job_source: JobSource = JobSource.LINKEDIN) -> List[JobData]:
        """
        Scrape jobs from the specified source.
        
        Args:
            job_source: JobSource enum value
            
        Returns:
            List of JobData objects
        """
        logger.info(f"Starting job scraping for {job_source.value}")
        
        # Get search settings
        search_settings = await self.get_search_settings(job_source)
        
        # Get scraper instance
        scraper = self.get_scraper(job_source, search_settings)
        
        all_jobs = []
        
        # Handle hardcoded URLs first (if any)
        hardcoded_urls = search_settings.get('hardcoded_urls', [])
        if hardcoded_urls:
            logger.info(f"Processing {len(hardcoded_urls)} hardcoded URLs")
            for url_config in hardcoded_urls:
                try:
                    logger.info(f"Scraping hardcoded URL: {url_config.get('description', url_config['url'])}")
                    jobs = await scraper.scrape_hardcoded_url(url_config)
                    all_jobs.extend(jobs)
                    
                    # Add delay between URLs to be respectful
                    await asyncio.sleep(1)
                    
                except Exception as e:
                    logger.error(f"Error scraping hardcoded URL {url_config.get('url', 'unknown')}: {str(e)}")
                    continue
        
        # Handle regular search queries
        search_queries = search_settings.get('search_queries', [])
        if search_queries:
            logger.info(f"Processing {len(search_queries)} search queries")
            for query in search_queries:
                try:
                    logger.info(f"Scraping with query: {query}")
                    jobs = await scraper.scrape_jobs(query)
                    all_jobs.extend(jobs)
                    
                    # Add delay between queries to be respectful
                    await asyncio.sleep(1)
                    
                except Exception as e:
                    logger.error(f"Error scraping with query {query}: {str(e)}")
                    continue
        
        logger.info(f"Scraped {len(all_jobs)} total jobs from {job_source.value}")
        return all_jobs
    
    async def save_jobs_to_database(self, jobs: List[JobData], job_source: JobSource) -> int:
        """
        Save scraped jobs to database, avoiding duplicates.
        
        Args:
            jobs: List of JobData objects
            job_source: JobSource enum value for the scraper used
            
        Returns:
            Number of new jobs saved
        """
        if not jobs:
            return 0
        
        async with get_session() as session:
            new_jobs_count = 0
            
            for job_data in jobs:
                try:
                    # Check if job already exists (by URL)
                    existing_job = await session.execute(
                        select(Jobs).where(Jobs.job_url == job_data.job_url)
                    )
                    existing_job = existing_job.scalar_one_or_none()
                    
                    if existing_job:
                        logger.debug(f"Job already exists: {job_data.title}")
                        continue
                    
                    # Create new job record
                    new_job = Jobs(
                        job_source=job_source,
                        title=job_data.title,
                        company=job_data.company,
                        description=job_data.description,
                        location=job_data.location,
                        date=job_data.date or datetime.now(),
                        job_url=job_data.job_url,
                        new=True,
                        duplicate=False,
                        relevant=False,
                        promising=False,
                        notified=False
                    )
                    
                    session.add(new_job)
                    new_jobs_count += 1
                    
                except Exception as e:
                    logger.error(f"Error saving job {job_data.title}: {str(e)}")
                    continue
            
            await session.commit()
            logger.info(f"Saved {new_jobs_count} new jobs to database")
            return new_jobs_count
    
    async def run_full_scrape(self, job_source: JobSource = JobSource.LINKEDIN) -> Dict[str, Any]:
        """
        Run complete scraping process: scrape jobs and save to database.
        
        Args:
            job_source: JobSource enum value
            
        Returns:
            Dictionary with scraping results
        """
        logger.info(f"Starting full scrape for {job_source.value}")
        
        try:
            # Scrape jobs
            scraped_jobs = await self.scrape_jobs(job_source)
            
            # Save to database
            saved_count = await self.save_jobs_to_database(scraped_jobs, job_source)
            
            return {
                "status": "success",
                "job_source": job_source.value,
                "scraped_count": len(scraped_jobs),
                "saved_count": saved_count,
                "duplicate_count": len(scraped_jobs) - saved_count
            }
        
        except Exception as e:
            logger.error(f"Error in full scrape: {str(e)}")
            return {
                "status": "error",
                "job_source": job_source.value,
                "error": str(e)
            }

