# ADR-0002: SvelteKit for the Web SPA

Date: 2026-10-04
Status: Accepted (owner decision)

## Context

The panel needs a snappy SPA with two areas (admin + user), heavy realtime surfaces (xterm.js console, live stats over WebSocket), and many data-driven tables/forms. The candidates were SvelteKit and React + Vite + TanStack Query.

## Decision

SvelteKit + Tailwind + xterm.js.

## Consequences

- Smallest bundles and least boilerplate of the two; the realtime console and stats surfaces stay snappy.
- xterm.js wraps in a small Svelte component; TanStack Query has an official Svelte adapter for server-state management.
- Smaller ecosystem and hiring pool than React — accepted tradeoff, owner's call.
