# Portfolio Screenshot Plan

This document specifies exactly which screenshots to capture for your Upwork portfolio to showcase the lottery backend system effectively.

---

## Screenshot 1: System Architecture Diagram

**What to capture:**
- Open `docs/architecture/system-architecture.mmd` in a Mermaid renderer (mermaid.live, VS Code Mermaid plugin, or GitHub preview)
- Render the full architecture diagram

**Why it matters:**
Clients need to see the high-level architecture at a glance. This shows:
- Multi-layer design (API → Services → Data)
- Technology choices (Go, PostgreSQL, Redis)
- Deployment readiness (Docker, Kubernetes)

**Suggested caption:**
> "Production-grade lottery system architecture built with Go, PostgreSQL, and Redis. Features defense-in-depth concurrency control and cryptographic winner selection."

**Presentation tip:**
Export as PNG with transparent background if possible, or capture with clean white background.

---

## Screenshot 2: Concurrency Protection Diagram

**What to capture:**
- Render `docs/diagrams/concurrency-protection.mmd`
- Show the dual-layer locking mechanism

**Why it matters:**
This is your standout technical feature. It demonstrates:
- Senior-level understanding of distributed systems
- Preventing race conditions at scale
- Defense-in-depth engineering

**Suggested caption:**
> "Defense-in-depth concurrency control: Redis distributed locking + PostgreSQL row-level locking prevents overselling even under infrastructure failures."

**Talking points for client discussion:**
- "This dual-layer approach ensures no overselling even if Redis fails"
- "The system remains correct under all failure scenarios"
- "Owner-safe lock release prevents timing vulnerabilities"

---

## Screenshot 3: Lottery Workflow Sequence Diagram

**What to capture:**
- Render `docs/diagrams/lottery-workflow.mmd`
- Full sequence from event creation → booking → draw → results

**Why it matters:**
Shows end-to-end business flow with technical detail. Clients understand:
- The complete user journey
- Database transactions and validation steps
- API integration points

**Suggested caption:**
> "Complete lottery workflow: event management, concurrent booking with capacity validation, cryptographically fair draw, and immutable results."

**Presentation tip:**
If the diagram is too long, consider splitting into two screenshots:
- Part 1: Event creation + Booking flow
- Part 2: Lottery draw + Results

---

## Screenshot 4: Winner Selection Algorithm Diagram

**What to capture:**
- Render `docs/diagrams/winner-selection.mmd`
- Highlight the crypto/rand + Fisher-Yates flow

**Why it matters:**
This demonstrates specialized cryptographic knowledge:
- Why `crypto/rand` over `math/rand`
- Fisher-Yates algorithm correctness
- Audit trail integration

**Suggested caption:**
> "Cryptographically secure winner selection using crypto/rand (kernel entropy) and Fisher-Yates algorithm. Same randomness source as TLS key generation."

**Client value:**
"This approach is defensible in audits and compliance reviews. Predictable randomness would be a compliance failure."

---

## Screenshot 5: Code Sample — Booking Service with Locks

**What to capture:**
Open `internal/booking/service.go` and capture:
- Lines 50-75 (BookWithLock function with Redis SETNX)
- Lines 87-114 (createBooking with SELECT FOR UPDATE)

**Format:**
- Use VS Code with syntax highlighting
- Enable line numbers
- Use a professional theme (Dark+ or Light+)

**Why it matters:**
Shows actual production code quality:
- Clean, readable Go code
- Comprehensive error handling
- Inline documentation

**Suggested caption:**
> "Production Go code implementing Redis distributed locking with owner-safe release and PostgreSQL row-level locking for capacity validation."

**Presentation tip:**
Consider using Carbon (carbon.now.sh) to create a polished code screenshot with syntax highlighting.

---

## Screenshot 6: Code Sample — Lottery Algorithm

**What to capture:**
Open `internal/lottery/algorithm.go` and capture:
- Lines 1-45 (FairShuffle function with detailed comments)
- Show the `crypto/rand.Int` call
- Include the comment block explaining why crypto/rand vs math/rand

**Why it matters:**
Demonstrates:
- Algorithm expertise
- Security awareness
- Professional documentation standards

**Suggested caption:**
> "Fisher-Yates shuffle with crypto/rand ensures uniform, unpredictable winner selection. Extensive inline documentation explains cryptographic rationale."

**Client value:**
"The comments show the depth of thought behind every technical decision. This is production code that will be maintainable years from now."

---

## Screenshot 7: Test Coverage — Fairness Validation

**What to capture:**
Open `internal/lottery/algorithm_test.go` and show:
- Lines 50-85 (TestFairShuffle_UniformDistribution)
- The 10,000-trial statistical validation

**Why it matters:**
Shows commitment to correctness:
- Statistical testing of randomness
- Not just "does it run" but "is it fair"
- Production-grade quality assurance

**Suggested caption:**
> "Statistical fairness validation: 10,000-trial test ensures every participant has equal probability of winning. 5% tolerance proves uniform distribution."

---

## Screenshot 8: Database Schema with Audit Protection

**What to capture:**
Open `migrations/001_init.sql` and show:
- Lines 50-68 (lottery_draws table definition)
- Lines 70-72 (REVOKE UPDATE, DELETE statement)

**Why it matters:**
Unique feature demonstrating:
- Database-level security enforcement
- Tamper-evident audit trails
- Compliance-ready design

**Suggested caption:**
> "Immutable audit trail: Database-level permissions revoke UPDATE/DELETE on draw results. Even compromised application code cannot tamper with past lotteries."

---

## Screenshot 9: Docker Multi-Stage Build

**What to capture:**
Open `Dockerfile` and show the complete file:
- Builder stage (golang:1.22-alpine)
- Runtime stage (distroless)
- Security hardening (nonroot user)

**Why it matters:**
Shows DevOps/infrastructure expertise:
- Container optimization
- Security best practices
- Production deployment knowledge

**Suggested caption:**
> "Secure Docker deployment: Multi-stage build produces 20MB distroless container with no shell or package manager. Runs as non-root user."

---

## Screenshot 10: API Documentation Sample

**What to capture:**
- Open `docs/api/api-overview.md` in a Markdown preview
- Show 2-3 endpoint descriptions with request/response examples
- Choose visually appealing endpoints like POST /events/{id}/draw

**Why it matters:**
Demonstrates:
- Professional documentation standards
- API design consistency
- Client-ready deliverables

**Suggested caption:**
> "Comprehensive API documentation with request/response examples, error codes, and security notes. Production-ready for frontend integration."

---

## Screenshot 11: Smoke Test Script Output

**What to capture:**
Run the smoke test and capture terminal output:
```bash
make run          # In one terminal
make smoke        # In another terminal, capture this output
```

Show the successful test run with green checkmarks.

**Why it matters:**
Proves the system actually works:
- End-to-end testing
- Real API interactions
- Quality assurance

**Suggested caption:**
> "Automated smoke test validates complete workflow: user registration, authentication, event booking with concurrency control, lottery draw, and result retrieval."

---

## Screenshot 12: CI/CD Pipeline

**What to capture:**
- Open `.github/workflows/ci.yml` 
- Or show a GitHub Actions run (if you've pushed to GitHub)
- Highlight: tests with PostgreSQL + Redis services, Docker build

**Why it matters:**
Shows modern development practices:
- Automated testing
- Continuous integration
- Production deployment pipeline

**Suggested caption:**
> "Automated CI pipeline with PostgreSQL and Redis test databases. Every commit validated with race detector, linting, and Docker build."

---

## Bonus Screenshot: Local Development Setup

**What to capture:**
Terminal showing:
```bash
make up
docker ps  # Show running postgres + redis containers
make run   # Show server startup logs
```

**Why it matters:**
Shows ease of local development:
- Docker Compose for dependencies
- Simple commands
- Developer-friendly setup

**Suggested caption:**
> "Developer experience: Docker Compose provides PostgreSQL and Redis. Single 'make run' command starts the server with auto-migrations and structured logging."

---

## Portfolio Presentation Tips

### For Upwork Project Gallery:

**Lead screenshot:** System Architecture (#1)
- This hooks the client immediately
- Shows scale and sophistication

**Follow-up screenshots in order:**
1. Architecture (#1)
2. Concurrency Protection (#2)
3. Lottery Workflow (#3)
4. Code sample - Booking (#5)
5. Winner Selection Algorithm (#4)
6. Database Schema (#8)
7. Smoke Test Output (#11)
8. API Documentation (#10)

### For Project Description:

**Opening line:**
"Production-grade lottery backend built with Go, featuring defense-in-depth concurrency control, cryptographically secure winner selection, and immutable audit trails."

**Key screenshots to emphasize:**
- Concurrency diagram → Shows senior-level distributed systems knowledge
- Winner selection → Demonstrates cryptographic expertise
- Audit protection → Shows security/compliance awareness

### Screenshot Quality Guidelines:

1. **Resolution:** Export diagrams at 2x or 3x for Retina displays
2. **Consistency:** Use the same Mermaid theme for all diagrams
3. **Code screenshots:** Use Carbon or similar with consistent theme
4. **Annotations:** Add arrows/highlights if needed (but diagrams are already clear)
5. **File naming:** `lottery-backend-architecture.png` (descriptive, professional)

---

## What NOT to Screenshot

❌ **Environment files with secrets** (.env with JWT_SECRET)
❌ **Database credentials** (even if localhost)
❌ **Raw logs with IP addresses** (GDPR concern)
❌ **Error messages** (unless demonstrating error handling)
❌ **Messy terminal output** (format it first)

---

## Recommended Screenshot Tools

**Diagrams:**
- Mermaid Live Editor (mermaid.live)
- VS Code Mermaid Preview
- GitHub's built-in Mermaid rendering

**Code:**
- Carbon (carbon.now.sh) — beautiful code screenshots
- VS Code with built-in screenshot (Cmd+Shift+P → "Capture Screenshot")
- Ray.so — another code beautifier

**Terminal:**
- iTerm2 or Hyper with professional theme
- CleanShot X (Mac) for annotations
- `asciinema` for animated terminal recordings (advanced)

---

## Final Checklist Before Uploading

- [ ] All screenshots use consistent color scheme
- [ ] No sensitive data visible (passwords, API keys, real emails)
- [ ] Diagrams are high resolution (2x minimum)
- [ ] Code screenshots have syntax highlighting
- [ ] File names are descriptive (not "Screen Shot 2024-03-15.png")
- [ ] Each screenshot has a clear caption
- [ ] Screenshots show actual working code, not mockups
- [ ] Terminal output shows successful operations

---

## Using These Screenshots in Upwork Proposals

**For backend-heavy projects:**
Lead with #2 (Concurrency Protection) — "I've built systems that handle high-concurrency booking scenarios without overselling..."

**For security-focused projects:**
Lead with #4 (Winner Selection) + #8 (Audit Protection) — "I implement cryptographic solutions and compliance-ready audit trails..."

**For full-stack/API projects:**
Lead with #1 (Architecture) + #10 (API Docs) — "I design and document RESTful APIs with production-grade deployment..."

**For DevOps/infrastructure projects:**
Lead with #9 (Docker) + #12 (CI/CD) — "I containerize applications with security best practices and automated testing..."

---

## Estimated Time to Capture All Screenshots

- **Diagrams (1-4, 8):** 30 minutes (render + export)
- **Code samples (5-7):** 20 minutes (open files, format, capture)
- **Running system (11-13):** 15 minutes (start services, run tests)
- **Editing/annotations:** 15 minutes
- **Total:** ~90 minutes

**Recommendation:** Do this in one sitting to maintain visual consistency.
