from abc import ABC, abstractmethod
from typing import List, Dict, Any, Optional
from datetime import datetime
from dataclasses import dataclass
from app.db.models import JobSource


@dataclass
class JobData:
    """Standardized job data structure for all scrapers."""
    title: str
    company: str
    location: str
    job_url: str
    description: Optional[str] = None
    date: Optional[datetime] = None
    job_source: JobSource = JobSource.LINKEDIN


class BaseScraper(ABC):
    """Base class that all job scrapers must inherit from."""
    
    def __init__(self, search_settings: Dict[str, Any]):
        """
        Initialize scraper with search settings.
        
        Args:
            search_settings: Dictionary containing search configuration
        """
        self.search_settings = search_settings
        self.job_source = self.get_job_source()
    
    @abstractmethod
    def get_job_source(self) -> JobSource:
        """Return the job source enum for this scraper."""
        pass
    
    @abstractmethod
    async def scrape_jobs(self, search_query: Dict[str, str]) -> List[JobData]:
        """
        Scrape jobs based on the provided search query.
        
        Args:
            search_query: Dictionary with keys like 'keywords', 'location', 'f_WT'
            
        Returns:
            List of JobData objects
        """
        pass
    
    @abstractmethod
    async def scrape_hardcoded_url(self, url_config: Dict[str, Any]) -> List[JobData]:
        """
        Scrape jobs from a hardcoded URL.
        
        Args:
            url_config: Dictionary with 'url', 'description', 'is_remote'
            
        Returns:
            List of JobData objects
        """
        pass
    
    @abstractmethod
    def build_search_url(self, search_query: Dict[str, str]) -> str:
        """
        Build the search URL for the specific job site.
        
        Args:
            search_query: Dictionary with search parameters
            
        Returns:
            Complete search URL
        """
        pass
    
    @abstractmethod
    def parse_job_listing(self, job_element: Any) -> JobData:
        """
        Parse a single job listing element into JobData.
        
        Args:
            job_element: Raw job listing data from the scraper
            
        Returns:
            JobData object
        """
        pass
    
    def validate_search_query(self, search_query: Dict[str, str]) -> bool:
        """
        Validate that the search query has required fields.
        
        Args:
            search_query: Dictionary with search parameters
            
        Returns:
            True if valid, False otherwise
        """
        required_fields = ['keywords', 'location']
        return all(field in search_query for field in required_fields)
    
    def get_scraper_name(self) -> str:
        """Get the name of this scraper."""
        return self.__class__.__name__
    
    @classmethod
    @abstractmethod
    def get_default_settings(cls) -> Dict[str, Any]:
        """
        Get the default settings for this scraper.
        
        Returns:
            Dictionary containing default scraper-specific settings
        """
        pass