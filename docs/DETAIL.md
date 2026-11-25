# PHP 运行时插件（runtime-php）

## 它做什么

真实检测宿主已安装的 PHP CLI、汇总单版本详情（SAPI、扩展数、composer 路径），扫描 PHP 项目并做健康检查；0.3.0 起补默认版本切换与单进程守护（启动 / 停止 / 重启 / 状态 / 日志尾部）。
不内置 php 二进制、不引入常驻中间件、不做容器编排；守护没有 supervisor 进程，被守护应用 setsid 脱离会话独立运行，状态全部落盘。

## 协议与兼容性

协议 `kore.plugin.v1`，`lifecycle=lazy`，`depth_level=production`，`requires_kore >=0.1.0 <0.2.0`。
插件级能力 `system:admin` + `proc:write`；13 条方法级能力中只读探测 / 列表 / 日志为 `system:read`，版本钉住与守护配置增删为 `system:admin`，守护启停为 `proc:write`。

## 方法

- `versions` / `info` / `projects` / `doctor` / `paths`：探测、详情、项目扫描、健康检查与 PATH 解析；未检测到 PHP 给安装提示而非报错。
- `use {version?}` 与 `guard.*`：钉住默认版本（零参数清除）；注册并管理本机单进程应用，启动幂等、停止先 SIGTERM 再按需 SIGKILL。

## 明确不做

不管理 PHP-FPM 进程池、不切 `/etc/alternatives/php`、不改站点配置；不解析 `composer.lock` 依赖树、不执行 `composer install`。

## 安装与验证

`make check` 为本地门禁（gofmt → go vet → go test -timeout 200s），`make dist` 产出位级可复现的 `dist/runtime-php-0.3.0.tar.gz`。故障注入用例见 `tests/fault/`。
漏洞按 [SECURITY.md](SECURITY.md) 私下披露；MIT 许可；贡献见 [CONTRIBUTING.md](CONTRIBUTING.md)。