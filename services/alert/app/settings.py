from pydantic import Field
from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    WEBHOOK_ID: str = Field(..., env="WEBHOOK_ID")
    WEBHOOK_URL: str = Field(..., env="WEBHOOK_URL")
    MAX_FILE_MINUTES: int = 1  # Allow up to 1 minute
    HEARTBEAT_SEC: int = 20

    class Config:
        env_file = ".env"


settings = Settings()
