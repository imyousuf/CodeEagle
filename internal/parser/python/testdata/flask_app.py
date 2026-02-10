"""A Flask application for testing endpoint extraction."""

from flask import Flask, Blueprint

app = Flask(__name__)
bp = Blueprint("users", __name__)


@app.route("/health")
def health_check():
    """Health check endpoint."""
    return {"status": "ok"}


@app.route("/login", methods=["POST"])
def login():
    """Login endpoint."""
    return {"token": "abc"}


@bp.route("/users")
def list_users():
    """List users."""
    return []


@bp.route("/users/<user_id>", methods=["GET", "POST"])
def get_or_create_user(user_id):
    """Get or create user."""
    return {"id": user_id}
