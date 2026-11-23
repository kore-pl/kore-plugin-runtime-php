# Changelog

## 0.2.0 (2026-11-23)

- feat: `runtime-php.versions` 改为**真实检测**宿主 PHP CLI（PATH 的 `php`、`~/.phpenv/versions/*/bin/php`、
  `/usr/bin/php*`、`/usr/local/bin/php*`、`/opt/php*/bin/php`、`/etc/alternatives/php`），
  `php -v` 解析版本（`PHP 8.3.6 (cli)` → `8.3.6`）、`php -m` 统计扩展数、`sapi=cli`、`is_default` 由 `which php` 决定、
  软链/硬链按真实路径去重；未检测到 PHP 时返回 `count=0` + 固定 hint，不报错
- feat: 新增 `runtime-php.info`：真实 `php -v` / `php -m` / `php -i` / `php --ini`，
  返回 `ini_path` 与 `memory_limit` / `upload_max_filesize` / `post_max_size` / `max_execution_time` 有效值；
  `version` 接受 `8.5.4` 与 `8.5`（后者取分支最高补丁版本）
- feat: 新增 `runtime-php.projects`：扫描 root（深度 ≤3 / 条目 ≤200）下的 PHP 项目，
  返回 `name` / `php_requirement`（`require.php`）/ `dependencies_count`（只数 vendor 包）/
  `has_lock` / `framework`（laravel / symfony / thinkphp）；跳过 `vendor` / 隐藏目录，
  `composer.json` 只读前 64 KiB，坏 JSON 跳过并写 warnings
- feat: 新增 `runtime-php.doctor`：真实检查 PHP 安装 / 版本基线（<7.4 fail、7.4 warn、≥8.0 pass）/
  必需扩展 `mbstring` `json` `pdo` `openssl`（缺失 fail）/ 推荐扩展 `curl` `fileinfo`（缺失 warn）/
  `memory_limit ≥128M` / php.ini 是否加载 / 多版本共存 / 可选项目目录
- security: root 白名单（`/var/www`、`/opt`、`/home`、`<state>`）+ `ResolveExisting` + 符号链接二次归属校验；
  外部命令固定 argv + 5s 超时 + 输出上限（1 MiB stdout / 32 KiB stderr，防 rogue php 刷爆内存）；只读元数据，不执行项目脚本
- refactor: 移除骨架方法 `runtime-php.use`、版本白名单与 `noopExecutor`（0.2.0 方法面为 versions / info / projects / doctor）
- test: 真实 E2E（本机 PHP 8.5.4、`/tmp/kore-php-test` 两项目、二进制空 PATH 降级路径）；
  `tests/fault/` 四类故障注入覆盖全部四个方法

## 0.1.1 (2026-11-23)

- refactor: 方法名统一为 runtime-php.<action>，对齐协议域校验
- build: 位级可复现打包（stage chmod + tar --mode 归一、size_bytes 收敛），二进制改嵌 manifest.json，消除同版本 sha 漂移

## 0.1.0 (2026-11-22)

- feat: 新建 runtime-php 插件骨架，复用 kore.plugin.v1 握手与会话循环
- feat: runtime-php.versions / runtime-php.use 方法级参数校验与结构化结果，版本号走白名单（7.4/8.0/8.1/8.2/8.3），不真起 FPM 进程、不内置 php/fpm 二进制
- refactor: 方法名按协议统一为 `<plugin-id>.<action>`（原 `php.*` → `runtime-php.*`），移除裸动词与旧域前缀兼容
- feat: 四类故障注入测试（kill -9 / 超时 / 磁盘满 / cgroup 超限），验证方法级错误返回
- build: 确定性打包（--sort=name --mtime=@0 --owner=0 --numeric-owner + gzip -9n + chmod 归一），GOWORK=off 构建
