# NOFX 多用户 + TOTP + 摘除 Claw402 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把单 owner 的 NOFX 改造为邀请码多用户系统：强制 TOTP（谷歌验证器）登录、owner 管理用户与邀请码、摘除 Claw402 钱包付费模型路径（用户自带 OpenAI 兼容 API Key）。

**Architecture:** 后端（Go/gin/gorm）新增 `invite_codes` 表与 users 表扩展列（role/disabled/totp 字段），注册改两步（邀请码+密码 → 扫码绑定 TOTP → confirm 激活+发恢复码），登录加 TOTP 校验；owner 管理接口走独立 `/api/admin/*` 路由组。Claw402 只摘用户入口与环境变量读取，内部包保留不注册。前端（React/TS）注册页改两步状态机、登录页加验证码、新增补绑页与 owner 用户管理页签。

**Tech Stack:** Go 1.26 + gin + gorm + `github.com/pquerna/otp`（新依赖）；React + `qrcode.react`（已有）+ vitest。

**Spec:** `docs/superpowers/specs/2026-11-14-multi-user-totp-design.md`（本计划 3.1 的补充修正：`register/confirm` 请求额外携带可选 `invite_code`，用于无状态地在 confirm 阶段原子标记邀请码。Task 10 会把这个修正回写进 spec。）

## Global Constraints

- Go 新依赖只允许 `github.com/pquerna/otp`；不得引入其他后端依赖
- 每个任务先写失败测试再实现（TDD），测试命令统一 `go test ./<pkg>/ -run <TestName> -v`（在仓库根执行）
- 复用现有测试脚手架：`securityStore(t)`（临时 sqlite）、`securityJWTSecret(t)`、`securityRequest(router, method, path, body, token)`（api/security_regression_test.go:129-160）、`NewServer(nil, st, nil, 0)`
- 有效 TOTP 码在测试中用 `totp.GenerateCode(secret, time.Now())` 生成（`github.com/pquerna/otp/totp`）
- 登录类错误必须保持防枚举：错误文案统一，不泄露账号是否存在
- 前端认证页现有代码大量使用硬编码英文文案（RegisterPage 先例），新 UI 同样允许硬编码英文，不强制进 i18n
- 每任务一次 commit，英文 commit message，末尾带 `Co-Authored-By: Claude <noreply@anthropic.com>`
- 前端验证命令：`cd web && npx vitest run <file>` 与 `npm run build`

---

### Task 1: TOTP/恢复码/邀请码工具函数 + 依赖

**Files:**
- Create: `auth/totp.go`
- Create: `auth/totp_test.go`
- Modify: `go.mod` / `go.sum`（go get 自动）

**Interfaces:**
- Consumes: 无
- Produces（后续任务全部依赖，签名固定）:
  - `auth.GenerateTOTP(accountName string) (secret, url string, err error)`
  - `auth.ValidateTOTP(code, secret string) bool`
  - `auth.GenerateRecoveryCodes(count int) ([]string, error)` — 明文码，格式 `XXXX-XXXX`
  - `auth.HashRecoveryCode(code string) string` — SHA-256 hex
  - `auth.GenerateInviteCode() (string, error)` — 10 字符

- [ ] **Step 1: 添加依赖**

```bash
go get github.com/pquerna/otp@latest
```

- [ ] **Step 2: 写失败测试** `auth/totp_test.go`

```go
package auth

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestGenerateTOTP(t *testing.T) {
	secret, url, err := GenerateTOTP("user@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 32 { // base32(SHA1 20-byte seed) = 32 chars
		t.Fatalf("secret length = %d, want 32", len(secret))
	}
	if !strings.HasPrefix(url, "otpauth://totp/NOFX:") || !strings.Contains(url, "secret="+secret) {
		t.Fatalf("url = %q", url)
	}
}

func TestValidateTOTP(t *testing.T) {
	secret, _, err := GenerateTOTP("user@example.test")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !ValidateTOTP(code, secret) {
		t.Fatal("valid code rejected")
	}
	if ValidateTOTP("000000", secret) && code != "000000" {
		t.Fatal("invalid code accepted")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 8 {
		t.Fatalf("got %d codes", len(codes))
	}
	format := regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}$`)
	for _, c := range codes {
		if !format.MatchString(c) {
			t.Fatalf("bad format: %q", c)
		}
	}
	if HashRecoveryCode(codes[0]) == HashRecoveryCode(codes[1]) {
		t.Fatal("collision")
	}
	if len(HashRecoveryCode(codes[0])) != 64 {
		t.Fatal("not sha256 hex")
	}
}

func TestGenerateInviteCode(t *testing.T) {
	a, err := GenerateInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GenerateInviteCode()
	if !regexp.MustCompile(`^[A-Z0-9]{10}$`).MatchString(a) {
		t.Fatalf("bad code: %q", a)
	}
	if a == b {
		t.Fatal("codes must not repeat")
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./auth/ -run 'TestGenerateTOTP|TestValidateTOTP|TestRecoveryCodes|TestGenerateInviteCode' -v`
Expected: FAIL（函数未定义，编译错误）

- [ ] **Step 4: 实现** `auth/totp.go`

```go
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// GenerateTOTP creates a new TOTP secret and otpauth:// URL for the account.
func GenerateTOTP(accountName string) (string, string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "NOFX",
		AccountName: accountName,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// ValidateTOTP checks a 6-digit code with the standard ±1 step window.
func ValidateTOTP(code, secret string) bool {
	return totp.Validate(code, secret)
}

// GenerateRecoveryCodes returns count single-use plaintext codes (XXXX-XXXX).
func GenerateRecoveryCodes(count int) ([]string, error) {
	codes := make([]string, 0, count)
	seen := make(map[string]struct{}, count)
	for len(codes) < count {
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf) // 13 chars A-Z2-7
		code := s[:4] + "-" + s[4:8]
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	return codes, nil
}

// HashRecoveryCode returns the SHA-256 hex digest used for storage.
func HashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

// GenerateInviteCode returns a random 10-character invite code.
func GenerateInviteCode() (string, error) {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return "", errors.New("invite code entropy unavailable")
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)[:10], nil
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./auth/ -v`
Expected: PASS（含 auth 包既有测试）

- [ ] **Step 6: Commit**

```bash
git add auth/totp.go auth/totp_test.go go.mod go.sum
git commit -m "feat(auth): add TOTP, recovery-code and invite-code helpers"
```

---

### Task 2: users 表扩展 + invite_codes 表

**Files:**
- Modify: `store/user.go`（User 结构体、initTables、新方法）
- Create: `store/invite.go`
- Create: `store/invite_test.go`
- Modify: `store/store.go`（注册 InviteStore，模式参照 store/store.go:187-296 的访问器与 initTables）

**Interfaces:**
- Consumes: Task 1 的 `auth.HashRecoveryCode`
- Produces:
  - `store.User` 新字段：`Role string`（json:"role"）、`Disabled bool`、`TotpSecret crypto.EncryptedString`（column:totp_secret）、`TotpEnabled bool`、`RecoveryCodes string`（column:recovery_codes，JSON 哈希数组）
  - `(s *UserStore) CreateUser(user *User) error`
  - `(s *UserStore) UpdateTOTP(userID string, secret crypto.EncryptedString, enabled bool) error`
  - `(s *UserStore) SetRecoveryCodes(userID string, hashes []string) error`
  - `(s *UserStore) ConsumeRecoveryCode(userID, plaintext string) bool`
  - `(s *UserStore) SetDisabled(userID string, disabled bool) error`（停用时 auth_version+1）
  - `(s *UserStore) EnsureSingleOwner() error`（迁移：无 owner 且有用户时，最早用户置 owner）
  - `store.InviteCode{Code string PK; CreatedBy string; UsedBy *string; CreatedAt time.Time; UsedAt *time.Time; Revoked bool}`
  - `(s *Store) Invite() *InviteStore`
  - `(s *InviteStore) Create(code, createdBy string) error` / `List() ([]InviteCode, error)` / `GetValid(code string) (*InviteCode, error)` / `MarkUsed(code, userID string) error` / `Revoke(code string) error`
  - `store.ErrInviteUnavailable = errors.New("invite code unavailable")`

- [ ] **Step 1: 写失败测试** `store/invite_test.go`（覆盖邀请码生命周期 + 用户新方法 + owner 迁移）

```go
package store

import (
	"encoding/json"
	"testing"

	"nofx/auth"
	"nofx/crypto"
)

func newTestUserStore(t *testing.T) *UserStore {
	t.Helper()
	st, err := New(t.TempDir() + "/invite.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.User()
}

func TestInviteLifecycle(t *testing.T) {
	us := newTestUserStore(t)
	inv := us.db.Store(nil) // 见 Step 3 说明：通过 Store 聚合访问；测试中直接构造见下
	_ = inv
}

func TestInviteCodeLifecycle(t *testing.T) {
	st, err := New(t.TempDir() + "/invite.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.Invite().Create("ABC123XYZ9", "owner"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Invite().GetValid("ABC123XYZ9")
	if err != nil || got.Revoked || got.UsedBy != nil {
		t.Fatal(got, err)
	}
	if err := st.Invite().MarkUsed("ABC123XYZ9", "u1"); err != nil {
		t.Fatal(err)
	}
	// 单次语义：第二次标记必须失败
	if err := st.Invite().MarkUsed("ABC123XYZ9", "u2"); err != ErrInviteUnavailable {
		t.Fatalf("want ErrInviteUnavailable, got %v", err)
	}
	if _, err := st.Invite().GetValid("ABC123XYZ9"); err == nil {
		t.Fatal("used code still valid")
	}
	// 作废未用码
	if err := st.Invite().Create("REVOKABLE01", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := st.Invite().Revoke("REVOKABLE01"); err != nil {
		t.Fatal(err)
	}
	if err := st.Invite().MarkUsed("REVOKABLE01", "u1"); err != ErrInviteUnavailable {
		t.Fatalf("revoked code marked used: %v", err)
	}
}

func TestUserTOTPFields(t *testing.T) {
	us := newTestUserStore(t)
	u := &User{ID: "u1", Email: "u1@x.test", PasswordHash: "h", Role: RoleUser}
	if err := us.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	secret := "JBSWY3DPEHPK3PXP"
	if err := us.UpdateTOTP("u1", crypto.EncryptedString(secret), true); err != nil {
		t.Fatal(err)
	}
	got, err := us.GetByID("u1")
	if err != nil || !got.TotpEnabled || got.TotpSecret != crypto.EncryptedString(secret) {
		t.Fatalf("totp roundtrip failed: %+v %v", got, err)
	}
	codes, _ := auth.GenerateRecoveryCodes(3)
	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = auth.HashRecoveryCode(c)
	}
	if err := us.SetRecoveryCodes("u1", hashes); err != nil {
		t.Fatal(err)
	}
	if !us.ConsumeRecoveryCode("u1", codes[0]) {
		t.Fatal("valid recovery code rejected")
	}
	if us.ConsumeRecoveryCode("u1", codes[0]) {
		t.Fatal("recovery code reused")
	}
}

func TestSetDisabledBumpsAuthVersion(t *testing.T) {
	us := newTestUserStore(t)
	if err := us.CreateUser(&User{ID: "u1", Email: "u1@x.test", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := us.SetDisabled("u1", true); err != nil {
		t.Fatal(err)
	}
	got, _ := us.GetByID("u1")
	if !got.Disabled || got.AuthVersion != 1 {
		t.Fatalf("disabled=%v authVersion=%d", got.Disabled, got.AuthVersion)
	}
	if err := us.SetDisabled("u1", false); err != nil {
		t.Fatal(err)
	}
	got, _ = us.GetByID("u1")
	if got.Disabled || got.AuthVersion != 1 {
		t.Fatal("re-enable must not bump again", got)
	}
}

func TestEnsureSingleOwner(t *testing.T) {
	us := newTestUserStore(t)
	if err := us.CreateUser(&User{ID: "legacy", Email: "legacy@x.test", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := us.CreateUser(&User{ID: "new", Email: "new@x.test", PasswordHash: "h", Role: RoleUser}); err != nil {
		t.Fatal(err)
	}
	if err := us.EnsureSingleOwner(); err != nil {
		t.Fatal(err)
	}
	legacy, _ := us.GetByID("legacy")
	newu, _ := us.GetByID("new")
	if legacy.Role != RoleOwner || newu.Role != RoleUser {
		t.Fatalf("owner migration wrong: %q %q", legacy.Role, newu.Role)
	}
	// 幂等
	if err := us.EnsureSingleOwner(); err != nil {
		t.Fatal(err)
	}
	all, _ := us.GetAll()
	owners := 0
	for _, u := range all {
		if u.Role == RoleOwner {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("owners = %d", owners)
	}
}
```

（删除 Step 1 里占位的 `TestInviteLifecycle` 函数，只保留上面四个真实测试。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./store/ -run 'TestInviteCodeLifecycle|TestUserTOTPFields|TestSetDisabledBumpsAuthVersion|TestEnsureSingleOwner' -v`
Expected: FAIL（编译错误：字段/方法不存在）

- [ ] **Step 3: 实现**

`store/user.go` 修改（在现有文件上做以下增量）：

```go
// 新增常量（放 ErrAlreadyInitialized 附近）
const (
	RoleOwner = "owner"
	RoleUser  = "user"
)

// User 结构体追加字段（PasswordHash 之后）：
	Role          string                 `gorm:"not null;default:user" json:"role"`
	Disabled      bool                   `gorm:"not null;default:false" json:"-"`
	TotpSecret    crypto.EncryptedString `gorm:"column:totp_secret;default:''" json:"-"`
	TotpEnabled   bool                   `gorm:"not null;default:false" json:"-"`
	RecoveryCodes string                 `gorm:"column:recovery_codes;default:'[]'" json:"-"`
// import 增加 "nofx/crypto" 与 "encoding/json"

// initTables 的 postgres 分支，在既有 ALTER 列表追加：
	`ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'user'`,
	`ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled BOOLEAN NOT NULL DEFAULT false`,
	`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret TEXT DEFAULT ''`,
	`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN NOT NULL DEFAULT false`,
	`ALTER TABLE users ADD COLUMN IF NOT EXISTS recovery_codes TEXT DEFAULT '[]'`,
// sqlite 分支保持 s.db.AutoMigrate(&User{})（自动加列）
// initTables 末尾（两条分支汇合后）追加 owner 迁移：
	if err := s.EnsureSingleOwner(); err != nil {
		return err
	}

// 新增方法（放在 UpdatePassword 之后）：
func (s *UserStore) CreateUser(user *User) error {
	return s.db.Create(user).Error
}

func (s *UserStore) UpdateTOTP(userID string, secret crypto.EncryptedString, enabled bool) error {
	return s.db.Model(&User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"totp_secret":  secret,
		"totp_enabled": enabled,
		"updated_at":   time.Now().UTC(),
	}).Error
}

func (s *UserStore) SetRecoveryCodes(userID string, hashes []string) error {
	raw, err := json.Marshal(hashes)
	if err != nil {
		return err
	}
	return s.db.Model(&User{}).Where("id = ?", userID).Update("recovery_codes", string(raw)).Error
}

// ConsumeRecoveryCode atomically removes one matching hash; false = not found/already used.
func (s *UserStore) ConsumeRecoveryCode(userID, plaintext string) bool {
	hash := auth.HashRecoveryCode(plaintext)
	return s.db.Transaction(func(tx *gorm.DB) bool {
		var u User
		if err := tx.Where("id = ?", userID).First(&u).Error; err != nil {
			return false
		}
		var hashes []string
		if err := json.Unmarshal([]byte(u.RecoveryCodes), &hashes); err != nil {
			return false
		}
		kept := hashes[:0]
		found := false
		for _, h := range hashes {
			if h == hash && !found {
				found = true
				continue
			}
			kept = append(kept, h)
		}
		if !found {
			return false
		}
		raw, _ := json.Marshal(kept)
		return tx.Model(&User{}).Where("id = ? AND recovery_codes = ?", userID, u.RecoveryCodes).
			Update("recovery_codes", string(raw)).Error == nil
	})
}

func (s *UserStore) SetDisabled(userID string, disabled bool) error {
	updates := map[string]interface{}{"disabled": disabled, "updated_at": time.Now().UTC()}
	if disabled {
		updates["auth_version"] = gorm.Expr("auth_version + 1")
	}
	result := s.db.Model(&User{}).Where("id = ?", userID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *UserStore) EnsureSingleOwner() error {
	var owners int64
	if err := s.db.Model(&User{}).Where("role = ?", RoleOwner).Count(&owners).Error; err != nil {
		return err
	}
	if owners > 0 {
		return nil
	}
	var earliest User
	err := s.db.Order("created_at").First(&earliest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.db.Model(&User{}).Where("id = ?", earliest.ID).Update("role", RoleOwner).Error
}
```

新建 `store/invite.go`：

```go
package store

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

var ErrInviteUnavailable = errors.New("invite code unavailable")

// InviteStore invite-code storage
type InviteStore struct{ db *gorm.DB }

// InviteCode single-use registration invite
type InviteCode struct {
	Code      string     `gorm:"primaryKey;size:16" json:"code"`
	CreatedBy string     `gorm:"not null" json:"created_by"`
	UsedBy    *string    `json:"used_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	Revoked   bool       `gorm:"not null;default:false" json:"revoked"`
}

func (InviteCode) TableName() string { return "invite_codes" }

func NewInviteStore(db *gorm.DB) *InviteStore { return &InviteStore{db: db} }

func (s *InviteStore) initTables() error {
	return s.db.AutoMigrate(&InviteCode{})
}

func (s *InviteStore) Create(code, createdBy string) error {
	return s.db.Create(&InviteCode{Code: code, CreatedBy: createdBy}).Error
}

func (s *InviteStore) List() ([]InviteCode, error) {
	var out []InviteCode
	err := s.db.Order("created_at DESC").Find(&out).Error
	return out, err
}

// GetValid returns the invite only when unused and not revoked.
func (s *InviteStore) GetValid(code string) (*InviteCode, error) {
	var inv InviteCode
	err := s.db.Where("code = ? AND used_by IS NULL AND revoked = ?", code, false).First(&inv).Error
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

// MarkUsed atomically claims an invite for one user; exactly one concurrent caller wins.
func (s *InviteStore) MarkUsed(code, userID string) error {
	now := time.Now().UTC()
	res := s.db.Model(&InviteCode{}).
		Where("code = ? AND used_by IS NULL AND revoked = ?", code, false).
		Updates(map[string]interface{}{"used_by": userID, "used_at": &now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrInviteUnavailable
	}
	return nil
}

// Revoke voids an unused invite.
func (s *InviteStore) Revoke(code string) error {
	res := s.db.Model(&InviteCode{}).
		Where("code = ? AND used_by IS NULL AND revoked = ?", code, false).
		Update("revoked", true)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrInviteUnavailable
	}
	return nil
}
```

`store/store.go`：在 initTables（store/store.go:118）按现有模式追加 `if err := s.inviteStore.initTables(); err != nil { return err }`（结构体字段与 NewXXX 初始化照抄 UserStore 的写法），并新增访问器：

```go
func (s *Store) Invite() *InviteStore { return s.inviteStore }
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./store/ -run 'TestInviteCodeLifecycle|TestUserTOTPFields|TestSetDisabledBumpsAuthVersion|TestEnsureSingleOwner' -v`
Expected: PASS

- [ ] **Step 5: 全量 store 回归**

Run: `go test ./store/ -v 2>&1 | tail -5`
Expected: PASS（无既有测试被破坏）

- [ ] **Step 6: Commit**

```bash
git add store/user.go store/invite.go store/invite_test.go store/store.go
git commit -m "feat(store): multi-user schema — roles, TOTP fields, invite codes"
```

---

### Task 3: 注册改造（邀请码 + 两步 TOTP）

**Files:**
- Modify: `api/handler_user.go`（handleRegister 重写 + 新增 handleRegisterConfirm）
- Modify: `api/server.go:199-201`（新增 confirm 路由）
- Test: `api/handler_register_totp_test.go`（新建）

**Interfaces:**
- Consumes: Task 1 全部 + Task 2 全部；`s.initUserDefaultConfigs(userID, lang)`（api/handler_user.go:216）
- Produces:
  - `POST /api/register` 200 响应体：`{"totp_secret":"...","otpauth_url":"otpauth://...","is_first_user":bool}`（**不再返回 token**）
  - `POST /api/register/confirm` 请求：`{"email","password","totp_code","invite_code?"}`；200 响应：`{"token","user_id","email","role","recovery_codes":[...]}`
  - 错误：409 `Email already registered`、403 `Invite code required` / `Invalid invite code`、401 confirm 校验失败
  - `handleRegisterConfirm(c *gin.Context)` 方法名（server.go 路由注册引用）

- [ ] **Step 1: 写失败测试** `api/handler_register_totp_test.go`

```go
package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"nofx/auth"

	"github.com/pquerna/otp/totp"
)

func registerSetup(t *testing.T) (*Server, *storeStore) { return nil, nil } // 删除本行

type regEnv struct {
	s *Server
}

func newRegEnv(t *testing.T) *regEnv {
	securityJWTSecret(t)
	st := securityStore(t)
	return &regEnv{s: NewServer(nil, st, nil, 0)}
}

func firstRegisterBody() string { return `{"email":"owner@x.test","password":"Passw0rd!x","lang":"en"}` }

// 首用户免邀请码注册 → 返回 totp secret，不返回 token
func TestRegisterFirstUserBootstrap(t *testing.T) {
	env := newRegEnv(t)
	w := securityRequest(env.s.router, "POST", "/api/register", firstRegisterBody(), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["totp_secret"] == "" || resp["totp_secret"] == nil {
		t.Fatal("missing totp_secret", resp)
	}
	if _, has := resp["token"]; has {
		t.Fatal("register must not issue a token before TOTP confirmation")
	}
	if resp["is_first_user"] != true {
		t.Fatal("expected is_first_user=true")
	}
}

func confirmBody(email, password, secret, invite string, code string) string {
	b := map[string]string{"email": email, "password": password, "totp_code": code}
	if invite != "" {
		b["invite_code"] = invite
	}
	raw, _ := json.Marshal(b)
	return string(raw)
}

func validCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// 首用户 confirm 成功 → owner + 恢复码 + 可登录
func TestRegisterConfirmFirstUser(t *testing.T) {
	env := newRegEnv(t)
	w := securityRequest(env.s.router, "POST", "/api/register", firstRegisterBody(), "")
	var reg map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &reg)
	secret := reg["totp_secret"].(string)

	w = securityRequest(env.s.router, "POST", "/api/register/confirm",
		confirmBody("owner@x.test", "Passw0rd!x", secret, "", validCode(t, secret)), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out["role"] != auth.RoleOwner {
		t.Fatal("first confirmed user must be owner", out)
	}
	if codes, ok := out["recovery_codes"].([]interface{}); !ok || len(codes) != 8 {
		t.Fatal("expected 8 recovery codes")
	}
	if out["token"] == "" || out["token"] == nil {
		t.Fatal("confirm must return a token")
	}
}

// 实例初始化后必须携带有效邀请码
func TestRegisterRequiresInviteAfterInit(t *testing.T) {
	env := newRegEnv(t)
	st := env.s.store
	st.User().CreateUser(&storeUser("owner@x.test", auth.RoleOwner))
	st.User().UpdateTOTP("u-owner", mustSecret("KRSXG5CTMVRXEZLU"), true)

	// 无码
	w := securityRequest(env.s.router, "POST", "/api/register",
		`{"email":"a@x.test","password":"Passw0rd!x"}`, "")
	if w.Code != 403 {
		t.Fatal("missing invite must 403", w.Code)
	}
	// 无效码
	w = securityRequest(env.s.router, "POST", "/api/register",
		`{"email":"a@x.test","password":"Passw0rd!x","invite_code":"NOPE"}`, "")
	if w.Code != 403 || !strings.Contains(w.Body.String(), "Invalid invite code") {
		t.Fatal(w.Code, w.Body.String())
	}
	// 有效码 → 200
	st.Invite().Create("GOODCODE99", "u-owner")
	w = securityRequest(env.s.router, "POST", "/api/register",
		`{"email":"a@x.test","password":"Passw0rd!x","invite_code":"GOODCODE99"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// 未 confirm 前码未被标记、且该邮箱不能登录
	inv, err := st.Invite().GetValid("GOODCODE99")
	if err != nil || inv == nil {
		t.Fatal("invite must stay unused until confirm")
	}
}

// 同码并发 confirm 只能成功一个
func TestRegisterConfirmInviteSingleUse(t *testing.T) {
	env := newRegEnv(t)
	st := env.s.store
	st.User().CreateUser(&storeUser("owner@x.test", auth.RoleOwner))
	st.Invite().Create("RACECODE99", "u-owner")

	register := func(email string) (string, map[string]interface{}) {
		w := securityRequest(env.s.router, "POST", "/api/register",
			fmt.Sprintf(`{"email":%q,"password":"Passw0rd!x","invite_code":"RACECODE99"}`, email), "")
		var reg map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &reg)
		return reg["totp_secret"].(string), reg
	}
	s1, _ := register("a@x.test")
	s2, _ := register("b@x.test")

	var wg sync.WaitGroup
	results := make(chan int, 2)
	for i, sec := range []string{s1, s2} {
		email := []string{"a@x.test", "b@x.test"}[i]
		wg.Add(1)
		go func(email, sec string) {
			defer wg.Done()
			w := securityRequest(env.s.router, "POST", "/api/register/confirm",
				confirmBody(email, "Passw0rd!x", sec, "RACECODE99", validCode(t, sec)), "")
			results <- w.Code
		}(email, sec)
	}
	wg.Wait()
	close(results)
	ok := 0
	for code := range results {
		if code == 200 {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("exactly one confirm must win, got %d", ok)
	}
}
```

测试辅助（同文件底部；`storeUser`/`mustSecret` 命名避免与其他测试文件冲突）：

```go
func storeUser(email, role string) *store.User {
	return &store.User{ID: "u-" + strings.SplitN(email, "@", 2)[0], Email: email,
		PasswordHash: mustHash("Passw0rd!x"), Role: role}
}
func mustHash(pw string) string {
	h, _ := auth.HashPassword(pw)
	return h
}
func mustSecret(s string) crypto.EncryptedString { return crypto.EncryptedString(s) }
```

并在文件头 import `"nofx/crypto"` 与 `"nofx/store"`。删除 Step 1 顶部的 `registerSetup` 占位行。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./api/ -run 'TestRegister' -v`
Expected: FAIL（编译错误或行为不符）

- [ ] **Step 3: 实现**

`api/handler_user.go`：重写 `handleRegister`（替换 api/handler_user.go:47-132 的函数体）并新增 confirm：

```go
// handleRegister starts two-step registration. Step 1 verifies the invite (if
// the instance is already initialized), creates a pending user with a fresh
// TOTP secret, and returns the otpauth URL. The account cannot log in until
// step 2 (handleRegisterConfirm) verifies a code from the authenticator.
func (s *Server) handleRegister(c *gin.Context) {
	var req struct {
		Email      string `json:"email" binding:"required,email"`
		Password   string `json:"password" binding:"required,min=8"`
		Lang       string `json:"lang"`
		InviteCode string `json:"invite_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}
	lang := req.Lang
	if lang != "zh" && lang != "id" {
		lang = "en"
	}

	userCount, err := s.store.User().Count()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check user count"})
		return
	}
	isFirst := userCount == 0

	// Invite gate: the untouched first registration claims instance ownership;
	// every later registration needs a valid, unused invite.
	if !isFirst {
		if strings.TrimSpace(req.InviteCode) == "" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Invite code required"})
			return
		}
		if _, err := s.store.Invite().GetValid(strings.TrimSpace(req.InviteCode)); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "Invalid invite code"})
			return
		}
	}

	if _, err := s.store.User().GetByEmail(req.Email); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Email already registered"})
		return
	}
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Password processing failed"})
		return
	}

	secret, url, err := auth.GenerateTOTP(req.Email)
	if err != nil {
		SafeInternalError(c, "Failed to generate authenticator secret", err)
		return
	}

	userID := uuid.New().String()
	role := auth.RoleUser
	if isFirst {
		role = auth.RoleOwner
	}
	user := &store.User{
		ID:           userID,
		Email:        req.Email,
		PasswordHash: passwordHash,
		Role:         role,
		TotpSecret:   crypto.EncryptedString(secret),
	}
	if isFirst {
		// registrationLock serializes the ownership race across processes.
		if err := s.store.User().CreateFirst(user); errors.Is(err, store.ErrAlreadyInitialized) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Invite code required"})
			return
		} else if err != nil {
			SafeInternalError(c, "Failed to create user", err)
			return
		}
	} else if err := s.store.User().CreateUser(user); err != nil {
		SafeInternalError(c, "Failed to create user", err)
		return
	}
	_ = lang // default strategies are created at confirm time

	c.JSON(http.StatusOK, gin.H{
		"totp_secret":  secret,
		"otpauth_url":  url,
		"is_first_user": isFirst,
	})
}

// handleRegisterConfirm activates a pending registration: password + TOTP code
// verified, invite atomically claimed (single use), recovery codes issued.
func (s *Server) handleRegisterConfirm(c *gin.Context) {
	var req struct {
		Email      string `json:"email" binding:"required,email"`
		Password   string `json:"password" binding:"required"`
		TotpCode   string `json:"totp_code" binding:"required"`
		InviteCode string `json:"invite_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}
	user, err := s.store.User().GetByEmail(req.Email)
	if err != nil {
		auth.CheckPassword(req.Password, dummyPasswordHash)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Email or password incorrect"})
		return
	}
	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Email or password incorrect"})
		return
	}
	if user.TotpEnabled {
		c.JSON(http.StatusConflict, gin.H{"error": "Account already activated; use login"})
		return
	}
	if !auth.ValidateTOTP(req.TotpCode, user.TotpSecret.String()) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid verification code"})
		return
	}
	if req.InviteCode != "" {
		if err := s.store.Invite().MarkUsed(strings.TrimSpace(req.InviteCode), user.ID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "Invalid invite code"})
			return
		}
	}
	if err := s.store.User().UpdateTOTP(user.ID, user.TotpSecret, true); err != nil {
		SafeInternalError(c, "Failed to activate account", err)
		return
	}
	codes, err := auth.GenerateRecoveryCodes(8)
	if err != nil {
		SafeInternalError(c, "Failed to generate recovery codes", err)
		return
	}
	hashes := make([]string, len(codes))
	for i, code := range codes {
		hashes[i] = auth.HashRecoveryCode(code)
	}
	if err := s.store.User().SetRecoveryCodes(user.ID, hashes); err != nil {
		SafeInternalError(c, "Failed to store recovery codes", err)
		return
	}
	if err := s.initUserDefaultConfigs(user.ID, "en"); err != nil {
		logger.Infof("Failed to initialize user default configs: %v", err)
	}
	token, err := auth.GenerateJWT(user.ID, user.Email, user.AuthVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token": token, "user_id": user.ID, "email": user.Email,
		"role": user.Role, "recovery_codes": codes,
	})
}
```

注意：`TotpSecret.String()` 的解密依赖 gorm Scan；`UpdateTOTP` 里传回 `user.TotpSecret`（已读出的 EncryptedString）即可。import 增加 `"nofx/crypto"`、`"strings"`（若未有）。同时**删除旧 handleRegister 中创建后直接发 token 与 initUserDefaultConfigs 的调用**（已并入 confirm）。

`api/server.go` 在 authRoutes 组（api/server.go:199-201 区域）追加：

```go
		s.route(authRoutes, "POST", "/register/confirm", "Confirm registration with authenticator code", s.handleRegisterConfirm)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./api/ -run 'TestRegister' -v`
Expected: PASS（4 个新测试）

- [ ] **Step 5: 全量 api 回归（允许 login 相关既有测试失败——Task 4 会改 login）**

Run: `go test ./api/ 2>&1 | tail -15`
Expected: 仅 login/注册相关既有测试可能失败，其余 PASS。若 handler_user_test.go 有注册断言（返回 token），记录失败清单，Task 4 一并修复。

- [ ] **Step 6: Commit**

```bash
git add api/handler_user.go api/server.go api/handler_register_totp_test.go
git commit -m "feat(api): two-step registration with invite codes and TOTP binding"
```

---

### Task 4: 登录 TOTP 校验 + 补绑接口 + CLI reset-totp

**Files:**
- Modify: `api/handler_user.go`（handleLogin 改造 + handleBindTotp/handleBindTotpConfirm）
- Modify: `api/server.go`（authRoutes 追加 bind 路由）
- Modify: `cli.go`（reset-totp 子命令）
- Test: `api/handler_login_totp_test.go`（新建）

**Interfaces:**
- Consumes: Task 1-3
- Produces:
  - `POST /api/login` 请求 `{email,password,totp_code}`；200 `{"token","user_id","email","role"}`；403 `{"error":"totp_binding_required"}`（未绑定）；403 `{"error":"account_disabled"}`；401 凭据/验证码错误（统一文案）
  - `POST /api/auth/bind-totp` `{email,password}` → `{"totp_secret","otpauth_url"}`（重置 secret，可重复调用）
  - `POST /api/auth/bind-totp/confirm` `{email,password,totp_code}` → `{"token","user_id","email","role","recovery_codes"}`
  - CLI：`./nofx reset-totp --email x@y.com`

- [ ] **Step 1: 写失败测试** `api/handler_login_totp_test.go`

```go
package api

import (
	"encoding/json"
	"testing"
	"time"

	"nofx/auth"

	"github.com/pquerna/otp/totp"
)

// 已启用 TOTP 的用户登录：缺码/错码拒绝，对码成功，恢复码可用且一次性
func activatedEnv(t *testing.T) (*Server, string) {
	securityJWTSecret(t)
	st := securityStore(t)
	u := storeUser("u@x.test", auth.RoleUser)
	u.TotpEnabled = true
	u.TotpSecret = mustSecret("JBSXG5DSMVRXEZLU") // 任意合法 base32
	st.User().CreateUser(u)
	// 写入恢复码哈希
	codes, _ := auth.GenerateRecoveryCodes(2)
	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = auth.HashRecoveryCode(c)
	}
	st.User().SetRecoveryCodes(u.ID, hashes)
	return NewServer(nil, st, nil, 0), codes[0]
}

func TestLoginRequiresTOTP(t *testing.T) {
	s, recovery := activatedEnv(t)

	w := securityRequest(s.router, "POST", "/api/login", `{"email":"u@x.test","password":"Passw0rd!x"}`, "")
	if w.Code == 200 {
		t.Fatal("login without code must not succeed")
	}
	w = securityRequest(s.router, "POST", "/api/login",
		`{"email":"u@x.test","password":"Passw0rd!x","totp_code":"000000"}`, "")
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	// 恢复码登录（一次性）
	body := `{"email":"u@x.test","password":"Passw0rd!x","totp_code":"` + recovery + `"}`
	w = securityRequest(s.router, "POST", "/api/login", body, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = securityRequest(s.router, "POST", "/api/login", body, "")
	if w.Code == 200 {
		t.Fatal("recovery code must be single-use")
	}
}

// 未绑定用户 → totp_binding_required
func TestLoginUnboundTOTP(t *testing.T) {
	securityJWTSecret(t)
	st := securityStore(t)
	st.User().CreateUser(storeUser("u@x.test", auth.RoleUser))
	s := NewServer(nil, st, nil, 0)
	w := securityRequest(s.router, "POST", "/api/login",
		`{"email":"u@x.test","password":"Passw0rd!x","totp_code":"123456"}`, "")
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	if out["error"] != "totp_binding_required" {
		t.Fatal(out)
	}
}

// 停用用户拒绝登录
func TestLoginDisabledUser(t *testing.T) {
	securityJWTSecret(t)
	st := securityStore(t)
	u := storeUser("u@x.test", auth.RoleUser)
	u.TotpEnabled = true
	u.TotpSecret = mustSecret("JBSXG5DSMVRXEZLU")
	st.User().CreateUser(u)
	st.User().SetDisabled(u.ID, true)
	s := NewServer(nil, st, nil, 0)
	w := securityRequest(s.router, "POST", "/api/login",
		`{"email":"u@x.test","password":"Passw0rd!x","totp_code":"123456"}`, "")
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}

// 补绑：bind → confirm → 登录成功
func TestBindTOTPFlow(t *testing.T) {
	securityJWTSecret(t)
	st := securityStore(t)
	st.User().CreateUser(storeUser("u@x.test", auth.RoleUser))
	s := NewServer(nil, st, nil, 0)

	w := securityRequest(s.router, "POST", "/api/auth/bind-totp",
		`{"email":"u@x.test","password":"Passw0rd!x"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var bind map[string]any
	json.Unmarshal(w.Body.Bytes(), &bind)
	secret := bind["totp_secret"].(string)

	code, _ := totp.GenerateCode(secret, time.Now())
	w = securityRequest(s.router, "POST", "/api/auth/bind-totp/confirm",
		`{"email":"u@x.test","password":"Passw0rd!x","totp_code":"`+code+`"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// 绑定后正常登录
	code2, _ := totp.GenerateCode(secret, time.Now())
	w = securityRequest(s.router, "POST", "/api/login",
		`{"email":"u@x.test","password":"Passw0rd!x","totp_code":"`+code2+`"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

// 补绑口令错误不得成功
func TestBindTOTPWrongPassword(t *testing.T) {
	securityJWTSecret(t)
	st := securityStore(t)
	st.User().CreateUser(storeUser("u@x.test", auth.RoleUser))
	s := NewServer(nil, st, nil, 0)
	w := securityRequest(s.router, "POST", "/api/auth/bind-totp",
		`{"email":"u@x.test","password":"wrongpass"}`, "")
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./api/ -run 'TestLogin|TestBind' -v`
Expected: FAIL

- [ ] **Step 3: 实现**

`api/handler_user.go` — 替换 `handleLogin`（api/handler_user.go:142-182）：

```go
func (s *Server) handleLogin(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
		TotpCode string `json:"totp_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}
	user, err := s.store.User().GetByEmail(req.Email)
	if err != nil {
		auth.CheckPassword(req.Password, dummyPasswordHash)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Email or password incorrect"})
		return
	}
	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Email or password incorrect"})
		return
	}
	if user.Disabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "account_disabled"})
		return
	}
	if !user.TotpEnabled {
		// Pending registrations and pre-upgrade accounts must (re)bind an
		// authenticator before they can hold a session.
		c.JSON(http.StatusForbidden, gin.H{"error": "totp_binding_required"})
		return
	}
	if req.TotpCode == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Verification code required"})
		return
	}
	if !auth.ValidateTOTP(req.TotpCode, user.TotpSecret.String()) &&
		!s.store.User().ConsumeRecoveryCode(user.ID, req.TotpCode) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid verification code"})
		return
	}
	token, err := auth.GenerateJWT(user.ID, user.Email, user.AuthVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token": token, "user_id": user.ID, "email": user.Email, "role": user.Role,
	})
}

// handleBindTotp re-issues an authenticator secret for an unbound account
// after password verification. Repeat calls replace the pending secret.
func (s *Server) handleBindTotp(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}
	user, err := s.store.User().GetByEmail(req.Email)
	if err != nil || !auth.CheckPassword(req.Password, user.PasswordHash) {
		auth.CheckPassword(req.Password, dummyPasswordHash)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Email or password incorrect"})
		return
	}
	if user.Disabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "account_disabled"})
		return
	}
	secret, url, err := auth.GenerateTOTP(user.Email)
	if err != nil {
		SafeInternalError(c, "Failed to generate authenticator secret", err)
		return
	}
	if err := s.store.User().UpdateTOTP(user.ID, crypto.EncryptedString(secret), false); err != nil {
		SafeInternalError(c, "Failed to store authenticator secret", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"totp_secret": secret, "otpauth_url": url})
}

// handleBindTotpConfirm enables TOTP and issues fresh recovery codes.
func (s *Server) handleBindTotpConfirm(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
		TotpCode string `json:"totp_code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}
	user, err := s.store.User().GetByEmail(req.Email)
	if err != nil || !auth.CheckPassword(req.Password, user.PasswordHash) {
		auth.JSONUnauthorized(c)
		return
	}
	if user.Disabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "account_disabled"})
		return
	}
	if !auth.ValidateTOTP(req.TotpCode, user.TotpSecret.String()) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid verification code"})
		return
	}
	codes, err := auth.GenerateRecoveryCodes(8)
	if err != nil {
		SafeInternalError(c, "Failed to generate recovery codes", err)
		return
	}
	hashes := make([]string, len(codes))
	for i, code := range codes {
		hashes[i] = auth.HashRecoveryCode(code)
	}
	if err := s.store.User().UpdateTOTP(user.ID, user.TotpSecret, true); err != nil ||
		s.store.User().SetRecoveryCodes(user.ID, hashes) != nil {
		SafeInternalError(c, "Failed to enable authenticator", err)
		return
	}
	token, err := auth.GenerateJWT(user.ID, user.Email, user.AuthVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token": token, "user_id": user.ID, "email": user.Email,
		"role": user.Role, "recovery_codes": codes,
	})
}
```

注意：`auth.JSONUnauthorized` 若不存在，直接内联 `c.JSON(http.StatusUnauthorized, gin.H{"error": "Email or password incorrect"})`（保持防枚举统一文案）。

`api/server.go` authRoutes 组追加：

```go
		s.route(authRoutes, "POST", "/auth/bind-totp", "Re-issue authenticator secret (password required)", s.handleBindTotp)
		s.route(authRoutes, "POST", "/auth/bind-totp/confirm", "Confirm authenticator binding", s.handleBindTotpConfirm)
```

`cli.go`：`runCLISubcommand` switch（cli.go:44 附近）加 `case "reset-totp":`；仿照 `reset-password`（cli.go:97-143）实现：

```go
func runResetTOTP(args []string) {
	fs := flag.NewFlagSet("reset-totp", flag.ExitOnError)
	email := fs.String("email", "", "account email")
	dbPath := fs.String("db", "", "override SQLite DB path")
	_ = fs.Parse(args)
	if *email == "" {
		fmt.Fprintln(os.Stderr, "usage: nofx reset-totp --email you@example.com")
		os.Exit(1)
	}
	st, err := openStoreForCLI(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	user, err := st.User().GetByEmail(*email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: account not found\n")
		os.Exit(1)
	}
	if err := st.User().UpdateTOTP(user.ID, crypto.EncryptedString(""), false); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ TOTP reset for %s. The account must re-bind an authenticator on next login (bind-totp flow).\n", user.Email)
}
```

import 增加 `"nofx/crypto"`。

- [ ] **Step 4: 修复 Task 3 Step 5 记录的旧 login/注册断言测试**

旧测试若断言 login 两字段成功：更新为带有效 `totp_code` 或改为断言 403 `totp_binding_required`；逐个按新契约改写并运行。

Run: `go test ./api/ -run 'TestLogin|TestBind|TestRegister' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add api/handler_user.go api/server.go api/handler_login_totp_test.go cli.go
git commit -m "feat(api): TOTP login verification, bind-totp recovery flow, reset-totp CLI"
```

---

### Task 5: owner 管理接口（邀请码 + 用户管理）

**Files:**
- Create: `api/handler_admin.go`
- Create: `api/handler_admin_test.go`
- Modify: `api/server.go`（authMiddleware 下发 role；注册 admin 路由组；`/api/config` 增加 invite_required）
- Modify: `api/server.go` authMiddleware（约 :698 `c.Set("user_id", ...)` 后）

**Interfaces:**
- Consumes: Task 2 store 方法；`auth.RoleOwner`
- Produces:
  - 中间件 `requireOwner()`（读 `c.GetString("role")`）
  - `POST /api/admin/invites` → `{"code":"XXXXXXXXXX"}`；`GET /api/admin/invites` → `{"invites":[InviteCode…]}`；`DELETE /api/admin/invites/:code` → `{"message":"Invite revoked"}`
  - `GET /api/admin/users` → `{"users":[{"id","email","role","disabled","created_at"}]}`；`PATCH /api/admin/users/:id` `{"disabled":bool}` → `{"message":"ok"}`
  - `GET /api/config` 响应新增 `"invite_required": bool`（= 已初始化）
  - authMiddleware 新增 `c.Set("role", user.Role)`

- [ ] **Step 1: 写失败测试** `api/handler_admin_test.go`

```go
package api

import (
	"testing"

	"nofx/auth"
)

func adminEnv(t *testing.T) (*Server, string, string) {
	securityJWTSecret(t)
	st := securityStore(t)
	owner := storeUser("owner@x.test", auth.RoleOwner)
	owner.TotpEnabled = true
	st.User().CreateUser(owner)
	pleb := storeUser("pleb@x.test", auth.RoleUser)
	pleb.TotpEnabled = true
	st.User().CreateUser(pleb)
	ownerTok, _ := auth.GenerateJWT(owner.ID, owner.Email, 0)
	plebTok, _ := auth.GenerateJWT(pleb.ID, pleb.Email, 0)
	return NewServer(nil, st, nil, 0), ownerTok, plebTok
}

func TestAdminRequiresOwner(t *testing.T) {
	s, ownerTok, plebTok := adminEnv(t)
	for _, req := range []struct{ method, path string }{
		{"POST", "/api/admin/invites"}, {"GET", "/api/admin/invites"},
		{"GET", "/api/admin/users"}, {"PATCH", "/api/admin/users/xyz"},
	} {
		if w := securityRequest(s.router, req.method, req.path, `{}`, plebTok); w.Code != 403 {
			t.Fatalf("%s %s as user: %d", req.method, req.path, w.Code)
		}
		if w := securityRequest(s.router, req.method, req.path, `{}`, ownerTok); w.Code == 403 {
			t.Fatalf("%s %s as owner must not 403: %s", req.method, req.path, w.Body.String())
		}
	}
	// 未认证也不行
	if w := securityRequest(s.router, "GET", "/api/admin/users", ``, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
}

func TestAdminInviteLifecycle(t *testing.T) {
	s, ownerTok, _ := adminEnv(t)
	w := securityRequest(s.router, "POST", "/api/admin/invites", `{}`, ownerTok)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// code 从响应读取，列表可见，作废成功，重复作废 404
	var created map[string]any
	jsonUnmarshal(w.Body.Bytes(), &created)
	code := created["code"].(string)

	w = securityRequest(s.router, "GET", "/api/admin/invites", ``, ownerTok)
	if w.Code != 200 || !strings.Contains(w.Body.String(), code) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = securityRequest(s.router, "DELETE", "/api/admin/invites/"+code, ``, ownerTok)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = securityRequest(s.router, "DELETE", "/api/admin/invites/"+code, ``, ownerTok)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestAdminDisableUserKillsSessions(t *testing.T) {
	s, ownerTok, plebTok := adminEnv(t)
	st := s.store
	pleb, _ := st.User().GetByEmail("pleb@x.test")

	// owner 不能停用自己
	owner, _ := st.User().GetByEmail("owner@x.test")
	w := securityRequest(s.router, "PATCH", "/api/admin/users/"+owner.ID, `{"disabled":true}`, ownerTok)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}

	w := securityRequest(s.router, "PATCH", "/api/admin/users/"+pleb.ID, `{"disabled":true}`, ownerTok)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// 旧 token 立即失效（authVersion 已变）
	w = securityRequest(s.router, "GET", "/api/my-traders", ``, plebTok)
	if w.Code != 401 {
		t.Fatal("disabled user session must be invalid", w.Code)
	}
	// 登录拒绝
	w = securityRequest(s.router, "POST", "/api/login",
		`{"email":"pleb@x.test","password":"Passw0rd!x","totp_code":"123456"}`, "")
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	// 恢复
	w = securityRequest(s.router, "PATCH", "/api/admin/users/"+pleb.ID, `{"disabled":false}`, ownerTok)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestConfigExposesInviteRequired(t *testing.T) {
	s, _, _ := adminEnv(t)
	w := securityRequest(s.router, "GET", "/api/config", ``, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"invite_required":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
```

（`jsonUnmarshal` 用标准 `json.Unmarshal`，文件头补 import `"encoding/json"`、`"strings"`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./api/ -run 'TestAdmin|TestConfigExposes' -v`
Expected: FAIL

- [ ] **Step 3: 实现**

新建 `api/handler_admin.go`：

```go
package api

import (
	"errors"
	"net/http"

	"nofx/auth"
	"nofx/logger"
	"nofx/store"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// requireOwner gates owner-only admin routes. authMiddleware must have set "role".
func (s *Server) requireOwner() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("role") != auth.RoleOwner {
			c.JSON(http.StatusForbidden, gin.H{"error": "Owner access required"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) handleCreateInvite(c *gin.Context) {
	code, err := auth.GenerateInviteCode()
	if err != nil {
		SafeInternalError(c, "Failed to generate invite code", err)
		return
	}
	if err := s.store.Invite().Create(code, c.GetString("user_id")); err != nil {
		SafeInternalError(c, "Failed to store invite code", err)
		return
	}
	logger.Infof("✉️  Invite %s created by owner %s", code, c.GetString("email"))
	c.JSON(http.StatusOK, gin.H{"code": code})
}

func (s *Server) handleListInvites(c *gin.Context) {
	invites, err := s.store.Invite().List()
	if err != nil {
		SafeInternalError(c, "Failed to list invites", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"invites": invites})
}

func (s *Server) handleRevokeInvite(c *gin.Context) {
	if err := s.store.Invite().Revoke(c.Param("code")); errors.Is(err, store.ErrInviteUnavailable) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invite not found or already used"})
		return
	} else if err != nil {
		SafeInternalError(c, "Failed to revoke invite", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Invite revoked"})
}

func (s *Server) handleAdminListUsers(c *gin.Context) {
	users, err := s.store.User().GetAll()
	if err != nil {
		SafeInternalError(c, "Failed to list users", err)
		return
	}
	out := make([]gin.H, 0, len(users))
	for _, u := range users {
		out = append(out, gin.H{
			"id": u.ID, "email": u.Email, "role": u.Role,
			"disabled": u.Disabled, "created_at": u.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"users": out})
}

func (s *Server) handleAdminSetUserDisabled(c *gin.Context) {
	targetID := c.Param("id")
	if targetID == c.GetString("user_id") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "You cannot disable your own account"})
		return
	}
	var req struct {
		Disabled *bool `json:"disabled" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "disabled (bool) is required")
		return
	}
	err := s.store.User().SetDisabled(targetID, *req.Disabled)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	if err != nil {
		SafeInternalError(c, "Failed to update user", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "User updated"})
}
```

`api/server.go`：
1. authMiddleware（:698 附近）在 `c.Set("email", user.Email)` 后加 `c.Set("role", user.Role)`。
2. protected 组内新增：

```go
			admin := protected.Group("/", s.requireOwner())
			s.route(admin, "POST", "/admin/invites", "Create invite code", s.handleCreateInvite)
			s.route(admin, "GET", "/admin/invites", "List invite codes", s.handleListInvites)
			s.route(admin, "DELETE", "/admin/invites/:code", "Revoke unused invite code", s.handleRevokeInvite)
			s.route(admin, "GET", "/admin/users", "List users", s.handleAdminListUsers)
			s.route(admin, "PATCH", "/admin/users/:id", "Enable/disable user", s.handleAdminSetUserDisabled)
```

3. `handleGetSystemConfig`（api/server.go:465-473）改为：

```go
func (s *Server) handleGetSystemConfig(c *gin.Context) {
	userCount, _ := s.store.User().Count()
	c.JSON(http.StatusOK, gin.H{
		"initialized":      userCount > 0,
		"invite_required":  userCount > 0,
		"btc_eth_leverage": 10,
		"altcoin_leverage": 5,
	})
}
```

- [ ] **Step 4: 跑测试确认通过 + 全量回归**

Run: `go test ./api/ -run 'TestAdmin|TestConfigExposes' -v && go test ./api/ 2>&1 | tail -5`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add api/handler_admin.go api/handler_admin_test.go api/server.go
git commit -m "feat(api): owner admin API — invites and user management"
```

---

### Task 6: Claw402 后端摘除

**Files:**
- Delete: `api/handler_onboarding.go`（先确认 `walletAddressFromPrivateKey` 无其他引用）
- Modify: `api/server.go`（删 onboarding 两条路由、`/wallet/export` 路由、`/vergex/*` 7 条路由）
- Modify: `kernel/engine.go:197-210`、`provider/nofxos/client_resolve.go:19`、`provider/vergex/client.go:128`（删 CLAW402 env 兜底）
- Modify: `main.go:115`（`List("default")` → 全体用户统计）
- Modify: `store/ai_model.go`（删 `GetAnyEnabled`、`FindOrphanClaw402`、`AdoptModel` 及其引用测试）
- Modify: `api/handler_user.go`（createDefaultStrategies 的 vergex_signal 默认改为 OI top）
- Modify: `store/store.go` initTables（迁移：删 `ai_models` 中 `provider='claw402'` 行）
- Modify: `.env.example`（删 `NOFX_X402_RECEIPT_RPC_URL` 与 CLAW402 说明）
- Test: `api/claw402_removal_test.go`

**Interfaces:**
- Consumes: 无新依赖
- Produces: `/api/onboarding/beginner*`、`/api/wallet/export`、`/api/vergex/*` 全部 404；`grep -r "CLAW402_WALLET" --include="*.go"` 仅允许命中 `mcp/payment`、`provider/nofxos`（未注册的死包）

- [ ] **Step 1: 写失败测试** `api/claw402_removal_test.go`

```go
package api

import (
	"testing"
)

func TestClaw402UserFacingRoutesRemoved(t *testing.T) {
	securityJWTSecret(t)
	st := securityStore(t)
	s := NewServer(nil, st, nil, 0)
	token, _ := authGenerateToken(t, st)
	for _, path := range []string{
		"/api/onboarding/beginner",
		"/api/onboarding/beginner/current",
		"/api/wallet/export",
		"/api/vergex/direction-change/leaderboard",
		"/api/vergex/cost-liquidation-heatmap",
	} {
		for _, method := range []string{"GET", "POST"} {
			if w := securityRequest(s.router, method, path, `{}`, token); w.Code != 404 {
				t.Fatalf("%s %s = %d, want 404", method, path, w.Code)
			}
		}
	}
}

func TestDefaultStrategyNotVergex(t *testing.T) {
	securityJWTSecret(t)
	st := securityStore(t)
	s := NewServer(nil, st, nil, 0)
	_ = s
	// 直接调用策略创建并断言 coin source
	if err := createDefaultStrategiesFor("user1", "en", st); err != nil {
		t.Fatal(err)
	}
	var strat store.Strategy
	if err := st.GormDB().Where("user_id = ?", "user1").First(&strat).Error; err != nil {
		t.Fatal(err)
	}
	cfg, err := strat.ParseConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CoinSource.SourceType == "vergex_signal" || cfg.CoinSource.VergexLimit > 0 {
		t.Fatalf("default strategy still depends on vergex: %+v", cfg.CoinSource)
	}
	if !cfg.CoinSource.UseOITop {
		t.Fatal("default coin source should fall back to OI top (free)")
	}
}
```

实现说明：`authGenerateToken`/`createDefaultStrategiesFor` 是本任务引入的小助手——前者包一层 `auth.GenerateJWT(...)`（用临时用户），后者把 `createDefaultStrategies` 从 `(s *Server)` 抽成可独立调用的 `func createDefaultStrategiesFor(userID, lang string, st *store.Store) error`（handler_user.go 内部转调），便于测试。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./api/ -run 'TestClaw402|TestDefaultStrategy' -v`
Expected: FAIL

- [ ] **Step 3: 实现摘除**

1. `grep -rn "walletAddressFromPrivateKey\|queryBeginnerWalletBalance\|handleBeginnerOnboarding\|handleCurrentBeginnerWallet" api/ --include="*.go" | grep -v _test` — 确认仅 handler_onboarding.go 与 server.go 路由引用，然后：
   - server.go 删除两行 onboarding 路由（:217-218）、`protected.POST("/wallet/export", ...)`（:214）、7 条 `/vergex/*` 路由（:231-237 附近）
   - 删除 `api/handler_onboarding.go`；若 `api/handler_wallet.go` 仅为 wallet export 服务（grep 确认无其他 handler 引用），一并删除
2. `kernel/engine.go:197-210`：删除读 `CLAW402_WALLET_KEY` 的 fallback 分支——`NewStrategyEngine` 在缺钱包参数时返回明确错误 `"claw402 wallet removed; configure a custom model API key"`（保留函数签名，调用方传参路径不变；实际新用户走 custom provider 不会进入该分支）
3. `provider/nofxos/client_resolve.go:19`、`provider/vergex/client.go:128`：删除 `os.Getenv("CLAW402_WALLET_KEY")` 兜底，改为返回错误 `"CLAW402_WALLET_KEY removed"`（这两个包已不可达，保证编译通过即可）
4. `mcp/providers.go`、`mcp/registry.go`：若存在 claw402/nofxos provider 注册调用，注释/删除注册行（保留包代码）
5. `main.go:115`：`st.Trader().List("default")` 改为：

```go
	allTraders, err := st.Trader().ListAll()
	if err == nil {
		logger.Infof("🤖 Loaded %d trader(s) from store", len(allTraders))
	}
```

6. `store/ai_model.go`：删除 `GetAnyEnabled`（:166）、`FindOrphanClaw402`（:60）、`AdoptModel`（:71）三个方法；`grep -rn "GetAnyEnabled\|FindOrphanClaw402\|AdoptModel" --include="*.go"` 清理所有引用（测试文件同步删除对应用例）
7. `store/user.go`：删除 `EnsureAdmin`（:181-192）
8. `api/handler_user.go` `setClaw402Strategy`（:255-284）：`SourceType = "oi_top"`、`UseOITop = true`、删除 `VergexLimit/VergexMarketType/VergexChain` 赋值；策略名/描述里的 "Claw402" 改为 "NOFX Auto Strategy"（三语言）
9. `store/store.go` initTables 末尾追加迁移：

```go
	if err := s.db.Where("provider = ?", "claw402").Delete(&AIModel{}).Error; err != nil {
		return err
	}
```

10. `.env.example`：删除 `NOFX_X402_RECEIPT_RPC_URL` 段落及注释
11. `grep -rn "persistBeginnerWalletEnv\|resolvePlanningSetupWallet" api/ --include="*.go" | grep -v _test` — `resolvePlanningSetupWallet` 被 planning setup（Frontier）引用：这些调用点改为返回错误 `"AI wallet onboarding removed; configure a custom model in Config"`（不删 planning 本体）

- [ ] **Step 4: 编译 + 测试**

Run: `go build ./... && go test ./api/ -run 'TestClaw402|TestDefaultStrategy' -v`
Expected: 编译通过，PASS

- [ ] **Step 5: 全量回归**

Run: `go test ./... 2>&1 | grep -v "^ok\|no test files" | head -20`
Expected: 无 FAIL；planning/claw402 相关旧测试若断言钱包行为，按"入口已摘除"新契约改写或删除

- [ ] **Step 6: env 兜底清零断言**

Run: `grep -rn "CLAW402_WALLET" --include="*.go" . | grep -v "mcp/payment\|provider/nofxos\|_test"`
Expected: 无输出（退出码 1）

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor: remove Claw402 user-facing paths and wallet env fallbacks"
```

---

### Task 7: 前端 — 注册两步 TOTP + 登录验证码 + 补绑页

**Files:**
- Modify: `web/src/lib/config.ts`（SystemConfig + invite_required）
- Modify: `web/src/contexts/AuthContext.tsx`（register 拆两步、login 加 totpCode、删 loginAdmin、User 加 role）
- Modify: `web/src/components/auth/RegisterPage.tsx`（两步状态机 + 邀请码输入）
- Modify: `web/src/components/auth/LoginPage.tsx`（验证码输入 + totp_binding_required 跳转）
- Create: `web/src/components/auth/BindTotpPage.tsx`
- Modify: `web/src/router/paths.ts`（+ `/bind-totp`）与路由注册处（`web/src/router/` 下使用 ROUTES 的文件，grep `ROUTES.login` 定位）
- Modify: `web/src/components/auth/RegisterPage.test.tsx`（适配两步）

**Interfaces:**
- Consumes: Task 3/4/5 的 API 契约（register/confirm/login/bind-totp/config.role）
- Produces:
  - `AuthContext.register(email, password, inviteCode?) → Promise<{success, message?, data?: {totp_secret, otpauth_url}}>`
  - `AuthContext.confirmRegister(email, password, code, inviteCode?) → Promise<{success, message?, data?: {token, user_id, email, role, recovery_codes}}>`
  - `AuthContext.login(email, password, totpCode?) → Promise<{success, message?, needsBinding?: boolean}>`
  - `User { id, email, role }`

- [ ] **Step 1: 更新 config lib**（`web/src/lib/config.ts`）

```ts
export interface SystemConfig {
  initialized: boolean
  invite_required?: boolean
  beta_mode?: boolean
}
```

- [ ] **Step 2: AuthContext 改造**（关键 diff，替换 register/login、删 loginAdmin）

```ts
interface User {
  id: string
  email: string
  role?: string
}

// login:
const login = async (email: string, password: string, totpCode?: string) => {
  try {
    const response = await fetch('/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password, totp_code: totpCode }),
    })
    const data = await response.json()
    if (response.ok && data.token) {
      const userInfo = { id: data.user_id, email: data.email, role: data.role }
      handlePostAuthSuccess(data.token, userInfo)
      return { success: true, message: data.message }
    }
    if (response.status === 403 && data.error === 'totp_binding_required') {
      return { success: false, needsBinding: true }
    }
    return { success: false, message: data.error }
  } catch {
    return { success: false, message: 'Login failed, please try again' }
  }
}

// register（第 1 步，不再自动登录）:
const register = async (email: string, password: string, inviteCode?: string) => {
  const body: Record<string, string> = { email, password, lang: language }
  if (inviteCode) body.invite_code = inviteCode
  try {
    const result = await httpClient.post<{
      totp_secret: string
      otpauth_url: string
      is_first_user: boolean
    }>('/api/register', body)
    if (result.success && result.data) {
      return { success: true, data: result.data }
    }
    return { success: false, message: result.message || 'Registration failed' }
  } catch (error) {
    return {
      success: false,
      message: error instanceof Error ? error.message : 'Registration failed',
    }
  }
}

// confirmRegister（第 2 步）:
const confirmRegister = async (
  email: string,
  password: string,
  code: string,
  inviteCode?: string
) => {
  const body: Record<string, string> = { email, password, totp_code: code }
  if (inviteCode) body.invite_code = inviteCode
  try {
    const result = await httpClient.post<{
      token: string
      user_id: string
      email: string
      role: string
      recovery_codes: string[]
    }>('/api/register/confirm', body)
    if (result.success && result.data) {
      const userInfo = {
        id: result.data.user_id,
        email: result.data.email,
        role: result.data.role,
      }
      handlePostAuthSuccess(result.data.token, userInfo)
      return { success: true, data: result.data }
    }
    return { success: false, message: result.message || 'Confirmation failed' }
  } catch (error) {
    return {
      success: false,
      message: error instanceof Error ? error.message : 'Confirmation failed',
    }
  }
}
```

AuthContextType 接口同步更新；删除 `loginAdmin` 函数与 Provider value 中的引用；`handlePostAuthSuccess` 的 `auth_user` localStorage 写入已含 role（userInfo 带 role）。

- [ ] **Step 3: RegisterPage 两步状态机**（替换 `handleRegister` 并扩展 UI；新增 state）

```tsx
import { QRCodeSVG } from 'qrcode.react'

type Step = 'form' | 'totp' | 'recovery'

// 新增 state：
const [inviteCode, setInviteCode] = useState('')
const [inviteRequired, setInviteRequired] = useState(false)
const [step, setStep] = useState<Step>('form')
const [totpUrl, setTotpUrl] = useState('')
const [totpCode, setTotpCode] = useState('')
const [recoveryCodes, setRecoveryCodes] = useState<string[]>([])

// useEffect 里追加：
setInviteRequired(config.invite_required === true)
// 删除 registrationEnabled 的关闭逻辑：注册始终开放（initialized 后需邀请码），
// 即删除 `if (!registrationEnabled) return <RegistrationDisabled />`，
// RegistrationDisabled.tsx 若无其他引用可删除。

// 第 1 步提交：
const handleRegister = async (e: React.FormEvent) => {
  e.preventDefault()
  if (!passwordValid) return setError(t('passwordNotMeetRequirements', language))
  if (inviteRequired && !inviteCode.trim()) return setError('An invite code is required')
  setLoading(true)
  const result = await register(email, password, inviteCode.trim() || undefined)
  setLoading(false)
  if (!result.success || !result.data) {
    setError(result.message || 'Registration failed')
    return
  }
  setTotpUrl(result.data.otpauth_url)
  setStep('totp')
}

// 第 2 步确认：
const handleConfirmTotp = async (e: React.FormEvent) => {
  e.preventDefault()
  if (totpCode.trim().length !== 6) return setError('Enter the 6-digit code')
  setLoading(true)
  const result = await confirmRegister(email, password, totpCode.trim(), inviteCode.trim() || undefined)
  setLoading(false)
  if (!result.success || !result.data) {
    setError(result.message || 'Confirmation failed')
    return
  }
  setRecoveryCodes(result.data.recovery_codes)
  setStep('recovery')
}
```

JSX：`step === 'form'` 渲染现有表单 +（`inviteRequired` 时）邀请码输入框（复用现有 betaCode 输入框样式，label "Invite code"，maxLength 10）；`step === 'totp'` 渲染 `<QRCodeSVG value={totpUrl} size={180}/>` + 6 位验证码输入 + Confirm 按钮 + "无法扫码？手动输入密钥" 提示；`step === 'recovery'` 渲染恢复码列表（等宽字体 + 复制按钮 + "这些恢复码仅显示一次"警告）+ "进入工作台" 按钮（`navigate(getPostAuthPath())`——已自动登录）。

- [ ] **Step 4: LoginPage 加验证码**

```tsx
const [totpCode, setTotpCode] = useState('')
// handleLogin 内：
const result = await login(email, password, totpCode.trim() || undefined)
if (result.needsBinding) {
  navigate('/bind-totp', { state: { email, password } })
  return
}
```

JSX 在密码框后加同风格验证码输入框（label "Authenticator code"，placeholder "6 位验证码或恢复码"）。

- [ ] **Step 5: BindTotpPage**（新建，完整组件）

```tsx
import React, { useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { QRCodeSVG } from 'qrcode.react'
import { toast } from 'sonner'
import { httpClient } from '../../lib/httpClient'
import { BrandLogo } from '../common/BrandLogo'
import { DeepVoidBackground } from '../common/DeepVoidBackground'

export function BindTotpPage() {
  const location = useLocation() as { state?: { email?: string; password?: string } }
  const navigate = useNavigate()
  const [email, setEmail] = useState(location.state?.email ?? '')
  const [password, setPassword] = useState(location.state?.password ?? '')
  const [totpUrl, setTotpUrl] = useState('')
  const [totpCode, setTotpCode] = useState('')
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const handleBind = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(''); setLoading(true)
    try {
      const result = await httpClient.post<{ totp_secret: string; otpauth_url: string }>(
        '/api/auth/bind-totp', { email, password })
      if (result.success && result.data) {
        setTotpUrl(result.data.otpauth_url)
      } else {
        setError(result.message || 'Failed to start binding')
      }
    } finally { setLoading(false) }
  }

  const handleConfirm = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(''); setLoading(true)
    try {
      const result = await httpClient.post<{
        token: string; user_id: string; email: string; role: string
        recovery_codes: string[]
      }>('/api/auth/bind-totp/confirm', { email, password, totp_code: totpCode.trim() })
      if (result.success && result.data) {
        setRecoveryCodes(result.data.recovery_codes)
      } else {
        setError(result.message || 'Invalid code')
      }
    } finally { setLoading(false) }
  }

  const inputCls = 'w-full bg-nofx-bg border border-[rgba(25,36,52,0.14)] rounded-lg px-4 py-3 text-sm focus:border-nofx-gold focus:ring-1 focus:ring-nofx-gold/50 outline-hidden text-nofx-text font-sans'

  return (
    <DeepVoidBackground className="min-h-screen flex items-center justify-center py-12 font-sans nofx-auth" disableAnimation>
      <div className="w-full max-w-md px-6 relative z-10">
        <div className="flex justify-center mb-6"><BrandLogo /></div>
        <h1 className="text-2xl font-bold text-nofx-text mb-2 text-center">Bind your authenticator</h1>
        <p className="text-nofx-text-muted text-sm text-center mb-6">
          Two-factor authentication is required. Scan the QR code with Google Authenticator.
        </p>
        {!totpUrl && (
          <form onSubmit={handleBind} className="space-y-4">
            <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="Email" className={inputCls} required />
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="Password" className={inputCls} required />
            {error && <div className="text-xs bg-nofx-danger/10 border border-nofx-danger/30 text-nofx-danger px-3 py-2 rounded-lg">{error}</div>}
            <button type="submit" disabled={loading} className="w-full bg-nofx-gold text-nofx-bg font-bold py-3 rounded-lg text-sm">{loading ? '…' : 'Continue'}</button>
          </form>
        )}
        {totpUrl && !recoveryCodes && (
          <form onSubmit={handleConfirm} className="space-y-4">
            <div className="flex justify-center bg-white p-3 rounded-lg w-fit mx-auto"><QRCodeSVG value={totpUrl} size={180} /></div>
            <input value={totpCode} onChange={(e) => setTotpCode(e.target.value.replace(/\D/g, ''))} placeholder="6-digit code" maxLength={6} className={inputCls + ' text-center tracking-widest'} required />
            {error && <div className="text-xs bg-nofx-danger/10 border border-nofx-danger/30 text-nofx-danger px-3 py-2 rounded-lg">{error}</div>}
            <button type="submit" disabled={loading} className="w-full bg-nofx-gold text-nofx-bg font-bold py-3 rounded-lg text-sm">{loading ? '…' : 'Confirm'}</button>
          </form>
        )}
        {recoveryCodes && (
          <div className="space-y-4">
            <div className="bg-nofx-danger/10 border border-nofx-danger/30 text-nofx-danger text-xs px-3 py-2 rounded-lg">
              Save these recovery codes now — they are shown only once.
            </div>
            <div className="bg-nofx-bg-lighter border border-[rgba(25,36,52,0.14)] rounded-lg p-4 font-mono text-sm grid grid-cols-2 gap-2">
              {recoveryCodes.map((c) => <div key={c} className="text-nofx-text">{c}</div>)}
            </div>
            <button
              onClick={() => { toast.success('Saved'); navigate('/') }}
              className="w-full bg-nofx-gold text-nofx-bg font-bold py-3 rounded-lg text-sm"
            >
              I saved them — continue
            </button>
          </div>
        )}
        <div className="text-center mt-6">
          <button onClick={() => navigate('/login')} className="text-xs text-nofx-text-muted hover:text-nofx-gold">Back to login</button>
        </div>
      </div>
    </DeepVoidBackground>
  )
}
```

路由：`router/paths.ts` 加 `bindTotp: '/bind-totp'`，在路由注册文件（`grep -rn "ROUTES.login\|/login" web/src/router/`）旁加同款 `<Route>`。

- [ ] **Step 6: 前端测试**

Run: `cd web && npx vitest run src/components/auth/ 2>&1 | tail -15`
Expected: PASS（RegisterPage.test.tsx 按两步契约更新后）

- [ ] **Step 7: 构建验证**

Run: `cd web && npm run build 2>&1 | tail -5`
Expected: 构建成功（tsc 无错误）

- [ ] **Step 8: Commit**

```bash
git add web/src
git commit -m "feat(web): two-step TOTP registration, login verification and bind-totp page"
```

---

### Task 8: 前端 — Settings 用户管理页签（owner）

**Files:**
- Create: `web/src/components/settings/AdminUsersPanel.tsx`
- Modify: `web/src/pages/SettingsPage.tsx`（Tab 类型 + 'users' 页签 + owner 可见性）
- Test: `web/src/components/settings/AdminUsersPanel.test.tsx`

**Interfaces:**
- Consumes: Task 5 admin API；`useAuth()` 的 `user.role`；`httpClient`
- Produces: `AdminUsersPanel`（无 props）

- [ ] **Step 1: 写失败测试** `web/src/components/settings/AdminUsersPanel.test.tsx`

```tsx
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { AdminUsersPanel } from './AdminUsersPanel'

vi.mock('../../lib/httpClient', () => ({
  httpClient: {
    get: vi.fn(),
    post: vi.fn(),
    del: vi.fn(),
    patch: vi.fn(),
  },
}))

import { httpClient } from '../../lib/httpClient'

describe('AdminUsersPanel', () => {
  beforeEach(() => vi.clearAllMocks())

  it('renders invite and user lists from the admin API', async () => {
    ;(httpClient.get as any).mockImplementation((url: string) =>
      Promise.resolve({
        success: true,
        data:
          url === '/api/admin/invites'
            ? { invites: [{ code: 'ABC123', created_by: 'o', revoked: false, created_at: '2026-11-14' }] }
            : { users: [{ id: 'u1', email: 'a@x.test', role: 'owner', disabled: false, created_at: '2026-11-14' }] },
      })
    )
    render(<AdminUsersPanel />)
    await waitFor(() => expect(screen.getByText('a@x.test')).toBeTruthy())
    expect(screen.getByText('ABC123')).toBeTruthy()
  })
})
```

（若 `httpClient` 无 `del/patch` 方法，改用它实际的删除/更新封装——先 `grep -n "del\|patch\|delete\|put" web/src/lib/httpClient.ts` 确认，按实际方法名调整。）

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/components/settings/AdminUsersPanel.test.tsx`
Expected: FAIL（组件不存在）

- [ ] **Step 3: 实现 AdminUsersPanel**

```tsx
import React, { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'
import { httpClient } from '../../lib/httpClient'
import { useAuth } from '../../contexts/AuthContext'

interface InviteRow { code: string; used_by?: string; revoked: boolean; created_at: string }
interface UserRow { id: string; email: string; role: string; disabled: boolean; created_at: string }

export function AdminUsersPanel() {
  const { user } = useAuth()
  const [invites, setInvites] = useState<InviteRow[]>([])
  const [users, setUsers] = useState<UserRow[]>([])
  const [busy, setBusy] = useState(false)

  const reload = useCallback(async () => {
    const [inv, usr] = await Promise.all([
      httpClient.get<{ invites: InviteRow[] }>('/api/admin/invites'),
      httpClient.get<{ users: UserRow[] }>('/api/admin/users'),
    ])
    if (inv.success && inv.data) setInvites(inv.data.invites ?? [])
    if (usr.success && usr.data) setUsers(usr.data.users ?? [])
  }, [])

  useEffect(() => { void reload() }, [reload])

  const createInvite = async () => {
    setBusy(true)
    try {
      const r = await httpClient.post<{ code: string }>('/api/admin/invites', {})
      if (r.success && r.data) {
        toast.success(`Invite created: ${r.data.code}`)
        await reload()
      } else toast.error(r.message || 'Failed')
    } finally { setBusy(false) }
  }

  const revokeInvite = async (code: string) => {
    await httpClient.del(`/api/admin/invites/${code}`)
    await reload()
  }

  const toggleUser = async (u: UserRow) => {
    const r = await httpClient.patch(`/api/admin/users/${u.id}`, { disabled: !u.disabled })
    if (!r.success) { toast.error(r.message || 'Failed'); return }
    await reload()
  }

  return (
    <div className="space-y-8">
      <section>
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-sm font-bold text-nofx-text">邀请码</h3>
          <button onClick={createInvite} disabled={busy}
            className="bg-nofx-gold text-nofx-bg text-xs font-bold px-3 py-1.5 rounded-lg">
            生成邀请码
          </button>
        </div>
        <div className="space-y-1">
          {invites.length === 0 && <p className="text-xs text-nofx-text-muted">暂无邀请码</p>}
          {invites.map((i) => (
            <div key={i.code} className="flex items-center justify-between bg-nofx-bg-deeper border border-[rgba(25,36,52,0.14)] rounded-lg px-3 py-2">
              <span className="font-mono text-sm text-nofx-text tracking-widest">{i.code}</span>
              <span className="text-[10px] text-nofx-text-muted">
                {i.revoked ? '已作废' : i.used_by ? '已使用' : '未使用'}
              </span>
              {!i.used_by && !i.revoked && (
                <button onClick={() => revokeInvite(i.code)} className="text-[10px] text-nofx-danger hover:underline">作废</button>
              )}
            </div>
          ))}
        </div>
      </section>

      <section>
        <h3 className="text-sm font-bold text-nofx-text mb-3">用户</h3>
        <div className="space-y-1">
          {users.map((u) => (
            <div key={u.id} className="flex items-center justify-between bg-nofx-bg-deeper border border-[rgba(25,36,52,0.14)] rounded-lg px-3 py-2">
              <div>
                <div className="text-sm text-nofx-text">{u.email}</div>
                <div className="text-[10px] text-nofx-text-muted">{u.role}{u.disabled ? ' · 已停用' : ''}</div>
              </div>
              {u.id !== user?.id && (
                <button onClick={() => toggleUser(u)}
                  className={`text-[10px] px-2 py-1 rounded ${u.disabled ? 'text-nofx-gold' : 'text-nofx-danger'} hover:underline`}>
                  {u.disabled ? '恢复' : '停用'}
                </button>
              )}
            </div>
          ))}
        </div>
      </section>
    </div>
  )
}
```

SettingsPage：Tab 类型与数组追加 `{ key: 'users', label: '用户' }`（仅 `user?.role === 'owner'` 时渲染该 tab 按钮）；渲染区加：

```tsx
{activeTab === 'users' && <AdminUsersPanel />}
```

- [ ] **Step 4: 测试 + 构建**

Run: `cd web && npx vitest run src/components/settings/ && npm run build 2>&1 | tail -3`
Expected: PASS + 构建成功

- [ ] **Step 5: Commit**

```bash
git add web/src
git commit -m "feat(web): owner user management tab with invites"
```

---

### Task 9: 前端 Claw402 清理

**Files:**（grep 驱动，逐处处置）

- [ ] **Step 1: 摸底**

```bash
cd web && grep -rln "claw402\|Claw402\|CLAW402\|beginner_wallet\|beginnerWallet" src/ | sort
```

- [ ] **Step 2: 逐类处置**（对每个命中文件执行对应动作）

| 命中 | 动作 |
|---|---|
| `src/lib/onboarding.ts` 的 `nofx_beginner_wallet_address` key | 删除该 key 及其读写函数；`nofx_user_mode`/`getPostAuthPath` 保留 |
| `src/components/auth/OnboardingModeSelector.tsx`、`BeginnerOnlyExperience.test.tsx` 中涉及"AI 钱包引导"的文案/分支 | 删除钱包相关分支，保留 beginner/advanced 模式选择本体 |
| AuthContext 中 `localStorage.removeItem('nofx_beginner_wallet_address')`（register 内） | 随 Task 7 重写已删除；此处 grep 验证无残留 |
| 模型配置 UI（SettingsPage models tab 或独立组件）中 provider 选项含 `claw402` | 删除该选项，仅保留 `custom`（grep `'claw402'` 于 provider 列表/映射处） |
| `src/data/faqData.ts:504` 单运营者 FAQ | 改写为："实例由 owner 运营，注册需邀请码；每个用户的数据（模型密钥、交易所、trader）相互隔离。" |
| i18n 文案键含 wallet/claw402 且已无 UI 引用 | 删除对应键（`grep -rn "钱包\|wallet" src/i18n/translations/` 核对引用后） |

- [ ] **Step 3: 清理后摸底断言**

```bash
cd web && grep -rn "claw402\|Claw402" src/ | grep -v "faqData\|i18n" ; echo "exit=$?"
```
Expected: exit=1（无残留；faqData/i18n 中仅允许出现改写后的运营说明文案）

- [ ] **Step 4: 测试 + 构建**

Run: `cd web && npx vitest run 2>&1 | tail -5 && npm run build 2>&1 | tail -3`
Expected: 全部 PASS + 构建成功（BeginnerOnlyExperience.test.tsx 若测的是钱包引导，删除对应用例）

- [ ] **Step 5: Commit**

```bash
git add web/src
git commit -m "refactor(web): strip Claw402 wallet onboarding UI"
```

---

### Task 10: 全量回归、文档与部署清单

**Files:**
- Modify: `docs/superpowers/specs/2026-11-14-multi-user-totp-design.md`（回写 3.1 修正：confirm 携带 invite_code）
- Modify: `.env.example`（已在 Task 6 处理，此处核对无 CLAW402 残留）
- Modify: `SERVER.md`（部署流程增量：升级后所有账号需走 bind-totp；新增 reset-totp 运维命令）

- [ ] **Step 1: 后端全量测试**

Run: `go test ./... 2>&1 | grep -v "^ok\|no test files" | head -20`
Expected: 空（无 FAIL）

- [ ] **Step 2: 前端全量测试 + 构建**

Run: `cd web && npx vitest run 2>&1 | tail -5 && npm run build 2>&1 | tail -3`
Expected: PASS + 构建成功

- [ ] **Step 3: spec 回写**

在 spec §3.1 的 confirm 请求体中加 `invite_code?`（与 Task 3 实现一致），标注"(2026-11-14 修订：无状态 confirm 需重携邀请码)"。

- [ ] **Step 4: SERVER.md 增量**

追加"多用户运维"一节：升级步骤（`git pull && docker compose up -d --build`）、升级后首次登录会强制 bind-totp、`docker exec nofx-trading ./nofx reset-totp --email x@y.com` 兜底、邀请码在 Settings→用户 页签生成。

- [ ] **Step 5: Commit + push（部署待用户确认后执行）**

```bash
git add -A
git commit -m "docs: multi-user TOTP rollout notes"
git push origin dev-allin
```

- [ ] **Step 6: 部署（需用户确认后执行）**

```bash
ssh -i ~/.ssh/btcnode.pem ubuntu@18.139.149.138 'cd ~/nofx-src && git pull origin dev-allin && docker compose up -d --build 2>&1 | tail -3'
# 验证：
# 1) curl -s https://hyperaitrade.com/api/health → {"status":"ok"}
# 2) 旧账号 likeaixi@gmail.com 登录 → 应跳 bind-totp 补绑流程 → 绑定后进 Settings 见"用户"页签
# 3) 生成邀请码 → 无痕窗口注册新用户走两步流程 → 新用户无"用户"页签（403）
```

---

## Self-Review 记录

- **Spec 覆盖**：§3.1-3.6 → Task 3/4/5；§4 全部 → Task 6/9；§5-6 → Task 2/3；§7 → Task 7/8/9；§8 → 各任务测试 + Task 10；§9 → Task 10。§4.2"内部包保留"与 Task 6 Step 3-4 一致（保留 mcp/payment、provider/nofxos 代码，仅摘注册与 env 读取）。§10 范围外未越界（未做配额/级联删除/邮箱验证）。
- **占位符**：无 TBD/TODO；Task 9 为 grep 驱动的清理表，每行给了处置动作与断言命令。
- **类型一致性**：`auth.RoleOwner/RoleUser`、`store.ErrInviteUnavailable`、`ConsumeRecoveryCode(userID, plaintext) bool`、`register/confirm` 响应字段在 Task 3/4/5/7 间已对齐；前端 `User.role`、`login.needsBinding` 在 Task 5/7/8 一致。
