import asyncio
import logging
from typing import List, Dict, Any, Optional
from datetime import datetime
import aiohttp
from bs4 import BeautifulSoup
import re
from urllib.parse import quote, urljoin
import time

from .base import BaseScraper, JobData
from app.db.models import JobSource

logger = logging.getLogger(__name__)


class LinkedInScraper(BaseScraper):
    """LinkedIn job scraper implementation using the proven API approach."""
    
    def __init__(self, search_settings: Dict[str, Any]):
        super().__init__(search_settings)
        self.base_url = "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search"
        self.session = None
    
    def get_job_source(self) -> JobSource:
        return JobSource.LINKEDIN
    
    def build_search_url(self, search_query: Dict[str, str]) -> str:
        """
        Build LinkedIn API search URL with parameters.
        
        Args:
            search_query: Dictionary with 'keywords', 'location', 'f_WT'
            
        Returns:
            Complete LinkedIn API search URL
        """
        keywords = quote(search_query.get('keywords', ''))
        location = search_query.get('location', '')
        f_wt = search_query.get('f_WT', '')
        timespan_code = self.search_settings.get('timespan_code', 'r84600')  # 24 hours default
        
        # Build URL parameters
        params = []
        if keywords:
            params.append(f"keywords={keywords}")
        if location:
            params.append(f"f_PP={location}")
        if f_wt:
            params.append(f"f_WT={f_wt}")
        
        # Add default parameters
        params.extend([
            "geoId=",
            f"f_TPR={timespan_code}",
            "start=0"
        ])
        
        url = f"{self.base_url}?{'&'.join(params)}"
        logger.info(f"Built LinkedIn API search URL: {url}")
        return url
    
    async def scrape_jobs(self, search_query: Dict[str, str]) -> List[JobData]:
        """
        Scrape jobs from LinkedIn based on search query using the API approach.
        
        Args:
            search_query: Dictionary with search parameters
            
        Returns:
            List of JobData objects
        """
        if not self.validate_search_query(search_query):
            raise ValueError("Invalid search query: missing required fields")
        
        jobs = []
        pages_to_scrape = self.search_settings.get('pages_to_scrape', 1)
        
        try:
            async with aiohttp.ClientSession() as session:
                self.session = session
                
                for page in range(pages_to_scrape):
                    logger.info(f"Scraping LinkedIn page {page + 1}/{pages_to_scrape}")
                    
                    # Build URL for this page
                    page_url = self.build_search_url(search_query)
                    page_url = page_url.replace("start=0", f"start={25 * page}")
                    
                    # Scrape this page
                    page_jobs = await self._scrape_page(page_url)
                    jobs.extend(page_jobs)
                    
                    # Exit if no results
                    if len(page_jobs) == 0:
                        break
                    
                    # Add delay between pages to be respectful
                    if page < pages_to_scrape - 1:
                        await asyncio.sleep(2)
        
        except Exception as e:
            logger.error(f"Error scraping LinkedIn jobs: {str(e)}")
            raise
        
        finally:
            self.session = None
        
        logger.info(f"Scraped {len(jobs)} jobs from LinkedIn")
        return jobs
    
    async def _scrape_page(self, url: str) -> List[JobData]:
        """
        Scrape a single page of job listings using LinkedIn API.
        
        Args:
            url: LinkedIn API search URL
            
        Returns:
            List of JobData objects from this page
        """
        jobs = []
        
        try:
            headers = {
                'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.0.0.0 Safari/537.36'
            }
            
            async with self.session.get(url, headers=headers, timeout=30) as response:
                if response.status != 200:
                    logger.error(f"Failed to fetch LinkedIn page: {response.status}")
                    return jobs
                
                html = await response.text()
                soup = BeautifulSoup(html, 'html.parser')
                
                # Parse job cards using the proven approach from old code
                jobs = self._transform_job_cards(soup)
        
        except Exception as e:
            logger.error(f"Error scraping LinkedIn page {url}: {str(e)}")
        
        return jobs
    
    def _transform_job_cards(self, soup: BeautifulSoup) -> List[JobData]:
        """
        Transform LinkedIn job cards into JobData objects using proven parsing logic.
        
        Args:
            soup: BeautifulSoup object of the page
            
        Returns:
            List of JobData objects
        """
        jobs = []
        
        try:
            divs = soup.find_all('div', class_='base-search-card__info')
        except:
            logger.info("Empty page, no jobs found")
            return jobs
        
        for item in divs:
            try:
                # Extract title
                title_elem = item.find('h3')
                title = title_elem.text.strip() if title_elem else "Unknown Title"
                
                # Extract company
                company_elem = item.find('a', class_='hidden-nested-link')
                company = company_elem.text.strip().replace('\n', ' ') if company_elem else "Unknown Company"
                
                # Extract location
                location_elem = item.find('span', class_='job-search-card__location')
                location = location_elem.text.strip() if location_elem else "Unknown Location"
                
                # Extract job URL from parent div
                parent_div = item.parent
                entity_urn = parent_div.get('data-entity-urn', '')
                job_posting_id = entity_urn.split(':')[-1] if entity_urn else ''
                job_url = f'https://www.linkedin.com/jobs/view/{job_posting_id}/' if job_posting_id else ''
                
                # Extract date
                date_tag_new = item.find('time', class_='job-search-card__listdate--new')
                date_tag = item.find('time', class_='job-search-card__listdate')
                date = None
                
                if date_tag and date_tag.get('datetime'):
                    try:
                        date = datetime.fromisoformat(date_tag['datetime'].replace('Z', '+00:00'))
                    except:
                        pass
                elif date_tag_new and date_tag_new.get('datetime'):
                    try:
                        date = datetime.fromisoformat(date_tag_new['datetime'].replace('Z', '+00:00'))
                    except:
                        pass
                
                # Create JobData object
                job_data = JobData(
                    title=title,
                    company=company,
                    location=location,
                    job_url=job_url,
                    date=date,
                    job_source=self.job_source
                )
                
                jobs.append(job_data)
                
            except Exception as e:
                logger.warning(f"Error parsing job card: {str(e)}")
                continue
        
        return jobs
    
    def parse_job_listing(self, job_element: Any) -> Optional[JobData]:
        """
        Parse a LinkedIn job listing element into JobData.
        This method is kept for compatibility but the main parsing is done in _transform_job_cards.
        
        Args:
            job_element: BeautifulSoup element representing a job card
            
        Returns:
            JobData object or None if parsing fails
        """
        # This method is not used in the new implementation
        # The main parsing is done in _transform_job_cards
        return None
    
    async def get_job_description(self, job_url: str) -> Optional[str]:
        """
        Get detailed job description from job URL using proven parsing logic.
        
        Args:
            job_url: URL of the specific job posting
            
        Returns:
            Job description text or None if failed
        """
        if not self.session:
            logger.error("No active session for fetching job description")
            return None
        
        try:
            headers = {
                'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.0.0.0 Safari/537.36'
            }
            
            async with self.session.get(job_url, headers=headers, timeout=30) as response:
                if response.status != 200:
                    return None
                
                html = await response.text()
                soup = BeautifulSoup(html, 'html.parser')
                
                # Use the proven description parsing logic from old code
                return self._transform_job_description(soup)
        
        except Exception as e:
            logger.error(f"Error fetching job description from {job_url}: {str(e)}")
            return None
    
    def _transform_job_description(self, soup: BeautifulSoup) -> str:
        """
        Transform job description using proven parsing logic from old code.
        
        Args:
            soup: BeautifulSoup object of the job page
            
        Returns:
            Cleaned job description text
        """
        div = soup.find('div', class_='description__text description__text--rich')
        if div:
            # Remove unwanted elements
            for element in div.find_all(['span', 'a']):
                element.decompose()

            # Replace bullet points
            for ul in div.find_all('ul'):
                for li in ul.find_all('li'):
                    li.insert(0, '-')

            text = div.get_text(separator='\n').strip()
            text = text.replace('\n\n', '')
            text = text.replace('::marker', '-')
            text = text.replace('-\n', '- ')
            text = text.replace('Show less', '').replace('Show more', '')
            return text
        else:
            return "Could not find Job Description"

    async def scrape_hardcoded_url(self, url_config: Dict[str, Any]) -> List[JobData]:
        """
        Scrape jobs from a hardcoded URL.
        
        Args:
            url_config: Dictionary with 'url', 'description', 'is_remote'
            
        Returns:
            List of JobData objects
        """
        url = url_config['url']
        is_remote = url_config.get('is_remote', False)
        pages_to_scrape = self.search_settings.get('pages_to_scrape', 1)
        
        jobs = []
        
        try:
            async with aiohttp.ClientSession() as session:
                self.session = session
                
                for page in range(pages_to_scrape):
                    logger.info(f"Scraping hardcoded URL page {page + 1}/{pages_to_scrape}")
                    
                    # Build URL for this page
                    page_url = url
                    if page > 0:
                        # Add pagination parameter
                        if 'start=' in page_url:
                            page_url = page_url.replace('start=0', f'start={25 * page}')
                        else:
                            page_url = f"{page_url}&start={25 * page}"
                    
                    # Scrape this page
                    page_jobs = await self._scrape_page(page_url)
                    
                    # Add remote flag to jobs if specified
                    if is_remote:
                        for job in page_jobs:
                            # You could add an is_remote field to JobData if needed
                            pass
                    
                    jobs.extend(page_jobs)
                    
                    # Exit if no results
                    if len(page_jobs) == 0:
                        break
                    
                    # Add delay between pages to be respectful
                    if page < pages_to_scrape - 1:
                        await asyncio.sleep(2)
        
        except Exception as e:
            logger.error(f"Error scraping hardcoded URL {url}: {str(e)}")
            raise
        
        finally:
            self.session = None
        
        logger.info(f"Scraped {len(jobs)} jobs from hardcoded URL: {url_config.get('description', url)}")
        return jobs
    
    @classmethod
    def get_default_settings(cls) -> Dict[str, Any]:
        """Get the default settings for LinkedIn scraper."""
        return {
            "job_source": JobSource.LINKEDIN,
            "search_queries": [
                {"keywords": "IT Help Desk", "location": "101076143", "f_WT": ""},
                {"keywords": "IT Help Desk", "location": "102252967", "f_WT": ""},
                {"keywords": "IT Help Desk", "location": "102570379", "f_WT": ""},
                {"keywords": "IT Help Desk", "location": "104198807", "f_WT": ""},
                {"keywords": "IT Help Desk", "location": "104779438", "f_WT": ""},
                {"keywords": "IT Help Desk", "location": "105135351", "f_WT": "3"},
                {"keywords": "IT Help Desk", "location": "105142029", "f_WT": "3"},
                {"keywords": "IT Help Desk", "location": "106362955", "f_WT": "3"},
                {"keywords": "IT Help Desk", "location": "105135351", "f_WT": "2"},
                {"keywords": "IT Help Desk", "location": "105142029", "f_WT": "2"},
                {"keywords": "IT Help Desk", "location": "106362955", "f_WT": "2"},
                {"keywords": "Application Support", "location": "101076143", "f_WT": ""},
                {"keywords": "Application Support", "location": "102252967", "f_WT": ""},
                {"keywords": "Application Support", "location": "102570379", "f_WT": ""},
                {"keywords": "Application Support", "location": "104198807", "f_WT": ""},
                {"keywords": "Application Support", "location": "104779438", "f_WT": ""},
                {"keywords": "Application Support", "location": "105135351", "f_WT": "3"},
                {"keywords": "Application Support", "location": "105142029", "f_WT": "3"},
                {"keywords": "Application Support", "location": "106362955", "f_WT": "3"},
                {"keywords": "Application Support", "location": "105135351", "f_WT": "2"},
                {"keywords": "Application Support", "location": "105142029", "f_WT": "2"},
                {"keywords": "Application Support", "location": "106362955", "f_WT": "2"}
            ],
            "hardcoded_urls": [
                {
                    "url": "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search?keywords=IT%20Help%20Desk&location=United%20States&&f_WT=2&geoId=&f_TPR=r84600",
                    "description": "IT Help Desk - Remote - United States",
                    "is_remote": True
                },
                {
                    "url": "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search?keywords=Application%20Support&location=United%20States&&f_WT=2&geoId=&f_TPR=r84600",
                    "description": "Application Support - Remote - United States",
                    "is_remote": True
                }
            ],
            "timespan_code": "r84600",
            "pages_to_scrape": 1,
            "rounds": 1
        }
