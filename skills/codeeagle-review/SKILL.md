---
name: codeeagle-review
description: Review code changes and diffs against codebase conventions using CodeEagle's knowledge graph. Use for code review, convention checking, and identifying missing tests.
allowed-tools: Bash(codeeagle *)
---

# CodeEagle Review

Run the CodeEagle code review agent to review changes against codebase conventions.

## Usage

Use the Bash tool to run:
```
codeeagle agent review "$ARGUMENTS"
```

To review a specific git diff or PR reference:
```
codeeagle agent review --diff <ref>
```

## Prerequisites
- CodeEagle must be installed and on PATH
- Project must be initialized (`codeeagle init`) with an indexed graph (`codeeagle sync`)

## Notes
- Reviews diffs against codebase conventions and patterns
- Flags deviations from established patterns
- Identifies missing tests for changed code paths
- Highlights complexity hotspots in modified code
- Performs security pattern checks (auth, input validation, secrets)
