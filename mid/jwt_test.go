package mid

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

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

	if w.Code != http.StatusOK {
		t.Fatalf("expected business error envelope over HTTP 200, got %d", w.Code)
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

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"uid": float64(42)})
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

func TestIdentityGettersDoNotTrustRequestHeaders(t *testing.T) {
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
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("a_uid", "42")
	req.Header.Set("a_rid", "1")
	req.Header.Set("a_nickname", "admin")
	req.Header.Set("a_mobile", "13800000000")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected no content, got %d", w.Code)
	}
}
