package api

import (
	"encoding/json"
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
