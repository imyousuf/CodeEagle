---
title: Architecture Overview
author: Jane Doe
date: 2024-01-15
tags:
  - architecture
  - design
---

# Architecture Overview

This document describes the system architecture.

## Services

The system consists of multiple services communicating via gRPC.

See [main.go](../cmd/main.go) for the entry point.

### Auth Service

Handles authentication and authorization. See [auth handler](../internal/auth/handler.go) for details.

```go
func Authenticate(ctx context.Context, token string) (*User, error) {
    // validate token
}
```

### API Gateway

Routes requests to backend services. Configuration in [config.yaml](../configs/config.yaml).

```python
def create_app():
    app = Flask(__name__)
    return app
```

## Data Model

The data model is defined in [schema.sql](../migrations/schema.sql).

TODO: Add diagram for entity relationships

## Dependencies

- Go 1.24+
- PostgreSQL 15
- Redis 7

For more details, see the [README](../README.md).
