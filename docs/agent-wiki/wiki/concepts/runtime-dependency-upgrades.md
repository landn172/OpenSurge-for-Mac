# 运行时依赖升级

OpenSurge 随 macOS 安装包分发两个受信任的运行时组件：`mihomo` 与
`dnsmasq`。它们不是用户可从 PATH 任意替换的插件。root Helper 只接受安装根目录内、
root-owned 且不可由 group/other 写入的可执行文件；这是网关高权限边界的一部分。

## 唯一事实源

`dependencies/runtime.lock.json` 是运行时组件版本、上游来源、许可证、双架构 artifact
和 SHA-256 的唯一事实源。

`go run ./cmd/opensurge-deps check` 会校验该锁文件，并要求
`THIRD_PARTY_NOTICES.md` 中 `runtime-dependencies` 标记包围的区块与它完全一致。
发行准备脚本从锁文件读取下载参数，而不是维护第二套版本常量。

## 更新流程

### mihomo

1. 在 lock file 中更新 version、对应源码链接，以及 `arm64` / `x86_64` 两个 release
   artifact 的 URL 和 SHA-256。
2. 更新标记包围的 third-party notice；运行 `go run ./cmd/opensurge-deps notices` 可以得到
   期望内容，再运行 `make deps-check`。
3. 在两种架构的 macOS runner 上执行 `prepare-gui-release-deps.sh`。发行工作流会用已下载的
   mihomo 对最终渲染的 gateway config 执行 `mihomo -t`。
4. 执行 `make test`、`make policy-control-test`，以及实际 macOS 环境中的
   `make lab-test-tun`。升级时必须重新确认 `/configs` 中 `tun.enable` 的 failure semantics；
   不能把旧版本的 TUN ready 契约外推到新版本。

运行中的小版本 mihomo 更新将来可复用 `restart-mihomo`：它会在停止旧 engine 前校验
当前渲染配置，并保持 dnsmasq、PF、forwarding 不变。激活前仍需先把候选二进制放入
root-owned 的版本目录、完成校验；禁止原地覆盖正在运行的可执行文件。

### dnsmasq

1. 在 lock file 更新 source archive URL、版本和 SHA-256。
2. 运行 `make deps-check`；发行流程会从 source 编译两种架构。
3. 发行工作流会用实际生成的 dnsmasq config 运行 `dnsmasq --test`，随后在真实 macOS
   环境执行 `make lab-test`。

dnsmasq 的独立激活需要专门的 `restart-dnsmasq` 事务：验证候选配置后停止旧进程、启动候选、
确认 DNS/DHCP listener 与 lease 路径正常；失败时恢复旧二进制。该过程会短暂影响 DHCP/DNS，
因此必须是显式、可审计的维护动作，而不是后台自动更新。

## 推荐的下一阶段：组件包，但不做在线任意下载

当需要在两个 OpenSurge 产品版本之间独立发布安全更新时，组件包应包含锁文件副本、二进制、
对应源码/许可证、双架构 SHA-256 和测试记录。安装时采用：

1. 下载到 root-owned staging 目录并验证 package provenance 与每个 SHA-256；
2. 写入不可变 `components/<name>/<version>/`，不覆盖现有版本；
3. 用候选二进制验证已渲染配置；
4. 记录候选/当前 component set，显式维护窗口内切换；
5. 失败时恢复上一个 component set 并启动旧进程。

`active` 指针及其解析目标必须都留在 OpenSurge 安装根目录并保持 root-owned；Helper 的现有
real-path 检查继续作为最后一道防线。没有实现上述签名、审计和回滚协议前，不提供 GUI
“在线更新 engine”按钮。

## 其他依赖

- Go 库：通过 `go.mod` / `go.sum` 更新，运行 `go mod tidy`、`make test`，并审阅许可证变化。
- Web 库：通过 `web/package.json` / `pnpm-lock.yaml` 更新，使用 frozen lockfile、web test/build。
- PF、sysctl、launchd：它们属于 macOS，不作为独立下载组件；按 macOS 支持矩阵进行 host-network
  Lab 验证。
