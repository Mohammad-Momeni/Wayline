# Contributing

Use synthetic gateways and reserved example IP addresses in tests. Keep saved profiles, passwords, imported Xray files, logs, signing keys and generated binaries out of commits.

Run the Go tests from `engine` and build the Windows desktop before sending a pull request. Changes to session cleanup, reconnect, packet ownership or chain ordering need a regression test.

Android changes should be checked on both ARM64 and ARMv7 hardware when available. Include Android version, build version, CPU architecture and sanitized diagnostics in bug reports.
