"""Sample Python module for testing the Python parser."""

import os
import sys
from pathlib import Path
from typing import List, Optional

MAX_RETRIES = 3
DEFAULT_TIMEOUT = 30
_internal_counter = 0


class Animal:
    """Base class for all animals.

    Provides common attributes and behavior.
    """

    def __init__(self, name: str, age: int) -> None:
        """Initialize an Animal instance."""
        self.name = name
        self.age = age

    def speak(self) -> str:
        """Return the sound this animal makes."""
        return ""

    @property
    def info(self) -> str:
        """Return formatted info string."""
        return f"{self.name} (age {self.age})"

    @staticmethod
    def kingdom() -> str:
        """Return the biological kingdom."""
        return "Animalia"

    def __repr__(self) -> str:
        return f"Animal(name={self.name!r}, age={self.age})"


class Dog(Animal):
    """A dog is a domestic animal."""

    def __init__(self, name: str, age: int, breed: str) -> None:
        super().__init__(name, age)
        self.breed = breed

    def speak(self) -> str:
        """Dogs bark."""
        return "Woof!"

    def fetch(self, item: str) -> str:
        """Fetch an item."""
        return f"{self.name} fetches {item}"


def create_animal(name: str, age: int) -> Animal:
    """Factory function to create an Animal."""
    global _internal_counter
    _internal_counter += 1
    return Animal(name, age)


def _validate_name(name: str) -> bool:
    """Internal helper to validate a name."""
    return len(name) > 0 and name.isalpha()


def process_animals(animals: List[Animal], filter_fn: Optional[callable] = None) -> List[str]:
    """Process a list of animals and return their info strings."""
    if filter_fn:
        animals = [a for a in animals if filter_fn(a)]
    return [a.info for a in animals]
