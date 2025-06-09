from setuptools import setup, find_packages

setup(
    name="alert",
    version="0.1.0",
    packages=find_packages(),
    install_requires=[
        "temporalio",
        "python-dotenv",
        "pydantic",
        "loguru"
    ],
    python_requires=">=3.12",
)
