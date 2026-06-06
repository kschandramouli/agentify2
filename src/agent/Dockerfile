# Dockerfile for Python agent service

FROM python:3.11-slim

WORKDIR /app

# Install dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy source code
COPY . .

EXPOSE 8001

ENV PYTHONUNBUFFERED=1

CMD ["python", "main.py"]
