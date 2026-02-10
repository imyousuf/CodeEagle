"""Module demonstrating Python Protocol types."""

from typing import Protocol, runtime_checkable


class Serializable(Protocol):
    """A protocol for objects that can be serialized."""

    def serialize(self) -> str: ...
    def deserialize(self, data: str) -> None: ...


@runtime_checkable
class Comparable(Protocol):
    """A protocol for comparable objects."""

    def compare(self, other) -> int: ...


class JsonSerializer(Serializable):
    """Concrete implementation of Serializable."""

    def serialize(self) -> str:
        return "{}"

    def deserialize(self, data: str) -> None:
        pass


class UserComparator(Comparable):
    """Concrete implementation of Comparable."""

    def compare(self, other) -> int:
        return 0


def process(item: Serializable) -> str:
    """Process a serializable item."""
    return item.serialize()
