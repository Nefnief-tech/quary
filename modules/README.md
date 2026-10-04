# Modules

First-party modules (compiled in; Phase 4): marketplace providers (Modrinth, Hangar, CurseForge, Spiget, custom registry), backup targets (local, S3), auth providers (OIDC). Third-party extensions run out of process — see `docs/ARCHITECTURE.md` for the module API sketch and the ADR (due before Phase 4) on WASM vs out-of-process gRPC.
