# PHP Runtime Plugin (runtime-php)

## What it does

Detects installed PHP CLI on the host, reports per-version details (SAPI, extension count, composer path), scans PHP projects and runs health checks; since 0.3.0 it also supports default-version pinning and a minimal single-process daemon (start / stop / restart / status / log tail).
It ships no php binary, adds no resident middleware and does no container orchestration; the daemon has no supervisor process; guarded apps run detached via setsid and all state is on disk.

## Protocol and compatibility

Protocol `kore.plugin.v1`, `lifecycle=lazy`, `depth_level=production`, `requires_kore >=0.1.0 <0.2.0`.
Plugin capabilities `system:admin` + `proc:write`; of the 13 method-level capabilities, read-only probes / lists / logs are `system:read`, version pinning and daemon config are `system:admin`, daemon start / stop / restart are `proc:write`.

## Methods

- `versions` / `info` / `projects` / `doctor` / `paths`: discovery, details, project scan, health check and PATH resolution; no PHP installed yields an install hint rather than an error.
- `use {version?}` and `guard.*`: pin the default version (zero arguments clear it); register and manage local single-process apps; start is idempotent, stop sends SIGTERM then SIGKILL if needed.

## Explicitly out of scope

No PHP-FPM pool management, no `/etc/alternatives/php` switching, no site config changes; no `composer.lock` dependency tree parsing and no `composer install`.

## Install and verification

`make check` is the local gate (gofmt, go vet, go test -timeout 200s); `make dist` produces a bit-for-bit reproducible `dist/runtime-php-0.3.0.tar.gz`. Fault-injection cases live in `tests/fault/`.
Report vulnerabilities privately per SECURITY.md; MIT license; contributions: CONTRIBUTING.md.