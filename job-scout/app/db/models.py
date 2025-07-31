import enum
from datetime import datetime
from sqlalchemy import (ARRAY, Boolean, Column, DateTime, Enum, Float,
                        ForeignKey, Integer, Interval, PrimaryKeyConstraint,
                        String, Text, JSON)
from sqlalchemy.orm import relationship
from app.db.database import Base
from sqlalchemy.ext.declarative import declarative_base

Base = declarative_base()

class JobSource(enum.Enum):
    LINKEDIN = "LINKEDIN"
    INDEED = "INDEED"

class Jobs(Base):
    __tablename__ = "jobs"

    id = Column(Integer, primary_key=True, index=True)
    job_source = Column(Enum(JobSource), default=JobSource.LINKEDIN)
    title = Column(String(100), nullable=False)
    company = Column(String(100), nullable=False)
    description = Column(Text, nullable=True)
    location = Column(String(100), nullable=False)
    date = Column(DateTime, default=datetime.now)
    job_url = Column(String(250), nullable=False)
    created_at = Column(DateTime, default=datetime.now)
    updated_at = Column(DateTime, default=datetime.now, onupdate=datetime.now)
    new = Column(Boolean, default=True)
    duplicate = Column(Boolean, default=False)
    relevant = Column(Boolean, default=False)
    promising = Column(Boolean, default=False)
    notified = Column(Boolean, default=False)
    
    #representation
    def __repr__(self):
        return f"<Job '{self.title}' ({self.job_url})>"

    def to_dict(self):
        """Convert SQLAlchemy model instance to a dictionary."""
        result = {}

        # Loop through the column names, not the Column objects
        for column in self.__table__.columns:
            column_name = column.name  # Get the column name (string)
            value = getattr(self, column_name)  # Get the actual value of the column

            # Handle Enum values by converting them to a string
            if isinstance(value, enum.Enum):
                value = value.name  # Convert Enum to its string name

            # Handle datetime objects by converting to ISO 8601 format string
            elif isinstance(value, datetime):
                value = value.isoformat()  # Convert datetime to string in ISO format

            if isinstance(value, list):  # Handle relationships (e.g., 'job_ids')
                # Recursively convert related objects to dictionaries if they have 'to_dict' method
                result[column_name] = [
                    item.to_dict() if hasattr(item, "to_dict") else item
                    for item in value
                ]
            else:
                result[column_name] = value

        return result
    
class SearchSettings(Base):
    __tablename__ = "search_settings"

    id = Column(Integer, primary_key=True, index=True)  
    desc_include_words = Column(JSON, nullable=False)  
    desc_exclude_words = Column(JSON, nullable=False)  
    title_include = Column(JSON, nullable=False)  
    title_exclude = Column(JSON, nullable=False)  
    company_exclude = Column(JSON, nullable=False)  
    non_remote_phrases = Column(JSON, nullable=False)  

    created_at = Column(DateTime, default=datetime.now)  
    updated_at = Column(DateTime, default=datetime.now, onupdate=datetime.now)
    
    #representation
    def __repr__(self):
        return f"<SearchSettings>"

    def to_dict(self):
        """Convert SQLAlchemy model instance to a dictionary."""
        result = {}

        # Loop through the column names, not the Column objects
        for column in self.__table__.columns:
            column_name = column.name  # Get the column name (string)
            value = getattr(self, column_name)  # Get the actual value of the column

            # Handle Enum values by converting them to a string
            if isinstance(value, enum.Enum):
                value = value.value  # Convert Enum to its string name

            # Handle datetime objects by converting to ISO 8601 format string
            elif isinstance(value, datetime):
                value = value.isoformat()  # Convert datetime to string in ISO format

            if isinstance(value, list):  # Handle relationships (e.g., 'job_ids')
                # Recursively convert related objects to dictionaries if they have 'to_dict' method
                result[column_name] = [
                    item.to_dict() if hasattr(item, "to_dict") else item
                    for item in value
                ]
            else:
                result[column_name] = value

        return result

class ScraperSettings(Base):
    __tablename__ = "scraper_settings"

    id = Column(Integer, primary_key=True, index=True)  
    job_source = Column(Enum(JobSource), nullable=False)  # Which scraper these settings are for
    search_queries = Column(JSON, nullable=False)  # List of dicts with keys: keywords, location, f_WT
    hardcoded_urls = Column(JSON, nullable=True)   # List of dicts with keys: url, description, is_remote
    timespan_code = Column(String(100), nullable=False)  
    pages_to_scrape = Column(Integer, nullable=False)  
    rounds = Column(Integer, nullable=False)  

    created_at = Column(DateTime, default=datetime.now)  
    updated_at = Column(DateTime, default=datetime.now, onupdate=datetime.now)
    
    #representation
    def __repr__(self):
        return f"<ScraperSettings for {self.job_source.value}>"

    def to_dict(self):
        """Convert SQLAlchemy model instance to a dictionary."""
        result = {}

        # Loop through the column names, not the Column objects
        for column in self.__table__.columns:
            column_name = column.name  # Get the column name (string)
            value = getattr(self, column_name)  # Get the actual value of the column

            # Handle Enum values by converting them to a string
            if isinstance(value, enum.Enum):
                value = value.name  # Convert Enum to its string name

            # Handle datetime objects by converting to ISO 8601 format string
            elif isinstance(value, datetime):
                value = value.isoformat()  # Convert datetime to string in ISO format

            if isinstance(value, list):  # Handle relationships (e.g., 'job_ids')
                # Recursively convert related objects to dictionaries if they have 'to_dict' method
                result[column_name] = [
                    item.to_dict() if hasattr(item, "to_dict") else item
                    for item in value
                ]
            else:
                result[column_name] = value

        return result