package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"nofx/auth"
	"nofx/crypto"
	"nofx/store"

	"github.com/pquerna/otp/totp"
)

type regEnv struct {
	s *Server
}

func newRegEnv(t *testing.T) *regEnv {
	securityJWTSecret(t)
	st := securityStore(t)
	return &regEnv{s: NewServer(nil, st, nil, 0)}
}

func firstRegisterBody() string {
	return `{"email":"owner@x.test","password":"Passw0rd!x","lang":"en"}`
}

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
	if out["role"] != store.RoleOwner {
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
	st.User().CreateUser(storeUser("owner@x.test", store.RoleOwner))
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
	st.User().CreateUser(storeUser("owner@x.test", store.RoleOwner))
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

func storeUser(email, role string) *store.User {
	return &store.User{ID: "u-" + strings.SplitN(email, "@", 2)[0], Email: email,
		PasswordHash: mustHash("Passw0rd!x"), Role: role}
}
func mustHash(pw string) string {
	h, _ := auth.HashPassword(pw)
	return h
}
func mustSecret(s string) crypto.EncryptedString { return crypto.EncryptedString(s) }
