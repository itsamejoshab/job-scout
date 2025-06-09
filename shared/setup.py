from setuptools import find_packages, setup

setup(
    name="shared",
    version="0.1.0",
    packages=find_packages(include=["shared", "shared.*"]),
    package_data={
        "shared": ["py.typed"],
    },
    install_requires=[
        "sqlalchemy>=2.0.0",
        "pydantic>=2.0.0",
        "temporalio>=1.4.0",
        "asyncpg>=0.29.0",
        "python-dotenv>=1.0.0",
        "tenacity>=9.1.2",
    ],
    python_requires=">=3.12",
)
