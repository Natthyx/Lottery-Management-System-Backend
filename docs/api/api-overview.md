# API Reference

Base URL: `http://localhost:8080` (development)

All responses follow the envelope format:
```json
{
  "success": true|false,
  "data": {...},
  "error": "error message if success=false",
  "meta": {"total": 0, "page": 1, "limit": 20}
}
```

---

## Authentication

### Register
**POST** `/auth/register`

Create a new user account with role `user`.

**Request:**
```json
{
  "email": "user@example.com",
  "password": "SecurePass123",
  "full_name": "Alice Johnson"
}
```

**Response:** `201 Created`
```json
{
  "success": true,
  "data": {
    "id": 42,
    "email": "user@example.com",
    "full_name": "Alice Johnson",
    "role": "user",
    "created_at": "2024-03-15T10:30:00Z",
    "updated_at": "2024-03-15T10:30:00Z"
  }
}
```

**Errors:**
- `400` Invalid input (email format, password too short)
- `409` Email already registered

---

### Login
**POST** `/auth/login`

Authenticate and receive a JWT token.

**Request:**
```json
{
  "email": "user@example.com",
  "password": "SecurePass123"
}
```

**Response:** `200 OK`
```json
{
  "success": true,
  "data": {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "user": {
      "id": 42,
      "email": "user@example.com",
      "full_name": "Alice Johnson",
      "role": "user",
      "created_at": "2024-03-15T10:30:00Z",
      "updated_at": "2024-03-15T10:30:00Z"
    }
  }
}
```

**Errors:**
- `401` Invalid credentials (same error for wrong email or password to prevent user enumeration)

**Token Usage:**
Include in subsequent requests:
```
Authorization: Bearer <token>
```

---

### Get Current User
**GET** `/auth/me`

Retrieve the authenticated user's profile.

**Auth:** Required (Bearer token)

**Response:** `200 OK`
```json
{
  "success": true,
  "data": {
    "id": 42,
    "email": "user@example.com",
    "full_name": "Alice Johnson",
    "role": "user",
    "created_at": "2024-03-15T10:30:00Z",
    "updated_at": "2024-03-15T10:30:00Z"
  }
}
```

---

## Events

### List Events
**GET** `/events`

List all events with optional filtering and pagination.

**Query Parameters:**
- `status` (optional): Filter by status (`open`, `closed`, `drawn`, `cancelled`)
- `page` (optional): Page number (default: 1)
- `limit` (optional): Results per page (default: 20, max: 100)

**Example:** `GET /events?status=open&page=1&limit=20`

**Response:** `200 OK`
```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "title": "Spring Festival Lottery",
      "description": "Win exclusive merchandise",
      "capacity": 100,
      "winner_count": 3,
      "draw_at": "2024-04-01T18:00:00Z",
      "status": "open",
      "created_by": 1,
      "booking_count": 47,
      "created_at": "2024-03-01T10:00:00Z",
      "updated_at": "2024-03-15T14:30:00Z"
    }
  ],
  "meta": {
    "total": 15,
    "page": 1,
    "limit": 20
  }
}
```

---

### Get Event by ID
**GET** `/events/{id}`

Retrieve a single event with live booking count.

**Response:** `200 OK`
```json
{
  "success": true,
  "data": {
    "id": 1,
    "title": "Spring Festival Lottery",
    "description": "Win exclusive merchandise",
    "capacity": 100,
    "winner_count": 3,
    "draw_at": "2024-04-01T18:00:00Z",
    "status": "open",
    "created_by": 1,
    "booking_count": 47,
    "created_at": "2024-03-01T10:00:00Z",
    "updated_at": "2024-03-15T14:30:00Z"
  }
}
```

**Errors:**
- `404` Event not found

---

### Create Event
**POST** `/events`

Create a new lottery event (admin only).

**Auth:** Required (admin role)

**Request:**
```json
{
  "title": "Summer Giveaway",
  "description": "Win premium prizes",
  "capacity": 50,
  "winner_count": 2,
  "draw_at": "2024-07-15T20:00:00Z"
}
```

**Response:** `201 Created`
```json
{
  "success": true,
  "data": {
    "id": 10,
    "title": "Summer Giveaway",
    "description": "Win premium prizes",
    "capacity": 50,
    "winner_count": 2,
    "draw_at": "2024-07-15T20:00:00Z",
    "status": "open",
    "created_by": 1,
    "booking_count": 0,
    "created_at": "2024-03-15T15:00:00Z",
    "updated_at": "2024-03-15T15:00:00Z"
  }
}
```

**Validation:**
- `title`: required, max 200 chars
- `capacity`: minimum 1
- `winner_count`: minimum 1, cannot exceed capacity
- `draw_at`: must be in the future

**Errors:**
- `400` Invalid input
- `401` Not authenticated
- `403` Not admin

---

### Close Event
**PUT** `/events/{id}/close`

Transition event from `open` to `closed` (admin only). No new bookings allowed after this.

**Auth:** Required (admin role)

**Response:** `200 OK`
```json
{
  "success": true,
  "data": {
    "id": 10,
    "status": "closed",
    ...
  }
}
```

**Errors:**
- `404` Event not found
- `409` Invalid status transition (e.g., already drawn or cancelled)

---

### Cancel Event
**PUT** `/events/{id}/cancel`

Cancel an event (admin only). Can transition from `open` or `closed` to `cancelled`.

**Auth:** Required (admin role)

**Response:** `200 OK`

**Errors:**
- `404` Event not found
- `409` Cannot cancel a drawn event

---

## Bookings

### Book Event
**POST** `/events/{eventID}/book`

Register for a lottery event. Uses Redis distributed locking + PostgreSQL row locking for concurrency safety.

**Auth:** Required (user or admin)

**Response:** `201 Created`
```json
{
  "success": true,
  "data": {
    "message": "booking confirmed — you are now eligible for the lottery"
  }
}
```

**Errors:**
- `401` Not authenticated
- `404` Event not found
- `409` Event busy (retry), event not open, event full, already booked

**Concurrency Protection:**
- Redis lock prevents concurrent bookings for the same event
- PostgreSQL `SELECT FOR UPDATE` ensures capacity isn't exceeded
- Both layers combined guarantee no overselling

---

### My Bookings
**GET** `/me/bookings`

List all bookings for the authenticated user.

**Auth:** Required

**Response:** `200 OK`
```json
{
  "success": true,
  "data": [
    {
      "id": 123,
      "event_id": 10,
      "user_id": 42,
      "booked_at": "2024-03-15T12:00:00Z"
    }
  ]
}
```

---

### List Event Bookings
**GET** `/events/{eventID}/bookings`

List all bookings for an event (admin only).

**Auth:** Required (admin role)

**Response:** `200 OK`
```json
{
  "success": true,
  "data": [
    {
      "id": 123,
      "event_id": 10,
      "user_id": 42,
      "booked_at": "2024-03-15T12:00:00Z"
    },
    {
      "id": 124,
      "event_id": 10,
      "user_id": 43,
      "booked_at": "2024-03-15T12:05:00Z"
    }
  ]
}
```

---

## Lottery

### Run Draw
**POST** `/events/{eventID}/draw`

Execute the lottery draw using crypto/rand Fisher-Yates algorithm (admin only).

**Auth:** Required (admin role)

**Requirements:**
- Event must be in `closed` status
- At least one booking exists

**Response:** `200 OK`
```json
{
  "success": true,
  "data": {
    "event_id": 10,
    "event_title": "Summer Giveaway",
    "total_entrants": 47,
    "winners": [
      {
        "id": 501,
        "event_id": 10,
        "winner_user_id": 42,
        "draw_rank": 1,
        "entropy_source": "crypto/rand+fisher-yates",
        "total_entrants": 47,
        "drawn_at": "2024-03-15T15:30:00Z"
      },
      {
        "id": 502,
        "event_id": 10,
        "winner_user_id": 89,
        "draw_rank": 2,
        "entropy_source": "crypto/rand+fisher-yares",
        "total_entrants": 47,
        "drawn_at": "2024-03-15T15:30:00Z"
      }
    ],
    "waitlist": [
      {
        "id": 503,
        "event_id": 10,
        "winner_user_id": 15,
        "draw_rank": 3,
        "entropy_source": "crypto/rand+fisher-yates",
        "total_entrants": 47,
        "drawn_at": "2024-03-15T15:30:00Z"
      }
    ],
    "drawn_at": "2024-03-15T15:30:00Z"
  }
}
```

**Process:**
1. Lock event row with `FOR UPDATE`
2. Validate status is `closed`
3. Fetch all participants
4. Shuffle using crypto/rand
5. Split into winners and waitlist
6. Persist all results to append-only audit table
7. Update event status to `drawn`
8. Commit transaction atomically

**Errors:**
- `404` Event not found
- `409` Event not closed, already drawn, no participants

**Idempotency:**
- Cannot draw twice
- All results recorded in immutable audit log

---

### Get Results
**GET** `/events/{eventID}/results`

Retrieve lottery results (public, no auth required).

**Response:** `200 OK`
```json
{
  "success": true,
  "data": {
    "event_id": 10,
    "event_title": "Summer Giveaway",
    "total_entrants": 47,
    "winners": [...],
    "waitlist": [...],
    "drawn_at": "2024-03-15T15:30:00Z"
  }
}
```

**Errors:**
- `404` No results available (event not drawn yet)

---

## Admin

### Promote User to Admin
**POST** `/admin/users/{id}/promote`

Elevate a user's role to admin (admin only).

**Auth:** Required (admin role)

**Response:** `200 OK`
```json
{
  "success": true,
  "data": {
    "id": 42,
    "email": "user@example.com",
    "full_name": "Alice Johnson",
    "role": "admin",
    "created_at": "2024-03-15T10:30:00Z",
    "updated_at": "2024-03-15T16:00:00Z"
  }
}
```

**Errors:**
- `400` Invalid user ID
- `404` User not found
- `403` Not admin

---

## Operations

### Health Check
**GET** `/health`

Liveness probe. Returns 200 if the process is running. Does not check dependencies.

**Response:** `200 OK`
```json
{
  "status": "ok"
}
```

---

### Readiness Check
**GET** `/ready`

Readiness probe. Verifies PostgreSQL and Redis connectivity.

**Response:** `200 OK`
```json
{
  "status": "ok",
  "checks": {
    "postgres": "ok",
    "redis": "ok"
  }
}
```

**Response (degraded):** `503 Service Unavailable`
```json
{
  "status": "degraded",
  "checks": {
    "postgres": "ok",
    "redis": "down: connection refused"
  }
}
```

---

## Rate Limiting

**Auth endpoints** (`/auth/login`, `/auth/register`):
- 10 requests per minute per IP (configurable)
- 429 Too Many Requests on violation

**All other endpoints:**
- 100 requests per minute per IP (configurable)
- 429 Too Many Requests on violation

---

## Error Responses

All errors follow the envelope:
```json
{
  "success": false,
  "error": "descriptive error message"
}
```

**Status Codes:**
- `400` Bad Request (invalid input)
- `401` Unauthorized (missing/invalid token)
- `403` Forbidden (insufficient permissions)
- `404` Not Found
- `409` Conflict (business logic violation)
- `429` Too Many Requests (rate limit)
- `500` Internal Server Error

---

## Security Notes

- **Passwords:** bcrypt hashed (cost 12)
- **JWT:** HMAC-SHA256, explicit algorithm validation prevents `alg:none` attacks
- **Login timing:** Constant-time comparison prevents user enumeration
- **Body limit:** 1 MB maximum request size
- **CORS:** Configurable allowed origins
- **TLS:** Required in production (handled by load balancer/ingress)
