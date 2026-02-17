# Security Policy

## Supported Versions

| Version | Supported          |
|---------|--------------------|
| 1.0.x   | :white_check_mark: |
| < 1.0   | :x:                |

## Reporting a Vulnerability

**Please do NOT open public GitHub issues for security vulnerabilities.**

If you discover a security vulnerability in any repository under the `openjobspec` organization, please report it responsibly by emailing:

**[security@openjobspec.org](mailto:security@openjobspec.org)**

### What to Include

- Description of the vulnerability
- Steps to reproduce the issue
- Affected repository and version
- Potential impact assessment
- Any suggested fixes (if applicable)

### Response Timeline

- **Acknowledgment:** Within 48 hours of your report
- **Assessment:** Within 7 days we will provide an initial assessment
- **Resolution:** We aim to release a fix within 30 days of confirmation

### Coordinated Disclosure

We follow a coordinated disclosure process:

1. Reporter submits vulnerability privately via email
2. We acknowledge receipt and begin investigation
3. We develop and test a fix
4. We release the fix and publish a security advisory
5. Reporter is credited (unless they prefer anonymity)

We ask that you allow up to **90 days** from initial report before public disclosure, to give us time to develop and release a proper fix.

### Scope

This security policy applies to all repositories under the [openjobspec](https://github.com/openjobspec) GitHub organization.

### Credit

We believe in recognizing the efforts of security researchers. With your permission, we will acknowledge your contribution in the security advisory and release notes.

## Security Best Practices for Implementers

If you are implementing an OJS-compliant backend or SDK, please review the security considerations in the [Core Specification](https://github.com/openjobspec/spec/blob/main/spec/ojs-core.md) and ensure your implementation follows the recommended practices for authentication, authorization, and input validation.
