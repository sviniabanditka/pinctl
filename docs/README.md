# pinctl Documentation

Design documentation for pinctl — a pipeline-based I/O controller for
Arduino-class boards, orchestrated from a Raspberry Pi.

> Nothing is implemented yet. These docs are the full design spec written
> before the first line of code. `00-design-brief.md` is the source of truth:
> if any doc contradicts it, the brief wins.

## Reading order

| # | Doc | What it covers |
|---|-----|----------------|
| 00 | [Design Brief](00-design-brief.md) | Fixed core decisions — read first |
| 01 | [Overview](01-overview.md) | Problem, vision, concepts, alternatives |
| 02 | [Requirements](02-requirements.md) | Functional & non-functional requirements, MVP scope |
| 03 | [Architecture](03-architecture.md) | System components, data flows, concurrency, ADRs |
| 04 | [Firmware Agent](04-firmware-agent.md) | Arduino side: StandardFirmata, pin maps, boards |
| 05 | [Serial Protocol](05-serial-protocol.md) | Firmata usage, session lifecycle, board identity |
| 06 | [Runtime](06-runtime.md) | Go process: config, packages, event bus, lifecycle |
| 07 | [Pipeline Engine](07-pipeline-engine.md) | Tick-based DAG execution, validation, hot-reload |
| 08 | [Node Catalog](08-node-catalog.md) | Every MVP node: inputs, outputs, config, behavior |
| 09 | [Data Model](09-data-model.md) | JSON schemas: boards, devices, pipelines; on-disk layout |
| 10 | [API](10-api.md) | REST + WebSocket spec |
| 11 | [Web UI](11-web-ui.md) | Vue 3 editor: screens, Vue Flow, live debugging |
| 12 | [Roadmap](12-roadmap.md) | Milestones v0.1 → MVP → v1.x backlog |
| 13 | [Testing](13-testing.md) | Test strategy, engine simulation, CI |
| 14 | [Deployment](14-deployment.md) | Install, systemd, udev, ops, troubleshooting |
