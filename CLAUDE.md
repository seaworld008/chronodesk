# Claude Code instructions

The repository has one shared instruction set for human and AI contributors:

1. Read [AGENTS.md](AGENTS.md) for commands, invariants, and contribution rules.
2. Read [CONTEXT.md](CONTEXT.md) before naming or changing a domain concept.
3. Read [ARCHITECTURE.md](ARCHITECTURE.md) and applicable
   [ADRs](docs/adr/README.md) before changing Module dependencies.
4. Treat `server/internal/openapi/openapi.yaml` and its contract tests as the
   machine Interface source of truth.

Do not maintain a second copy of framework versions, commands, or architecture
rules in this file.
