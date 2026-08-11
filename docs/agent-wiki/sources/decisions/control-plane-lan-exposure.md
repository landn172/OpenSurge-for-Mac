---
title: Control plane LAN exposure for a mobile H5 surface
kind: decision
status: accepted
---

# 控制面的局域网暴露与手机 H5 面

在此之前，Control API 只监听 loopback。这从来不是一条被决策过的边界，而只是
`wiki/concepts/gui-control-plane.md` 里的一句描述；`sources/decisions/` 下没有
任何条目讨论过服务暴露。本条目补上这个决策。

产品需求是：用手机浏览器（H5，不做原生 App）在**内网**查看和轻量操作全屋网关。

## 决策

1. Control API 可以监听**上游接口**的 IPv4 地址，即手机所在的家庭 Wi-Fi 网段。
   不绑 `cfg.Gateway.LANIP`：那是 Mac 对外提供 DHCP 的下游网段地址，其设备population
   恰好是本网关存在的意义所在的那批不可信设备（电视、IoT、客人）；而且在独立下游
   LAN 拓扑下手机根本连不到它。也不绑 `0.0.0.0`。
2. 手机面**以只读为主**：状态、设备流量、节点健康、诊断只读，加上切设备出口、切
   Selector、跑连通性检测这几个低风险动作。**不开放** gateway start/stop、网络配置
   写入、source 导入与 apply。
3. **暂不引入 TLS。** 明文的风险由第 2 条压制：手机侧凭据不再是 root 等价物。

## 为什么这三条是一组

三条互相支撑，不能单独取用。

今天 `internal/controlapi/` 只有两个凭据——`store.Token()` 写出的持久 bearer token
（256 位、不轮换、无撤销接口）和 12 小时滑动续期的全权限会话 cookie——**两个都等同
root，都不分作用域**。在明文 HTTP 上向局域网暴露其中任何一个，等于把下面这条链交给
任何抓到一个包的设备：

`POST /api/v1/sources` → `POST /api/v1/sources/{id}/apply` → `applyProfile`
（`internal/controlapi/configuration_actions.go:55-126`）

攻击者上传任意 mihomo profile 并热重载，从此**决定全屋每台设备的出口**，下游无法观测。
这是本 API 真正的 Tier-1 爆炸半径，比 gateway start/stop 严重得多。

所以「不上 TLS」只有在「手机拿到的不是 root 等价凭据」时才成立。**若以后要给手机面
开放写操作，必须同时重新评估 TLS 和凭据模型，不能只放开路由。**

## 影响

- `internal/controlapi/server.go:94-97` 的 bind allowlist 需要接受上游接口地址。
- `server.go:355-364` 的 `Host` allowlist 是**当前代码里唯一的 DNS rebinding 防线**
  （全仓库没有任何 CORS 头）。放开它必须换成**固定小集合**，
  **绝不能**改成通配或从 `r.Host` 推导。
- `server.go:399` 的 Origin 检查是与 `baseURL` 的精确相等比较，要改成集合成员判断。
  **不要**让 `baseURL` 跟随 `r.Host`：那会让 Origin 检查变成自我比较，CSRF 防护静默失效。
- QR / bootstrap URL 需要独立的 `publicBaseURL`，而不是复用 `server.go:175` 构造期常量。
  菜单栏侧不需要改 Swift：`APIClient.swift` 原样返回服务端给的 URL。
- 上游接口地址是 DHCP 分配、会变的，不能写死在
  `packaging/launchd/com.opensurge.control.plist`。需要新增「启动时按接口名发现地址」
  的能力；`cmd/opensurge-control/main.go:17` 目前只接受字面地址。
- 静态 SPA 在 `server.go:247-248` 是**不带认证**的。局域网绑定后，未认证的同网设备会
  拿到完整前端包。这是被接受的结果，但必须显式记录，不能当成 bug 报告处理。
- `GET /bootstrap`（`server.go:196`）会变成网络可达的发 cookie 端点。它只靠 192 位
  一次性 code 保护，**没有任何限流**。
- 只读手机面需要新的会话作用域维度；`server.go:373-406` today 没有这个概念。
- 前端不需要改：`web/src/api.ts` 全是相对路径 + `credentials: 'same-origin'`。

## 验证

这些改动**不触发** `make lab-test`：数据面（DHCP、DNS、mihomo 配置生成、pf、forwarding、
rollback、生命周期清理）完全没动。适用门槛是 `make gui-test`（含
`scripts/check-gui-packaging.sh`，因为要改 plist 与 postinstall）。

但**现有门槛里没有任何一条能证明「同网另一台设备能打开控制面」**。仓库现有的
real-device / same-lan 门槛全是数据面门槛。因此本决策要求新增一条 LAN 暴露门槛，
至少证明：

1. 同网另一台设备可达，跨网段不可达。
2. 未认证的同网设备在 `/api/v1/*` 上得到 401，同时确实拿到 SPA（预期结果）。
3. 配对后的会话在手机上可用，且手机的真实 Origin 能通过 Origin 检查。
4. 带 rebinding 式 `Host`（攻击者控制的域名解析到 Mac 的局域网地址）的请求仍被拒。
5. 只读面上不存在任何写路径——这条要在每次新增路由时重新证明。

第 4 条是单元测试证明不了、且当前没有任何门槛覆盖的一条。

补充两点实现后才看清的事实：

- 三个被放行的写操作**作用域不同**。设备 selector 只影响一台设备，连通性检测不改任何
  状态，但 **policy group selection 是全屋级的**——它改变该 group 路由的所有流量的出口
  （只能在已应用 profile 里已有的节点之间选）。这是这个面存在的意义之一，属于有意为之，
  但不要把三者当成同一量级。
- 「只读面上不存在写路径」这条不变量目前**只活在测试里**（`mobile_surface_test.go` 用 AST
  解析路由表），production 代码里没有一处集中声明「手机面是什么」。因此这条不变量的强度
  等于「有人会跑 `go test`」。若以后手机面继续扩大，应考虑把路由表提升为 production 数据结构。

## 重新打开条件

- 要给手机面开放写操作：必须同时处理 TLS 与作用域化、可撤销的凭据模型。
- 要支持外网访问：本决策不覆盖，需要新的决策条目。
- 若改为绑 `0.0.0.0` 或下游 `LANIP`：需要新的决策条目并重新做威胁模型。
