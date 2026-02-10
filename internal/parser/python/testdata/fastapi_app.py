"""A FastAPI application for testing endpoint extraction."""

from fastapi import APIRouter, FastAPI
from pydantic import BaseModel

app = FastAPI()
router = APIRouter()


class InstanceCreate(BaseModel):
    name: str
    description: str


@router.get("/instances")
async def list_instances():
    """List all instances."""
    return []


@router.get("/instances/{instance_id}")
async def get_instance(instance_id: str):
    """Get a specific instance."""
    return {"id": instance_id}


@router.post("/instances")
async def create_instance(data: InstanceCreate):
    """Create a new instance."""
    return {"id": "new", "name": data.name}


@router.put("/instances/{instance_id}")
async def update_instance(instance_id: str, data: InstanceCreate):
    """Update an instance."""
    return {"id": instance_id}


@router.delete("/instances/{instance_id}")
async def delete_instance(instance_id: str):
    """Delete an instance."""
    return {"deleted": True}


@router.patch("/instances/{instance_id}/status")
async def patch_instance_status(instance_id: str):
    """Patch instance status."""
    return {"patched": True}


app.include_router(router, prefix="/api/v1")
