---
name: code-review
description: Automated code review skill that analyzes code changes for quality, security, and best practices
version: 1.0.0
author: SwarmOS Team
category: development
tags:
  - code-review
  - quality
  - security
  - linting
triggers:
  - type: tool
    pattern: Write
  - type: tool
    pattern: Edit
  - type: file_pattern
    pattern: "*.go"
  - type: file_pattern
    pattern: "*.py"
---

# Code Review Skill

This skill provides automated code review capabilities for your development workflow.

## Capabilities

1. **Security Analysis** - Detect potential security vulnerabilities
2. **Code Quality** - Identify code smells and suggest improvements
3. **Best Practices** - Ensure adherence to language-specific conventions
4. **Documentation** - Check for missing documentation

## Usage

This skill automatically activates when you use Write or Edit tools on code files.

### Available Scripts

- `scripts/analyze.py` - Main analysis script
- `scripts/security-scan.sh` - Security vulnerability scanner

### Configuration

Create a `.code-review.yaml` in your project root:

```yaml
rules:
  security: strict
  style: relaxed
  documentation: required
ignored:
  - vendor/
  - node_modules/
```

## Hooks Integration

This skill registers PostToolUse hooks to analyze code after Write/Edit operations.