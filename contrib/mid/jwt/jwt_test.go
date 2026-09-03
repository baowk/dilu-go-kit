package jwtmid

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestClaimInt64RejectsNonPositiveValuesAcrossTypes(t *testing.T) {
	claims := jwt.MapClaims{
		"number": json.Number("-1"),
		"int64":  int64(-2),
		"int":    -3,
		"string": "-4",
		"valid":  json.Number("5"),
	}
	if got := claimInt64(claims, "number", "int64", "int", "string", "valid"); got != 5 {
		t.Fatalf("claimInt64 = %d, want 5", got)
	}
	if got := claimInt64Slice(jwt.MapClaims{"ids": []int64{-1, 0, 2}}, "ids"); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatalf("claimInt64Slice = %#v", got)
	}
}

func TestJWTIgnoresHeaderUIDUnlessExplicitlyTrusted(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(JWT(JWTConfig{Secret: "secret", HeaderUID: "a_uid"}))
	r.GET("/me", func(c *gin.Context) {
		c.String(http.StatusOK, "uid=%d", GetUID(c))
	})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("a_uid", "42")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected HTTP 401, got %d", w.Code)
	}
	if w.Body.String() == "uid=42" {
		t.Fatalf("HeaderUID was trusted without TrustHeaderUID")
	}
}

func TestJWTTrustsHeaderUIDWhenExplicitlyEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(JWT(JWTConfig{Secret: "secret", HeaderUID: "a_uid", TrustHeaderUID: true}))
	r.GET("/me", func(c *gin.Context) {
		c.String(http.StatusOK, "uid=%d", GetUID(c))
	})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("a_uid", "42")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if got := w.Body.String(); got != "uid=42" {
		t.Fatalf("expected trusted header uid, got %q", got)
	}
}

func TestJWTRejectsTrustedHeaderWhenCIDRConfigIsInvalid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(JWT(JWTConfig{Secret: "secret", HeaderUID: "a_uid", TrustHeaderUID: true, TrustedHeaderCIDRs: []string{"invalid"}}))
	r.GET("/me", func(c *gin.Context) { c.String(http.StatusOK, "uid=%d", GetUID(c)) })
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("a_uid", "42")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("invalid CIDR must not trust header, got status %d", w.Code)
	}
}

func TestJWTQueryTokenDisabledByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(JWT(JWTConfig{Secret: "secret"}))
	r.GET("/ws", func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/ws?token=abc", nil)
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestJWTRejectsUnsignedAlgorithm(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(JWT(JWTConfig{Secret: "secret"}))
	r.GET("/me", func(c *gin.Context) {
		c.String(http.StatusOK, "uid=%d", GetUID(c))
	})

	token := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"uid": float64(42)})
	tokenStr, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Body.String() == "uid=42" {
		t.Fatalf("unsigned token was accepted")
	}
}

func TestJWTAcceptsValidHMACToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(JWT(JWTConfig{Secret: "secret"}))
	r.GET("/me", func(c *gin.Context) {
		c.String(http.StatusOK, "uid=%d", GetUID(c))
	})

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"uid": float64(42),
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenStr, err := token.SignedString([]byte("secret"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if got := w.Body.String(); got != "uid=42" {
		t.Fatalf("expected valid JWT uid, got %q", got)
	}
}

func TestJWTExtractsTenantShopAndScopesFromClaims(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(JWT(JWTConfig{Secret: "secret"}))
	r.GET("/me", func(c *gin.Context) {
		if GetTenantID(c) != 9 {
			t.Fatalf("tenant_id = %d", GetTenantID(c))
		}
		if GetWorkspaceID(c) != 11 {
			t.Fatalf("workspace_id = %d", GetWorkspaceID(c))
		}
		if GetRoleID(c) != 7 {
			t.Fatalf("role_id = %d", GetRoleID(c))
		}
		if !reflect.DeepEqual(GetShopIDs(c), []int64{1, 2, 3}) {
			t.Fatalf("shop_ids = %#v", GetShopIDs(c))
		}
		if !reflect.DeepEqual(GetScopes(c), []string{"order.read", "order.write"}) {
			t.Fatalf("scopes = %#v", GetScopes(c))
		}
		c.String(http.StatusOK, "ok")
	})

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"uid":          float64(42),
		"tenant_id":    float64(9),
		"workspace_id": float64(11),
		"rid":          7,
		"shop_ids":     []any{float64(1), "2", float64(3)},
		"scopes":       "order.read, order.write",
		"exp":          time.Now().Add(time.Hour).Unix(),
	})
	tokenStr, err := token.SignedString([]byte("secret"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if got := w.Body.String(); got != "ok" {
		t.Fatalf("expected ok, got %q", got)
	}
}

func TestJWTTrustsTenantShopAndScopesHeadersWhenUIDTrusted(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(JWT(JWTConfig{
		Secret:            "secret",
		HeaderUID:         "x-user-id",
		HeaderTenantID:    "x-tenant-id",
		HeaderWorkspaceID: "x-workspace-id",
		HeaderShopIDs:     "x-shop-ids",
		HeaderScopes:      "x-scopes",
		TrustHeaderUID:    true,
	}))
	r.GET("/me", func(c *gin.Context) {
		if GetUID(c) != 42 {
			t.Fatalf("uid = %d", GetUID(c))
		}
		if GetTenantID(c) != 9 {
			t.Fatalf("tenant_id = %d", GetTenantID(c))
		}
		if GetWorkspaceID(c) != 11 {
			t.Fatalf("workspace_id = %d", GetWorkspaceID(c))
		}
		if !reflect.DeepEqual(GetShopIDs(c), []int64{1, 2}) {
			t.Fatalf("shop_ids = %#v", GetShopIDs(c))
		}
		if !reflect.DeepEqual(GetScopes(c), []string{"order.read", "inventory.write"}) {
			t.Fatalf("scopes = %#v", GetScopes(c))
		}
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("x-user-id", "42")
	req.Header.Set("x-tenant-id", "9")
	req.Header.Set("x-workspace-id", "11")
	req.Header.Set("x-shop-ids", "1,2")
	req.Header.Set("x-scopes", "order.read,inventory.write")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if got := w.Body.String(); got != "ok" {
		t.Fatalf("expected ok, got %q", got)
	}
}

func TestIdentityGettersDoNotTrustUntrustedHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/me", func(c *gin.Context) {
		if GetUID(c) != 0 {
			t.Fatalf("GetUID trusted request header")
		}
		if GetRoleID(c) != 0 {
			t.Fatalf("GetRoleID trusted request header")
		}
		if GetNickname(c) != "" {
			t.Fatalf("GetNickname trusted request header")
		}
		if GetPhone(c) != "" {
			t.Fatalf("GetPhone trusted request header")
		}
		if GetTenantID(c) != 0 {
			t.Fatalf("GetTenantID trusted request header")
		}
		if GetWorkspaceID(c) != 0 {
			t.Fatalf("GetWorkspaceID trusted request header")
		}
		if GetShopIDs(c) != nil {
			t.Fatalf("GetShopIDs trusted request header")
		}
		if GetScopes(c) != nil {
			t.Fatalf("GetScopes trusted request header")
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("a_uid", "42")
	req.Header.Set("a_rid", "1")
	req.Header.Set("a_nickname", "admin")
	req.Header.Set("a_mobile", "13800000000")
	req.Header.Set("x-tenant-id", "9")
	req.Header.Set("x-workspace-id", "11")
	req.Header.Set("x-shop-ids", "1,2")
	req.Header.Set("x-scopes", "order.read")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected no content, got %d", w.Code)
	}
}

func TestJWTRequiresExpiration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(JWT(JWTConfig{Secret: "secret"}))
	r.GET("/me", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"uid": 42})
	tokenStr, err := token.SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestJWTPreservesInt64Identity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const uid int64 = 9_007_199_254_740_993
	r := gin.New()
	r.Use(JWT(JWTConfig{Secret: "secret"}))
	r.GET("/me", func(c *gin.Context) {
		if got := GetUID(c); got != uid {
			t.Fatalf("uid = %d, want %d", got, uid)
		}
		c.Status(http.StatusNoContent)
	})

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"uid": uid,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenStr, err := token.SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
}
