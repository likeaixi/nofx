package api

import (
	"encoding/json"
	"strings"
	"testing"

	"nofx/auth"
	"nofx/store"
)

func adminEnv(t *testing.T) (*Server, string, string) {
	securityJWTSecret(t)
	st := securityStore(t)
	owner := storeUser("owner@x.test", store.RoleOwner)
	owner.TotpEnabled = true
	st.User().CreateUser(owner)
	pleb := storeUser("pleb@x.test", store.RoleUser)
	pleb.TotpEnabled = true
	st.User().CreateUser(pleb)
	ownerTok, _ := auth.GenerateJWT(owner.ID, owner.Email, 0)
	plebTok, _ := auth.GenerateJWT(pleb.ID, pleb.Email, 0)
	return NewServer(nil, st, nil, 0), ownerTok, plebTok
}

func TestAdminRequiresOwner(t *testing.T) {
	s, ownerTok, plebTok := adminEnv(t)
	for _, req := range []struct{ method, path, body string }{
		{"POST", "/api/admin/invites", `{}`},
		{"GET", "/api/admin/invites", ``},
		{"DELETE", "/api/admin/invites/NOPEINVITE", ``},
		{"GET", "/api/admin/users", ``},
		{"PATCH", "/api/admin/users/xyz", `{"disabled":false}`},
	} {
		if w := securityRequest(s.router, req.method, req.path, req.body, plebTok); w.Code != 403 {
			t.Fatalf("%s %s as user: %d", req.method, req.path, w.Code)
		}
		if w := securityRequest(s.router, req.method, req.path, req.body, ownerTok); w.Code == 403 {
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
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	code, ok := created["code"].(string)
	if !ok || code == "" {
		t.Fatal("missing invite code in response", created)
	}

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

	w = securityRequest(s.router, "PATCH", "/api/admin/users/"+pleb.ID, `{"disabled":true}`, ownerTok)
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
