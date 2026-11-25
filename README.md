# kore-plugin-runtime-php

语言：[简体中文](docs/DETAIL.md) · [English](docs/DETAIL.en.md) · [Español](docs/DETAIL.es.md) · [Français](docs/DETAIL.fr.md) · [Deutsch](docs/DETAIL.de.md) · [Русский](docs/DETAIL.ru.md) · [Português](docs/DETAIL.pt.md)

PHP 运行时插件（**生产级**）：真实检测宿主已安装的 PHP CLI、汇总单版本详情、
扫描 PHP 项目并做健康检查；0.3.0 起补**默认版本切换**与**单进程守护**
（启动 / 停止 / 重启 / 状态 / 日志尾部）。**不内置 php 二进制、不引入常驻中间件、不做容器编排**：
守护没有 supervisor 进程，被守护应用 setsid 脱离会话独立运行，状态全部落盘。

## 协议面

- `kore.plugin.v1`，`lifecycle=lazy`，`depth_level=production`，插件级能力 `system:admin` + `proc:write`（后者是守护启停真实进程所需；方法级能力只能收窄、不能跨域放大）
- **方法级能力**（`method_capabilities`，13 条）：只读探测 / 列表 / 日志 = `system:read`；
  版本钉住与守护配置增删 = `system:admin`；`guard.start` / `guard.stop` / `guard.restart` = `proc:write`
- 方法：`versions` / `info` / `projects` / `doctor` / `paths` / `use` /
  `guard.add` / `guard.list` / `guard.start` / `guard.stop` / `guard.restart` / `guard.remove` / `guard.logs`
  （域必须等于插件 id `runtime-php`，PLUGIN-PROTOCOL §7.2；不接受裸动词或旧 `php.` 前缀）
- 握手的 stdout 留给协议帧，日志一律写 stderr；未知方法 `E_NOTFOUND`，参数非法 `E_INVALID`
- 运行期配置：`KORE_PHP_ROOT`（插件状态目录，默认 `/var/lib/kore/php`，同时是 root 白名单的一项）

## 方法

### `runtime-php.versions`（无参数）

探测宿主全部 PHP CLI 安装：PATH 里的 `php`、`~/.phpenv/versions/*/bin/php`、
`/usr/bin/php*`、`/usr/local/bin/php*`、`/opt/php*/bin/php`、`/etc/alternatives/php`。
每项执行 `<path> -v` 解析版本（`PHP 8.3.6 (cli)` → `8.3.6`）并执行 `<path> -m` 统计扩展数；
同一真实文件按符号链接/硬链接去重（`is_default` 由 PATH 解析决定）。

```json
{
  "versions": [
    {"version": "8.5.4", "path": "/usr/bin/php8.5", "source": "path", "is_default": true,
     "sapi": "cli", "extensions_count": 19, "bin_dir": "/usr/bin",
     "package_manager": "composer", "package_manager_path": "/usr/local/bin/composer"}
  ],
  "default_version": "8.5.4",
  "count": 1
}
```

- `source` ∈ `path` / `known_path` / `phpenv`；`sapi` 固定 `cli`（本插件只管理 CLI，不管理 FPM 进程）
- `bin_dir` 是安装目录；`package_manager` 固定 `composer`；`package_manager_path` 优先同目录 `composer`、其次 PATH 上的
  `composer`（**只做文件查找，不执行命令**，不会为每个版本额外拉起进程）
- **未检测到 PHP 不报错**：`versions=[]`、`count=0`、`default_version=""`，并给固定 `hint`：
  `未检测到 PHP，可执行 apt install php-cli，或用 phpenv / 官方包安装后把 php 放进 PATH`
- glob 展开结果按 basename 过滤（只接受 `php` / `php8.5` 这类名字），`phpize` / `php-config` / `phpdbg` 不会被误当解释器
- `-v` 输出无法解析（如 RC 构建、非 PHP 程序）的候选跳过，不影响其他安装

### `runtime-php.info {version?}`

`version` 省略时取默认版本；给出时接受 `8.5.4`（精确）与 `8.5`（该分支最高补丁版本），未安装返回 `E_INVALID`。
执行 `php -v`、`php -m`、`php -i`、`php --ini`：

```json
{
  "version": "8.5.4",
  "php_path": "/usr/bin/php8.5",
  "ini_path": "/etc/php/8.5/cli/php.ini",
  "extensions": ["Core", "date", "json", "openssl", "pdo", "..."],
  "ini_settings": {
    "memory_limit": "256M",
    "upload_max_filesize": "2M",
    "post_max_size": "8M",
    "max_execution_time": "0"
  }
}
```

- `ini_path` 取 `php --ini` 的 `Loaded Configuration File`；未加载 php.ini 时为空串（合法状态）
- `ini_settings` 取 `php -i` 的**有效值**（`key => 当前值 => 主值` 的第一段）；CLI 的 `max_execution_time` 常为 `0`（不限制），属于 PHP 的 CLI 默认行为
- `php -i` / `php --ini` 失败只降级对应字段，不影响版本与扩展列表

### `runtime-php.projects {root}`

扫描 root 下深度 ≤3（root 为第 0 层）、条目数 ≤200 的 PHP 项目，只读 `composer.json` 前 64 KiB：

```json
{
  "projects": [
    {"path": "/var/www/app", "name": "acme/app", "php_requirement": "^8.1", "dependencies_count": 2, "has_lock": true, "framework": "laravel"}
  ],
  "count": 1,
  "warnings": []
}
```

- `php_requirement` 取 `require.php`；`dependencies_count` 只数**带 vendor 前缀**的包名，
  `php` / `ext-*` / `lib-*` / `composer-*` 这类平台需求不计入
- `framework` 从 `require` 识别：`laravel/framework` → `laravel`、`symfony/framework-bundle|symfony/symfony` → `symfony`、
  `topthink/framework|topthink/think` → `thinkphp`；识别不出则省略该字段
- `has_lock` 看 `composer.lock` 是否存在；`name` 缺失时回退到目录名
- 跳过 `vendor` / `node_modules` / `.git` / 隐藏目录（`vendor` 里是第三方 composer.json，绝不算本项目）；
  坏 JSON / 超大文件 / 不可读目录跳过并写 `warnings`，不中断整次扫描

### `runtime-php.doctor {root?}`

```json
{
  "ok": false,
  "checks": [
    {"name": "php", "status": "pass", "detail": "检测到 1 个 PHP，默认 8.5.4（/usr/bin/php8.5）"},
    {"name": "version_supported", "status": "pass", "detail": "PHP 8.5.4 满足常见框架要求（≥8.0）"},
    {"name": "ext_mbstring", "status": "fail", "detail": "缺少扩展 mbstring；Debian/Ubuntu 可执行 apt install php-mbstring"},
    {"name": "ext_curl", "status": "warn", "detail": "缺少扩展 curl；Debian/Ubuntu 可执行 apt install php-curl"},
    {"name": "memory_limit", "status": "pass", "detail": "memory_limit=256M（≥128M）"},
    {"name": "ini_loaded", "status": "pass", "detail": "已加载 php.ini: /etc/php/8.5/cli/php.ini"}
  ]
}
```

- `ok` = 没有任何 `fail` 项
- `php` / `version_supported`：`<7.4` fail、`7.4–7.x` warn（满足 Laravel 8 / Symfony 5 下限但低于 8.0 基线）、`≥8.0` pass
- 必需扩展 `mbstring` / `json` / `pdo` / `openssl` 缺失 → **fail**；推荐扩展 `curl` / `fileinfo` 缺失 → **warn**；
  detail 带安装命令（教用户怎么办）
- `memory_limit`：`-1`（不限制）或 ≥128M pass，低于 128M fail
- `ini_loaded`：php.ini 真被加载 pass，未加载 warn
- 检出多版本时追加 `path_conflict` warn（同一台机器多版本共存要固定路径）
- 给出 `root` 时追加 `project_root` 检查（发现 0 个项目为 warn，不误判为 fail）

### `runtime-php.paths`（无参数）

解释「**为什么用到了这个版本**」：PATH 原始顺序、候选可执行名解析结果、默认 PHP 的解析链，
以及按探测优先级排列的发现来源。只读：复用 `versions` 的同一套候选收集逻辑与一次 `-v`，不写宿主。

```json
{
  "path_env": ["/usr/local/sbin", "/usr/local/bin", "/usr/bin"],
  "executables": [{"name": "php", "path": "/usr/local/bin/php", "found": true}],
  "default": {
    "command": "php", "path": "/usr/local/bin/php", "real_path": "/usr/bin/php8.5",
    "version": "8.5.4", "bin_dir": "/usr/local/bin",
    "package_manager": "composer", "package_manager_path": "/usr/local/bin/composer",
    "found": true
  },
  "search_order": [
    {"source": "path", "paths": ["/usr/local/bin/php"]},
    {"source": "phpenv", "paths": ["/root/.phpenv/versions/8.1.0/bin/php"]},
    {"source": "known_path", "paths": ["/usr/bin/php8.5"]}
  ]
}
```

- `search_order` 的条目顺序 = 实际探测优先级（PATH → phpenv → 常见路径），只列**真实存在**的来源；同一路径只列一次（软链仍按来源保留，便于解释多版本来源）
- 未检测到 PHP 时不报错：`default.found=false`、`search_order=[]`，并给与 `versions` 相同的 `hint`

> 测试补充：`guard_test.go`（假 `GuardIO`，不拉起真实进程）覆盖零参数启动（G-33 A1）、start 幂等、
> start/stop/restart 状态流转、SIGTERM 无效升级 SIGKILL、日志尾部、应用名 / 目录越界 / `..` 穿越 /
> 脚本缺失的「怎么办」文案、退出码落盘、版本钉住与清除。

### `runtime-php.use {version?}` 与进程守护（`guard.*`）

与 runtime-node 同一套契约：

- `use {version?}`：把已安装的 PHP 钉为默认（写 `<state>/default-version.json`，不改宿主 PATH）；
  `versions` 的 `pinned_version` / `pinned_path` 回显
- `guard.add {name, dir, script, args?, version?}`：注册应用（`script` 通常是 `index.php`）；
  `dir` 走白名单 + 软链二次校验，`name` 匹配 `^[a-z][a-z0-9-]{0,31}$`
- `guard.start {names?}`（幂等，零参数 = 全部）/ `guard.stop {names?}`（SIGTERM 进程组 → 5s 后 SIGKILL）/
  `guard.restart {name}` / `guard.list` / `guard.logs {name, lines?}`（≤500 行 / 64 KiB）/ `guard.remove {name}`
- **诚实边界**：不自动重启（无常驻 supervisor）；进程退出后显示 `crashed` + `exit_code`；
  需要「挂了自动起」用 cron 插件定时调幂等的 `guard.start`；不做编排（单机单进程，上限 32 个应用）

## 安全模型

- **守护的进程边界**：固定 argv（无 shell）、工作目录限定白名单、子进程环境**剔除全部 `KORE_*`**、
  日志 0640 / 守护目录 0750；PID 存活用「PID + 启动时刻」双判据，`Z` / `X` 状态判为不在运行
- **root 白名单**：`/var/www`、`/opt`、`/home` + 插件状态目录（`KORE_PHP_ROOT`，默认 `/var/lib/kore/php`）
- **两层归属校验**：词法层（绝对路径、无 NUL / 反斜杠 / `..`、在白名单内）+ 真实层（`EvalSymlinks` 后再次比对，
  挡住"白名单目录本身或父级被换成软链"的逃逸）；`root` 必须是已存在的目录
- **只读**：不执行项目脚本、不写宿主文件、不读写 php.ini；`tests/` 与 `composer.json` 只读元数据
- **命令固定 argv + 5s 硬超时**：`php -v` / `-m` / `-i` / `--ini`，无 shell、无管道
- **输出有上限**：单条命令最多捕获 1 MiB stdout / 32 KiB stderr，超出静默截断（防 rogue php 刷爆插件内存，且不把正常大输出变成命令失败）
- **零 ticker**：没有任何后台轮询或定时器，只在方法调用时探测

## 构建与验收

```bash
gofmt -l . && GOWORK=off go vet ./... && GOWORK=off go test -timeout 200s ./...
make build && make dist
tar -tzf dist/runtime-php-0.2.2.tar.gz | head -1   # info.json
```

`dist` 使用确定性参数归档（`--sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner` + `gzip -9n`、逐条目 chmod 归一），
重复构建 sha256 不变；`size_bytes` 每轮从包内读回并与实际包体收敛（偏差 ≤2 字节）。

## 四类故障注入

`tests/fault/` 四个文件分别注入 kill -9、超时、磁盘满、cgroup 超限，断言四个方法都返回结构化错误，
且 `Error.Code` 与故障类别一一对应（另有 `okExecutor` sanity 用例防止"永远失败"的假通过）：

| 故障 | 文件 | 期望错误码 |
| --- | --- | --- |
| kill -9 | `kill_test.go` | `E_UPSTREAM` |
| 超时 | `timeout_test.go` | `E_UPSTREAM` |
| 磁盘满 | `disk_test.go` | `E_INTERNAL` |
| cgroup 超限 | `cgroup_test.go` | `E_UNSUPPORTED` |

## 不做（明确边界）

- 不真起 / 不管理 PHP-FPM 进程池、不切 `/etc/alternatives/php`、不改站点配置（`use` 方法在 0.2.0 移除）
- 不内置 php 二进制；宿主没装 PHP 时 `versions` 给安装指引而不是报错
- 不解析 `composer.lock` 依赖树、不执行 `composer install`

<!-- oss:begin -->
## 开源与贡献

[![CI](https://github.com/kore-pl/kore-plugin-runtime-php/actions/workflows/ci.yml/badge.svg)](https://github.com/kore-pl/kore-plugin-runtime-php/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)

本仓库是 **kore** 的 `PHP 运行时插件`（id `runtime-php`），独立仓库、独立版本线、独立 CI：GitHub Actions 只手动触发（`workflow_dispatch`），按版本 tag 自动触发在 Gitea Actions（`.gitea/workflows/ci.yml`，push `v*` + 手动）。内核与协议见 [kore](https://github.com/kore-pl/kore)。

- **许可**：MIT（inbound = outbound，不要求 CLA）；提交用 `git commit -s` 签 DCO。
- **贡献**：[CONTRIBUTING.md](CONTRIBUTING.md)（构建、门禁、发版、边界）。
- **安全**：[SECURITY.md](SECURITY.md)——漏洞不要开公开 issue。
- **行为准则**：[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。
- **兼容性**：`protocol: kore.plugin.v1` · `requires_kore: >=0.1.0 <0.2.0` · `lifecycle: lazy` · 深度等级 `production`。
- **验证方式**：本地 `make check` 优先（GitHub CI 只手动触发，Gitea CI 按版本 tag 触发；发布前 `make dist` 连跑两次核对 sha 一致）。
- **发布产物**：`make dist` → `dist/runtime-php-0.3.0.tar.gz` + `.sha256`（位级可复现，连跑两次 sha 相同）。
- **相关仓库**：[索引 kore-plugins](https://github.com/kore-pl/kore-plugins) · [协议文档](https://github.com/kore-pl/kore/blob/main/docs/PLUGIN-PROTOCOL.md) · [SDK 指南](https://github.com/kore-pl/kore/blob/main/docs/PLUGIN-SDK.md)



如果你觉得这个插件有用，欢迎给仓库点个 ⭐ Star —— 你的支持是持续维护的动力。

<!-- oss:end -->
