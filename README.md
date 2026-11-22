# kore-plugin-runtime-php

PHP 运行时插件（**骨架**）：覆盖多版本 PHP-FPM 管理 / 版本切换的方法面。本仓只交付协议骨架与参数校验，**不真起 FPM 进程、不内置任何 php/fpm 二进制**（裁决 37 / 提案 §2.1 批 2）。

## 协议面

- `kore.plugin.v1`，`lifecycle=lazy`，`depth_level=production`
- 能力声明：`system:admin`
- 方法：`php.versions`（同时接受 `runtime-php.versions` / 裸 `versions`）、`php.use`（同时接受 `runtime-php.use` / 裸 `use`）
- 握手的 stdout 留给协议帧，日志一律写 stderr

## 版本白名单（骨架口径）

`php.use` 的 `version` 必须在白名单内：`7.4` / `8.0` / `8.1` / `8.2` / `8.3`，精确匹配，不接受 `8.1.2` 之类的补丁版本号。版本参数仅做逻辑校验，**不执行 systemctl / 不连接 FPM / 不读写宿主配置**：

- 非法或白名单外的版本返回 `E_INVALID`
- 未知方法返回 `E_NOTFOUND`
- `php.versions` 返回白名单内的逻辑版本列表，当前逻辑版本为 `8.2`

## 四类故障注入

`tests/fault/` 四个文件分别注入 kill -9、超时、磁盘满、cgroup 超限，断言方法级返回结构化错误，且 `Error.Code` 与故障类别一一对应：

| 故障 | 文件 | 期望错误码 |
| --- | --- | --- |
| kill -9 | `kill_test.go` | `E_UPSTREAM` |
| 超时 | `timeout_test.go` | `E_UPSTREAM` |
| 磁盘满 | `disk_test.go` | `E_INTERNAL` |
| cgroup 超限 | `cgroup_test.go` | `E_UNSUPPORTED` |

## 构建与验收

```bash
gofmt -l . && GOWORK=off go vet ./... && GOWORK=off go test -timeout 200s ./...
make build && make dist
tar -tzf dist/runtime-php-0.1.0.tar.gz | head -1   # info.json
```

`dist` 使用确定性参数归档（`--sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner` + `gzip -9n`、逐条目 chmod 归一），重复构建 sha256 不变；`size_bytes` 由提交前实测包体积回写，与实际包体偏差 ≤10%（提案 §4.8 S-03）。
