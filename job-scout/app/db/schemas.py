from datetime import datetime
from typing import Any, Dict, List, Optional
from enum import Enum as PyEnum
from pydantic import BaseModel, ConfigDict, Field

from db.models import (JobSource)

class SearchQueryKeys(PyEnum):
    KEYWORDS = "keywords"
    LOCATION = "location"
    F_WT = "f_WT"

class JobBase(BaseModel):
    id = Optional[int] = None
    job_source = JobSource = Field(default=JobSource.LINKEDIN)
    title = str
    company = str
    description = Optional[str]
    location = str
    date = datetime
    job_url = str
    created_at = datetime
    updated_at = datetime
    new = bool
    duplicate = bool
    relevant = bool
    promising = bool
    notified = bool

    class Config:
        orm_mode = True

    def to_dict(self):
        return self.model_dump()
    
class SettingBase(BaseModel):
    id: Optional[int] = None  # Optional ID
    desc_include_words: List[str]  # List of strings
    desc_exclude_words: List[str]  # List of strings
    title_include: List[str]  # List of strings
    title_exclude: List[str]  # List of strings
    company_exclude: List[str]  # List of strings
    non_remote_phrases: List[str]  # List of strings
    created_at: datetime  # Timestamp for creation
    updated_at: datetime  # Timestamp for updates

    class Config:
        orm_mode = True  # Enables compatibility with SQLAlchemy models

    def to_dict(self):
        return self.model_dump()

class ScraperSettings(BaseModel):
    id: Optional[int] = None
    job_source = JobSource = Field(default=JobSource.LINKEDIN)
    search_queries: List[Dict[str, Any]]
    hardcoded_urls: Optional[List[Dict[str, Any]]]
    timespan_code: str
    pages_to_scrape: int
    rounds: int
    created_at: datetime
    updated_at: datetime

    class Config:
        orm_mode = True

    def to_dict(self):
        return self.model_dump()