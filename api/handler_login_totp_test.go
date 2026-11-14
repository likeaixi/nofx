package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nofx/auth"
	"nofx/store"

	"github.com/pquerna/otp/totp"
)

// activatedEnv returns a server whose single user has TOTP enabled plus two
// stored recovery-code hashes; the second return value is one recovery code.
func activatedEnv(t *testing.T) (*Server, string) {
	securityJWTSecret(t)
	st := securityStore(t)
	u := storeUser("u@x.test", store.RoleUser)
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

// 已启用 TOTP 的用户登录：缺码/错码拒绝，对码成功，恢复码可用且一次性
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
	st.User().CreateUser(storeUser("u@x.test", store.RoleUser))
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
	u := storeUser("u@x.test", store.RoleUser)
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
	st.User().CreateUser(storeUser("u@x.test", store.RoleUser))
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
	st.User().CreateUser(storeUser("u@x.test", store.RoleUser))
	s := NewServer(nil, st, nil, 0)
	w := securityRequest(s.router, "POST", "/api/auth/bind-totp",
		`{"email":"u@x.test","password":"wrongpass"}`, "")
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
}

// 绕过注册 confirm、经补绑流程完成激活也必须消耗邀请码——否则一个码可无限开户
func TestBindTOTPFlowConsumesInvite(t *testing.T) {
	securityJWTSecret(t)
	st := securityStore(t)
	st.User().CreateUser(storeUser("owner@x.test", store.RoleOwner))
	st.Invite().Create("BINDCODE99", "u-owner")
	s := NewServer(nil, st, nil, 0)

	// 只走注册第一步（拿码不 confirm），随后改走补绑流程完成激活
	w := securityRequest(s.router, "POST", "/api/register",
		`{"email":"a@x.test","password":"Passw0rd!x","invite_code":"BINDCODE99"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = securityRequest(s.router, "POST", "/api/auth/bind-totp",
		`{"email":"a@x.test","password":"Passw0rd!x"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var bind map[string]any
	json.Unmarshal(w.Body.Bytes(), &bind)
	secret := bind["totp_secret"].(string)

	w = securityRequest(s.router, "POST", "/api/auth/bind-totp/confirm",
		confirmBody("a@x.test", "Passw0rd!x", validCode(t, secret)), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}

	// 邀请码必须已被服务端认领，不能再赞助第二个账号
	if inv, err := st.Invite().GetValid("BINDCODE99"); err == nil || inv != nil {
		t.Fatal("invite survived bind-totp activation", inv, err)
	}
	w = securityRequest(s.router, "POST", "/api/register",
		`{"email":"b@x.test","password":"Passw0rd!x","invite_code":"BINDCODE99"}`, "")
	if w.Code != 403 || !strings.Contains(w.Body.String(), "Invalid invite code") {
		t.Fatal(w.Code, w.Body.String())
	}

	// 已被本账号消耗过的码不得锁死自己：CLI reset-totp 后的重新补绑仍可完成
	var row store.User
	if err := st.GormDB().Where("email = ?", "a@x.test").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	st.User().UpdateTOTP(row.ID, mustSecret(secret), false) // 模拟 nofx reset-totp
	w = securityRequest(s.router, "POST", "/api/auth/bind-totp",
		`{"email":"a@x.test","password":"Passw0rd!x"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &bind)
	secret2 := bind["totp_secret"].(string)
	w = securityRequest(s.router, "POST", "/api/auth/bind-totp/confirm",
		confirmBody("a@x.test", "Passw0rd!x", validCode(t, secret2)), "")
	if w.Code != 200 {
		t.Fatal("self-claimed invite must not wedge CLI-reset re-bind", w.Code, w.Body.String())
	}
}

// 已启用 TOTP 的账号：重绑必须提供当前第二因子（TOTP 码或恢复码），仅密码不得重置
func TestBindTOTPEnabledAccountRequiresCurrentFactor(t *testing.T) {
	s, _ := activatedEnv(t) // 首套恢复码会被中途 confirm 重置，改用下发的新码
	const currentSecret = "JBSXG5DSMVRXEZLU"
	bind := func(currentCode string) *httptest.ResponseRecorder {
		body := `{"email":"u@x.test","password":"Passw0rd!x"`
		if currentCode != "" {
			body += `,"current_totp_code":"` + currentCode + `"`
		}
		body += `}`
		return securityRequest(s.router, "POST", "/api/auth/bind-totp", body, "")
	}

	// 仅密码 → 401；错误验证码 → 401
	if w := bind(""); w.Code != 401 {
		t.Fatal("password-only bind on enabled account must 401", w.Code, w.Body.String())
	}
	if w := bind("000000"); w.Code != 401 {
		t.Fatal("wrong current code must 401", w.Code, w.Body.String())
	}

	// 正确当前 TOTP 码 → 200（密钥被替换，账号回到未启用态）
	w := bind(validCode(t, currentSecret))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	secret2 := out["totp_secret"].(string)

	// 重新启用（confirm 会下发一套全新恢复码），取其中一枚做当前因子
	w = securityRequest(s.router, "POST", "/api/auth/bind-totp/confirm",
		confirmBody("u@x.test", "Passw0rd!x", validCode(t, secret2)), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var conf map[string]any
	json.Unmarshal(w.Body.Bytes(), &conf)
	fresh, _ := conf["recovery_codes"].([]any)
	if len(fresh) == 0 {
		t.Fatal("confirm must return recovery codes")
	}
	rc := fresh[0].(string)
	wbr := bind(rc)
	if wbr.Code != 200 {
		t.Fatal("valid recovery code must authorize re-bind", wbr.Code, wbr.Body.String())
	}
	json.Unmarshal(wbr.Body.Bytes(), &out)
	secret3 := out["totp_secret"].(string)

	// 已消耗的恢复码不能再次作为当前因子
	if w := securityRequest(s.router, "POST", "/api/auth/bind-totp/confirm",
		confirmBody("u@x.test", "Passw0rd!x", validCode(t, secret3)), ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := bind(rc); w.Code != 401 {
		t.Fatal("consumed recovery code must not re-authorize re-bind", w.Code, w.Body.String())
	}
}

// 已启用账号直接打 confirm：无当前密钥者过不了新码校验（廉价健全性检查）
func TestBindTOTPConfirmEnabledAccountWrongCode(t *testing.T) {
	s, _ := activatedEnv(t)
	w := securityRequest(s.router, "POST", "/api/auth/bind-totp/confirm",
		confirmBody("u@x.test", "Passw0rd!x", "000000"), "")
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
}
