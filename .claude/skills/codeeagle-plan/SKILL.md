---
name: codeeagle-plan
description: Analyze impact, dependencies, and scope of planned code changes using CodeEagle's knowledge graph. Use for impact analysis, change risk assessment, and scope estimation.
allowed-tools: Bash(codeeagle *)
---

# CodeEagle Plan

Run the CodeEagle planning agent to analyze the impact and scope of code changes.

## Usage

Use the Bash tool to run:
```
codeeagle agent plan "$ARGUMENTS"
```

## Prerequisites
- CodeEagle must be installed and on PATH
- Project must be initialized (`codeeagle init`) with an indexed graph (`codeeagle sync`)

## Notes
- Performs impact analysis: what would be affected if you change X?
- Maps dependencies: what depends on service Y?
- Estimates scope: what files/services does feature Z touch?
- Assesses change risk based on complexity and test coverage
