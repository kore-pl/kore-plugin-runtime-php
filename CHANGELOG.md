# Changelog

## [0.3.0] - 2026-11-25

### Added

- **进程守护（极简 pm2 同款能力，不做编排）**：`guard.add` / `guard.list` / `guard.start` /
  `guard.stop` / `guard.restart` / `guard.remove` / `guard.logs`。启动用 `<php> <script> <args...>`
  拉起脱离会话（setsid）的子进程，日志追加到 `<state>/guard/<name>.log`；停止先给进程组 SIGTERM、
  5s 不退升级 SIGKILL；状态含 running / stopped / crashed 与退出码。没有 supervisor 常驻进程（G-05 / G-19），
  `guard.start` 幂等、零参数 = 全部启动。
- **默认版本切换**：`use {version?}` 钉住默认 PHP（写 `<state>/default-version.json`，不改宿主 PATH），
  零参数 = 清除；`versions` 新增 `pinned_version` / `pinned_path`。

### Changed

- 插件级能力增加 `proc:write`；`method_capabilities` 5 条 → 13 条（guard 启停 = `proc:write`）。
- 原本把 `runtime-php.use` 当作「未声明方法」的测试用例改为 `runtime-php.install`（use 已成为真实方法）。

### Fixed

- 僵尸进程误判：子进程退出后由阻塞 `Wait` 的回收 goroutine 回收并落盘退出码，`Probe` 把 `Z` / `X`
  判为不在运行。

## 0.2.2 (2026-11-23)

- fix: `runtime-php.paths` 的 `hint` 语义（原 0.2.1 已发布，按「改 dist sha 必须 bump」升 patch）——
  装了 PHP 但 PATH 上没有 `php` 时不再误报「未检测到 PHP」，改为说明「已安装的 PHP 仍在 versions 列表中，
  但无法确定默认版本，请检查 PATH」；确实一个都没装才给安装提示
- test: 空 PATH 二进制降级用例断言由「恒为安装提示」改为按「有无安装」区分两种 hint；单测覆盖两条路径
- docs: README 修正 `search_order` 的去重口径（同一路径只列一次，软链按来源保留）


## 0.2.1 (2026-11-23)

- feat: 方法级能力声明（§3.5 #12）——`runtime-php.versions` / `info` / `projects` / `doctor` / `paths` 全部 `system:read`，
  只读方法不再按插件级 `system:admin` 计入写审计；插件级能力保持不变，作为未声明方法的 fail-closed 回退
- feat: `runtime-php.versions` 增强元信息——每项新增 `bin_dir`（安装目录）、`package_manager`（固定 `composer`）、
  `package_manager_path`（同目录 composer 优先、其次 PATH，**只做文件查找不执行命令**）
- feat: 新增 `runtime-php.paths`——返回 `path_env`（PATH 原始顺序）、`executables`（php 解析结果）、
  `default`（which php 解析链 + 版本 + 包管理器）与 `search_order`（按探测优先级列出的命中来源），
  用于排查「为什么用到了这个版本」；未检测到 PHP 时 `default.found=false` + 固定 hint，不报错
- test: 新增 capability 双向校验（声明 vs 分发表 vs 内核键格式/值域）、增强字段与 `paths` 单测、
  二进制空 PATH 降级链路补 `paths` 断言；故障注入扩到五个方法
- docs: README 补 5 方法契约、增强字段与 `paths` 口径、方法级能力说明
- note: **进程守护 / 扩展管理本轮不做**——启用 FPM 池、装扩展都是写操作且需要 root（写 `/etc/php`、执行包管理器、
  reload FPM），必须走 `root_actions` + 不可逆确认，另立任务实现；本轮只交付只读增强
- build: 版本 0.2.1（只加元数据 + 只读增强 → patch），`info.json` / `manifest.json` 同步


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
