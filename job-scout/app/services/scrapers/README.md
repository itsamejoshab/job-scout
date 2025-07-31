# Job Scraper System

This directory contains the modular job scraping system for the Job-Scout application.

## Architecture

The scraper system is designed to be easily extensible for different job sources. It follows a plugin-based architecture:

```
scrapers/
├── scraper.py              # Main scraper service
├── providers/              # Individual scraper implementations
│   ├── base.py            # Base scraper interface
│   ├── linkedin.py        # LinkedIn scraper (implemented)
│   └── indeed.py          # Indeed scraper (placeholder)
└── README.md              # This file
```

## Components

### 1. ScraperService (`scraper.py`)
The main service that orchestrates scraping operations:
- Manages different scraper providers
- Handles database operations
- Provides a unified interface for scraping jobs

### 2. BaseScraper (`providers/base.py`)
Abstract base class that all scrapers must inherit from:
- Defines the interface for job scrapers
- Provides common validation and utility methods
- Ensures consistent behavior across all scrapers

### 3. JobData (`providers/base.py`)
Standardized data structure for job information:
- Consistent format across all scrapers
- Includes title, company, location, URL, description, etc.

## Current Implementations

### LinkedIn Scraper (`providers/linkedin.py`)
- **Status**: ✅ Fully implemented
- **Method**: Uses LinkedIn's API endpoint approach
- **Features**: 
  - Scrapes job listings from LinkedIn
  - Extracts job descriptions
  - Handles pagination
  - Uses proven parsing logic from the original codebase

### Indeed Scraper (`providers/indeed.py`)
- **Status**: 🔄 Placeholder implementation
- **Method**: Ready for implementation
- **Features**: 
  - Structure is in place
  - Needs actual scraping logic to be implemented

## How to Add a New Scraper

To add a new job source (e.g., Glassdoor, Monster, etc.):

1. **Create a new scraper class** in `providers/`:
   ```python
   from .base import BaseScraper, JobData
   from app.db.models import JobSource
   
   class GlassdoorScraper(BaseScraper):
       def get_job_source(self) -> JobSource:
           return JobSource.GLASDOOR  # Add to enum first
       
       def build_search_url(self, search_query: Dict[str, str]) -> str:
           # Implement URL building logic
           pass
       
       async def scrape_jobs(self, search_query: Dict[str, str]) -> List[JobData]:
           # Implement scraping logic
           pass
   ```

2. **Add the job source to the enum** in `app/db/models.py`:
   ```python
   class JobSource(enum.Enum):
       LINKEDIN = "linkedin"
       INDEED = "indeed"
       GLASDOOR = "glassdoor"  # Add this
   ```

3. **Register the scraper** in `scraper.py`:
   ```python
   from .providers.glassdoor import GlassdoorScraper
   
   class ScraperService:
       def __init__(self):
           self.scrapers = {
               JobSource.LINKEDIN: LinkedInScraper,
               JobSource.INDEED: IndeedScraper,
               JobSource.GLASDOOR: GlassdoorScraper,  # Add this
           }
   ```

## Usage

### Via API Endpoints

1. **Run scraping workflow**:
   ```bash
   curl -X POST "http://localhost:8000/api/v0/run?job_source=linkedin"
   ```

2. **Direct scraping**:
   ```bash
   curl -X POST "http://localhost:8000/api/v0/scrape?job_source=linkedin"
   ```

### Via Python Code

```python
from app.services.scrapers.scraper import ScraperService
from app.db.models import JobSource

# Initialize service
scraper_service = ScraperService()

# Run full scrape (scrape + save to database)
result = await scraper_service.run_full_scrape(JobSource.LINKEDIN)

# Or just scrape without saving
jobs = await scraper_service.scrape_jobs(JobSource.LINKEDIN)
```

## Configuration

Search settings are stored in the database and include:
- Search queries (keywords, locations, filters)
- Inclusion/exclusion words for titles and descriptions
- Company exclusions
- Remote work phrases
- Scraping parameters (pages, timespan_code, rounds)

To load initial settings:
```python
from app.db.load import load_search_settings
await load_search_settings()
```

## Testing

Run the test script to verify functionality:
```bash
cd job-scout/app
python test_scraper.py
```

## Temporal Integration

The scraper is integrated with Temporal for workflow management:
- Workflows are tracked and can be monitored
- Supports retry policies and error handling
- Provides workflow status via API endpoints

## Best Practices

1. **Rate Limiting**: Always add delays between requests to be respectful to job sites
2. **Error Handling**: Implement proper error handling for network issues
3. **User Agents**: Use realistic user agents to avoid being blocked
4. **Validation**: Validate scraped data before saving to database
5. **Logging**: Use appropriate logging levels for debugging and monitoring

## Future Enhancements

- [ ] Implement Indeed scraper
- [ ] Add job filtering logic (currently in old code)
- [ ] Add job description fetching for all scrapers
- [ ] Implement retry mechanisms for failed requests
- [ ] Add metrics and monitoring
- [ ] Support for more job sources (Glassdoor, Monster, etc.) 