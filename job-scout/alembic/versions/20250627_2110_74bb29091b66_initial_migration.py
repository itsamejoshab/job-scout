"""Initial migration

Revision ID: ididthismanually
Revises: None
Create Date: 2025-07-31 18:38:49.996280+00:00

"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

# Revision identifiers, used by Alembic.
revision = 'ididthismanually'
down_revision = None
branch_labels = None
depends_on = None


def upgrade() -> None:
    # Create the jobs table
    op.create_table(
        'jobs',
        sa.Column('id', sa.Integer(), nullable=False),
        sa.Column('job_source', sa.Enum('LINKEDIN', 'INDEED', name='jobsource'), nullable=True, default='LINKEDIN'),
        sa.Column('title', sa.String(length=100), nullable=False),
        sa.Column('company', sa.String(length=100), nullable=False),
        sa.Column('description', sa.Text(), nullable=True),
        sa.Column('location', sa.String(length=100), nullable=False),
        sa.Column('date', sa.DateTime(), nullable=True, default=sa.func.now()),
        sa.Column('job_url', sa.String(length=250), nullable=False),
        sa.Column('created_at', sa.DateTime(), nullable=True, default=sa.func.now()),
        sa.Column('updated_at', sa.DateTime(), nullable=True, default=sa.func.now(), onupdate=sa.func.now()),
        sa.Column('new', sa.Boolean(), nullable=False, default=True),
        sa.Column('duplicate', sa.Boolean(), nullable=False, default=False),
        sa.Column('relevant', sa.Boolean(), nullable=False, default=False),
        sa.Column('promising', sa.Boolean(), nullable=False, default=False),
        sa.Column('notified', sa.Boolean(), nullable=False, default=False),
        sa.PrimaryKeyConstraint('id')
    )
    op.create_index(op.f('ix_jobs_id'), 'jobs', ['id'], unique=False)

    # Create the search_settings table
    op.create_table(
        'search_settings',
        sa.Column('id', sa.Integer(), nullable=False),
        sa.Column('desc_include_words', sa.JSON(), nullable=False),
        sa.Column('desc_exclude_words', sa.JSON(), nullable=False),
        sa.Column('title_include', sa.JSON(), nullable=False),
        sa.Column('title_exclude', sa.JSON(), nullable=False),
        sa.Column('company_exclude', sa.JSON(), nullable=False),
        sa.Column('non_remote_phrases', sa.JSON(), nullable=False),
        sa.Column('created_at', sa.DateTime(), nullable=True, default=sa.func.now()),
        sa.Column('updated_at', sa.DateTime(), nullable=True, default=sa.func.now(), onupdate=sa.func.now()),
        sa.PrimaryKeyConstraint('id')
    )
    op.create_index(op.f('ix_search_settings_id'), 'search_settings', ['id'], unique=False)

    # Create the scraper_settings table
    op.create_table(
        'scraper_settings',
        sa.Column('id', sa.Integer(), nullable=False),
        sa.Column('job_source', sa.Enum('LINKEDIN', 'INDEED', name='jobsource'), nullable=True, default='LINKEDIN'),
        sa.Column('search_queries', sa.JSON(), nullable=False),
        sa.Column('hardcoded_urls', sa.JSON(), nullable=True),
        sa.Column('timespan_code', sa.String(length=100), nullable=False),
        sa.Column('pages_to_scrape', sa.Integer(), nullable=False),
        sa.Column('rounds', sa.Integer(), nullable=False),
        sa.Column('created_at', sa.DateTime(), nullable=True, default=sa.func.now()),
        sa.Column('updated_at', sa.DateTime(), nullable=True, default=sa.func.now(), onupdate=sa.func.now()),
        sa.PrimaryKeyConstraint('id')
    )
    op.create_index(op.f('ix_scraper_settings_id'), 'scraper_settings', ['id'], unique=False)


def downgrade() -> None:
    # Drop the scraper_settings table
    op.drop_index(op.f('ix_scraper_settings_id'), table_name='scraper_settings')
    op.drop_table('scraper_settings')

    # Drop the search_settings table
    op.drop_index(op.f('ix_search_settings_id'), table_name='search_settings')
    op.drop_table('search_settings')

    # Drop the jobs table
    op.drop_index(op.f('ix_jobs_id'), table_name='jobs')
    op.drop_table('jobs')

    # Drop the ENUM type for jobsource
    job_source_enum = sa.Enum('LINKEDIN', 'INDEED', name='job_source')
    job_source_enum.drop(op.get_bind())
