# pinctl

pinctl is an open-source pipeline-based I/O controller for Arduino-class boards.

It allows you to define device behavior (LEDs, sensors, relays, PWM outputs, etc.)
using visual pipelines instead of modifying Arduino firmware.

The microcontroller runs a minimal generic agent and exposes its pins over a serial protocol.
All logic — conditions, delays, comparisons, and automation — is executed on a Raspberry Pi
by the pinctl runtime and configured through a web-based visual editor.

This makes hardware behavior fully configurable without recompiling or flashing firmware.

## Features

- Visual pipeline editor (node-based)
- Control Arduino GPIO via Raspberry Pi
- No firmware regeneration or recompilation
- Digital I/O, PWM output, analog input
- Conditional logic and delays
- Multiple pipelines per board
- Designed for Raspberry Pi environments

## Philosophy

- Firmware should be generic and stable
- Behavior should be configurable, not hardcoded
- Hardware automation should be inspectable and debuggable
- Pipelines are easier to reason about than embedded state machines

## Example

Turn on an LED when it gets dark:

1. Connect a light sensor and an LED to Arduino
2. Add both devices in the pinctl web interface
3. Create a pipeline:
   - read light sensor
   - compare value with threshold
   - if below threshold → turn LED on
   - otherwise → turn LED off
4. Save and enable the pipeline

The behavior changes instantly without reflashing Arduino firmware.

## Tech Stack

- Raspberry Pi
- Arduino Uno (or compatible boards)
- Go / Vue 3 (runtime and admin UI)
- Serial communication (USB)
- Node-based pipeline engine
