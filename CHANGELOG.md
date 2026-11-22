# Changelog

## 0.1.0 (2026-11-22)

- feat: 新建 runtime-php 插件骨架，复用 kore.plugin.v1 握手与会话循环
- feat: runtime-php.versions / runtime-php.use 方法级参数校验与结构化结果，版本号走白名单（7.4/8.0/8.1/8.2/8.3），不真起 FPM 进程、不内置 php/fpm 二进制
- refactor: 方法名按协议统一为 `<plugin-id>.<action>`（原 `php.*` → `runtime-php.*`），移除裸动词与旧域前缀兼容
- feat: 四类故障注入测试（kill -9 / 超时 / 磁盘满 / cgroup 超限），验证方法级错误返回
- build: 确定性打包（--sort=name --mtime=@0 --owner=0 --numeric-owner + gzip -9n + chmod 归一），GOWORK=off 构建
