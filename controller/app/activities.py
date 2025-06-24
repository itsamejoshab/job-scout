from temporalio import activity
from sqlalchemy import text, select
#from shared.db.database import get_session
import logging
import json
import time
import backoff
from sqlalchemy.exc import SQLAlchemyError, OperationalError

logger = logging.getLogger(__name__)

@activity.defn
async def test():
    config_path = 'app/search_config.json'

    with open(config_path, "r") as file:
        search_config = json.load(file)

    logger.info(search_config['search_queries'])

    return True

@activity.defn
async def finder():
    time.sleep(10)
    return True

@activity.defn
async def duplicate_remover():
    time.sleep(10)
    return True

@activity.defn
async def basic_filter():
    time.sleep(10)
    return True

@activity.defn
async def detailer():
    time.sleep(10)
    return True

@activity.defn
async def advanced_filter():
    time.sleep(10)
    return True

@activity.defn
async def smart_filter():
    time.sleep(10)
    return True

@activity.defn
async def notifier():
    time.sleep(10)
    return True