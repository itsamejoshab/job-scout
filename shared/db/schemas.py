from datetime import datetime
from typing import Any, Dict, List, Optional

from pydantic import BaseModel, ConfigDict, Field

from shared.db.models import (JobSource)

class JobBase(BaseModel):
    id = Optional[int] = None
    job_source = JobSource = JobSource.LINKEDIN
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

    def to_dict(self):
        return self.model_dump()