import enum
from datetime import datetime

from sqlalchemy import (ARRAY, Boolean, Column, DateTime, Enum, Float,
                        ForeignKey, Integer, Interval, PrimaryKeyConstraint,
                        String, Text)
from sqlalchemy.orm import relationship

from shared.db.database import Base

class JobSource(enum.Enum):
    LINKEDIN = "linkedin"
    INDEED = "indeed"

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