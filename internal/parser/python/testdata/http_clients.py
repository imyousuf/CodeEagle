"""Python code with HTTP client calls for testing detection."""

import requests
import httpx


def fetch_instances():
    """Fetch instances from backend."""
    response = requests.get("/api/v1/instances")
    return response.json()


def create_instance(name):
    """Create an instance via HTTP."""
    response = requests.post("/api/v1/instances", json={"name": name})
    return response.json()


def update_instance(instance_id, data):
    """Update an instance via HTTP."""
    response = requests.put(f"/api/v1/instances/{instance_id}", json=data)
    return response.json()


async def async_fetch_agents(instance_id):
    """Fetch agents using httpx."""
    async with httpx.AsyncClient() as client:
        response = await client.get(f"/api/v1/instances/{instance_id}/agents")
        return response.json()


def delete_instance(instance_id):
    """Delete an instance."""
    response = requests.delete(f"/api/v1/instances/{instance_id}")
    return response.status_code
