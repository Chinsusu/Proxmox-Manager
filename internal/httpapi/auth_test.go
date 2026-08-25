package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func generateTestKeyPair(t *testing.T) (*rsa.PrivateKey, *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return key, &key.PublicKey
}

func signTestToken(t *testing.T, priv *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(priv)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func TestJWTAuthenticator_ValidToken(t *testing.T) {
	priv, pub := generateTestKeyPair(t)
	authn := &JWTAuthenticator{PublicKey: pub, ExpectedIssuer: "vm-factory", ExpectedAudience: "vmf-api"}

	token := signTestToken(t, priv, jwt.MapClaims{
		"sub":  "operator-1",
		"role": "operator",
		"iss":  "vm-factory",
		"aud":  "vmf-api",
		"exp":  time.Now().Add(time.Hour).Unix(),
	})

	p, err := authn.Authenticate(token)
	if err != nil {
		t.Fatalf("Authenticate() unexpected error: %v", err)
	}
	if p.Subject != "operator-1" || p.Role != RoleOperator {
		t.Fatalf("unexpected principal: %+v", p)
	}
}

func TestJWTAuthenticator_WrongIssuer(t *testing.T) {
	priv, pub := generateTestKeyPair(t)
	authn := &JWTAuthenticator{PublicKey: pub, ExpectedIssuer: "vm-factory", ExpectedAudience: "vmf-api"}

	token := signTestToken(t, priv, jwt.MapClaims{
		"sub": "operator-1", "role": "operator",
		"iss": "someone-else", "aud": "vmf-api",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	if _, err := authn.Authenticate(token); err == nil {
		t.Fatal("expected error for wrong issuer")
	}
}

func TestJWTAuthenticator_UnknownRole(t *testing.T) {
	priv, pub := generateTestKeyPair(t)
	authn := &JWTAuthenticator{PublicKey: pub, ExpectedIssuer: "vm-factory", ExpectedAudience: "vmf-api"}

	token := signTestToken(t, priv, jwt.MapClaims{
		"sub": "x", "role": "superuser",
		"iss": "vm-factory", "aud": "vmf-api",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	if _, err := authn.Authenticate(token); err == nil {
		t.Fatal("expected error for unknown role claim")
	}
}

func TestJWTAuthenticator_ExpiredToken(t *testing.T) {
	priv, pub := generateTestKeyPair(t)
	authn := &JWTAuthenticator{PublicKey: pub, ExpectedIssuer: "vm-factory", ExpectedAudience: "vmf-api"}

	token := signTestToken(t, priv, jwt.MapClaims{
		"sub": "x", "role": "viewer",
		"iss": "vm-factory", "aud": "vmf-api",
		"exp": time.Now().Add(-time.Hour).Unix(),
	})

	if _, err := authn.Authenticate(token); err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestJWTAuthenticator_WrongSigningKey(t *testing.T) {
	_, pub := generateTestKeyPair(t)
	otherPriv, _ := generateTestKeyPair(t)
	authn := &JWTAuthenticator{PublicKey: pub, ExpectedIssuer: "vm-factory", ExpectedAudience: "vmf-api"}

	token := signTestToken(t, otherPriv, jwt.MapClaims{
		"sub": "x", "role": "viewer",
		"iss": "vm-factory", "aud": "vmf-api",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	if _, err := authn.Authenticate(token); err == nil {
		t.Fatal("expected error: token signed by a different key must not verify")
	}
}

type stubAuthenticator struct {
	principal Principal
	err       error
}

func (s stubAuthenticator) Authenticate(string) (Principal, error) { return s.principal, s.err }

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { t.Fatal("should not reach handler") })
	handler := AuthMiddleware(stubAuthenticator{})(next)

	req := httptest.NewRequest(http.MethodGet, "/v1/instances", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAuthMiddleware_ValidToken_SetsPrincipal(t *testing.T) {
	want := Principal{Subject: "op-1", Role: RoleOperator}
	var got Principal
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = PrincipalFromContext(r.Context())
	})
	handler := AuthMiddleware(stubAuthenticator{principal: want})(next)

	req := httptest.NewRequest(http.MethodGet, "/v1/instances", nil)
	req.Header.Set("Authorization", "Bearer sometoken")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got != want {
		t.Fatalf("principal in context = %+v, want %+v", got, want)
	}
}

func TestRequireRole_Forbidden(t *testing.T) {
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { t.Fatal("should not reach handler") })
	handler := RequireRole(RoleAdmin)(next)

	req := httptest.NewRequest(http.MethodPost, "/v1/instances", nil)
	req = req.WithContext(context.WithValue(req.Context(), principalCtxKey{}, Principal{Subject: "v", Role: RoleViewer}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRequireRole_Allowed(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true })
	handler := RequireRole(RoleAdmin, RoleOperator)(next)

	req := httptest.NewRequest(http.MethodPost, "/v1/instances", nil)
	req = req.WithContext(context.WithValue(req.Context(), principalCtxKey{}, Principal{Subject: "o", Role: RoleOperator}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called || rec.Code != http.StatusOK {
		t.Fatalf("expected handler called with 200, called=%v code=%d", called, rec.Code)
	}
}
