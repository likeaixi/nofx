# NOFX 多用户 + TOTP + 摘除 Claw402 设计

- 日期：2026-11-14
- 分支：`dev-allin`（fork: likeaixi/nofx）
- 状态：已与 owner 逐节确认

## 1. 背景与目标

当前 NOFX 是单用户（单 owner）系统：仅第一个注册用户可存在（`api/handler_user.go:47-57` + `store/user.go:98 CreateFirst`）。部署于 https://hyperaitrade.com 的实例需要支持多人使用。

目标：
1. 多用户：邀请码注册，owner + 普通用户两级角色，owner 可管理用户
2. 强制 TOTP（谷歌验证器）：注册时绑定，登录时校验
3. 摘除 Claw402：用户不再使用实例提供的钱包付费模型，改为自带模型服务商 API Key（OpenAI 兼容），创建 trader 时自选交易所与模型

## 2. 现状调查结论（实现依据）

**已多用户就绪、无需改动**：
- 数据层：traders/strategies/exchanges/ai_models 全部按 `user_id` 作用域（`store/trader.go`、`store/exchange.go`、`store/strategy.go`、`store/ai_model.go`），有 IDOR 回归测试（`api/security_regression_test.go`）
- 认证：JWT 每用户独立 `auth_version`（改密/停用即失效），会话撤销按 `(userID, sessionID)`（`store/session.go`）
- 自定义模型：`ai_models` 表已有 `Provider/APIKey(加密)/CustomAPIURL/CustomModelName`，支持任意 OpenAI 兼容 API（`docs/getting-started/custom-api.md`）
- trader 关联该用户自己的 ai_model + exchange（`store/trader.go:198 GetFullConfig`），创建时自选即现状
- 前端已有 `qrcode.react`，可直接显示 TOTP 二维码

**需要改动**：
- 注册闸门、前端"注册已关闭"页（`web/src/components/auth/RegisterPage.tsx:36`）
- 无任何 TOTP 实现；需引入 `github.com/pquerna/otp`
- Claw402 用户可见入口 + 环境变量读取（`kernel/engine.go:197-210`、`provider/nofxos/client_resolve.go:19`、`provider/vergex/client.go:128`）
- 单用户化石：`main.go:115` 硬编码 `"default"`、`store/ai_model.go:166 GetAnyEnabled()`、`store/ai_model.go:60,71 FindOrphanClaw402/AdoptModel`（跨用户收养，删）、`store/user.go:181 EnsureAdmin()`（死代码，删）、前端 `AuthContext.tsx:162-186 loginAdmin` 幽灵路由（删）、`web/src/data/faqData.ts:504` 单运营者文案

## 3. 账户体系设计

### 3.1 注册（两步）（2026-11-15 修订：服务端原子认领，confirm 不再携带邀请码）

1. `POST /api/register` `{email, password, invite_code?}`
   - `userCount == 0`（实例未初始化）：免邀请码，建 owner（`CreateFirst` 串行化首用户抢占）
   - 否则必须携带有效邀请码（存在、未用、未作废，`GetValid` 预检）
   - 校验 email 唯一；创建**待激活用户**：`role=user`（或首用户 `owner`）、`totp_secret=新随机`、`totp_enabled=false`，并把邀请码**落盘到待激活用户行**（`users.registration_invite`）；**此步不标记邀请码已用**
   - 返回 `{totp_secret, otpauth_url, is_first_user}`，前端显示二维码
2. `POST /api/register/confirm` `{email, password, totp_code}`（**无 invite_code 字段**）
   - 复验密码 + 校验验证码（`pquerna/otp`，窗口 ±1 步）
   - 服务端从待激活行读取 `registration_invite` 并单条原子认领（`UPDATE ... WHERE code=? AND used_by IS NULL AND revoked=false`，`RowsAffected=1` 否则 403；并发同码只有一个成功）→ `totp_enabled=true` → 生成 8 个一次性恢复码返回（仅此一次明文展示）
   - 邀请码是否消耗由服务端行状态决定，客户端省略/篡改字段无法绕过单次认领

### 3.2 登录

`POST /api/login` `{email, password, totp_code}`
- `totp_code` 接受 6 位验证码**或**任一未用恢复码（恢复码命中即作废）
- 成功响应 `{token, user_id, email, role}`（2026-11-15 修订：新增 `role` 供前端做 owner 页签判定；不再返回 `message`）
- `totp_enabled=false` 的账号一律 403 `totp_binding_required`，前端引导进入补绑流程（覆盖两类账号：注册未 confirm 的半激活账号、升级后未绑定的存量账号）
- 失败信息保持现有防枚举策略（统一错误文案 + dummy bcrypt）
- 沿用现有登录限流（已按真实客户端 IP）

### 3.3 存量账号补绑（如 likeaixi@gmail.com）

- `POST /api/auth/bind-totp` `{email, password}`：密码验证后重置 `totp_secret`，返回新二维码（同注册第一步语义，可重复调用重置）
- `POST /api/auth/bind-totp/confirm` `{email, password, totp_code}`：启用 + 发新恢复码
- 密码即临时身份证明，无需已失效的旧会话

### 3.4 停用与恢复

- owner 停用用户：`disabled=true` + `auth_version+1`（全部会话立即失效）+ 登录拒绝
- **运行中 trader 不强停**（持仓安全）；owner 可在 Dashboard 手动停止该用户的 trader
- **owner 不能停用/降级自己**（防实例锁死）
- 恢复：`disabled=false`，用户重新登录

### 3.5 owner 管理接口（普通用户一律 403）

| 接口 | 说明 |
|---|---|
| `POST /api/admin/invites` | 生成邀请码（随机 10 字符），不过期 |
| `GET /api/admin/invites` | 列表（含使用状态） |
| `DELETE /api/admin/invites/:code` | 作废未用码 |
| `GET /api/admin/users` | 用户列表（email/role/disabled/创建时间/最近登录） |
| `PATCH /api/admin/users/:id` | `{disabled: bool}` 停用/恢复 |

owner 判定：`role == "owner"`（数据库字段，不用 env）。

### 3.6 兜底 CLI

`nofx reset-totp --email x@y.com`：服务器本地执行（与 `reset-password` 同款安全模型），重置 `totp_secret` 并置 `totp_enabled=false`，下次登录走补绑。

## 4. 模型配置与 Claw402 摘除

### 4.1 模型配置主路径

- 模型配置 = `custom` provider：`CustomAPIURL + CustomModelName + APIKey`（加密落库），任意 OpenAI 兼容 API
- 每用户可配多个模型，按 `user_id` 隔离（现状）

### 4.2 摘除范围（分层）

| 层 | 动作 |
|---|---|
| API | 删除 `POST /api/onboarding/beginner`、`GET /api/onboarding/beginner/current` |
| 前端 | 移除 AI 钱包 onboarding UI、模型下拉 claw402 选项、相关 FAQ/i18n 文案 |
| 环境变量 | 移除全部读取点：`CLAW402_WALLET_KEY`、`CLAW402_WALLET_ADDRESS`、`CLAW402_DEFAULT_MODEL`、`NOFX_X402_RECEIPT_RPC_URL` |
| 内部包 | `mcp/payment`、`provider/nofxos` 停止注册、不可达但**保留代码**（最小化上游合并冲突）；彻底删包列为后续可选清理 |
| 数据迁移 | 启动迁移删除 `ai_models` 中 `provider='claw402'` 行 |

### 4.3 付费数据连带影响

- 靠 x402 钱包付费的数据源（vergex/coinank 付费档）下线，UI 入口隐藏
- 免费数据源与自带 Key 数据源（TwelveData/Alpaca 等）不受影响
- trader 决策引擎数据链路保持可用，缺付费源时优雅降级（现状已有降级路径）

### 4.4 Trader 创建

无需改流程：现有 `GetFullConfig(userID, traderID)` 三表 join 已按用户隔离；摘除 claw402 后下拉列表自然只剩用户自配的模型与交易所。

## 5. 数据库变更

- 新表 `invite_codes`：`code(PK) / created_by / used_by(null) / created_at / used_at(null) / revoked(bool)`
- `users` 加列：`role(string, "user")`、`disabled(bool, false)`、`totp_secret(crypto.EncryptedString)`、`totp_enabled(bool, false)`、`recovery_codes(JSON，存 SHA-256 哈希)`、`registration_invite(string, "")`（待激活行所持邀请码，confirm 时服务端认领）
- 均经 GORM AutoMigrate；首次用户升级迁移时置 `role="owner"`

## 6. 安全设计要点

- `totp_secret` 用现有 `crypto.EncryptedString` 加密存储（同 API Key 机制）
- 恢复码：8 个、单次使用、只存哈希、confirm 时一次性明文下发
- TOTP 校验窗口 ±1 时间步；登录限流覆盖验证码尝试（防爆破）
- 邀请码单次语义由 confirm 阶段事务保证；未 confirm 的半激活账号无法登录，重新绑定即可（其占用的 email 阻止重复注册——重复注册报"邮箱已注册"并引导补绑）
- admin 接口、停用逻辑全部进 `security_regression_test.go` 风格的回归测试
- 删除跨用户收养能力（`AdoptModel`/`FindOrphanClaw402`）——历史 IDOR 隐患源头

## 7. 前端改动清单

- `RegisterPage`：邀请码字段（实例已初始化时显示）→ 两步流程（扫码 → 验证 → 恢复码展示，强调"仅显示一次"）
- `LoginPage`：TOTP 验证码/恢复码输入框；收到 `totp_binding_required` 跳补绑流程
- `AuthContext`：新增补绑流程状态管理
- `SettingsPage`：owner 专属"用户管理"页签（邀请码 + 用户列表 + 停用/恢复）
- 移除：AI 钱包 UI、claw402 选项、`loginAdmin`、`/api/admin-login` 残留
- 文案：FAQ 单运营者表述改为多用户邀请制

## 8. 测试计划

- 邀请码：并发同码竞态（一码一账号）、无效/已用/已作废拒绝、首用户免码
- TOTP：注册 confirm、登录正确/错误码、恢复码一次性、`reset-totp` CLI、存量账号强制补绑
- 权限：普通用户访问 admin 接口 403、停用用户登录拒绝+会话全失效、owner 不能停用自己
- 摘除：claw402 接口 404、env 读取点清零（grep 断言）、无钱包时 trader 全流程（配置→启动→决策）正常
- 回归：`api/security_regression_test.go` 等现有安全测试全绿

## 9. 迁移与部署

- 现有实例：`likeaixi@gmail.com` 迁移为 `owner`；升级后首次登录强制补绑 TOTP
- `.env` 清理：`CLAW402_*`、`NOFX_X402_RECEIPT_RPC_URL` 作废（SERVER.md 同步更新）
- 上线流程：push `dev-allin` → 服务器 `git pull && docker compose up -d --build`
- 上游同步：改动集中在 auth/user/admin/前端认证页 + Claw402 摘除点；后续合并上游安全补丁时重点比对

## 10. 明确范围外（YAGNI）

- 邮箱验证、自助找回密码（有 CLI 兜底）
- 每用户配额/消费上限、审计日志
- 用户级联删除（只做停用）
- `mcp/payment`/`provider/nofxos` 彻底删包（后续可选）
- 前端 localStorage 多账号隔离
