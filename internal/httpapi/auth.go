// Package httpapi chứa middleware và wiring HTTP cho vmf-api: auth/RBAC
// baseline, error envelope, request ID — theo Phần II mục 11 và Phần IX
// mục 9 (RBAC: viewer/operator/admin/service).
package httpapi

import (
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Role là RBAC role baseline P0 theo Phần IX mục 9.
type Role string

// Bốn role RBAC baseline P0 theo Phần IX mục 9.
const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
	RoleService  Role = "service"
)

// Principal là danh tính đã xác thực, gắn vào context sau khi verify JWT.
type Principal struct {
	Subject string
	Role    Role
}

type principalCtxKey struct{}

// PrincipalFromContext đọc principal đã xác thực; ok=false nếu request
// chưa qua AuthMiddleware hoặc là public endpoint.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(Principal)
	return p, ok
}

// Authenticator verify bearer token và trả về Principal.
// Interface hoá để test không cần khoá RSA thật.
type Authenticator interface {
	Authenticate(tokenString string) (Principal, error)
}

// JWTAuthenticator verify JWT ký RS256 bằng public key, kiểm tra
// issuer/audience theo cấu hình auth trong config example.
type JWTAuthenticator struct {
	PublicKey        *rsa.PublicKey
	ExpectedIssuer   string
	ExpectedAudience string
}

// Authenticate implement Authenticator cho JWTAuthenticator.
func (a *JWTAuthenticator) Authenticate(tokenString string) (Principal, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return a.PublicKey, nil
	},
		jwt.WithIssuer(a.ExpectedIssuer),
		jwt.WithAudience(a.ExpectedAudience),
	)
	if err != nil {
		return Principal{}, err
	}

	sub, _ := claims["sub"].(string)
	roleStr, _ := claims["role"].(string)
	if sub == "" || roleStr == "" {
		return Principal{}, errors.New("token missing sub/role claim")
	}

	role := Role(roleStr)
	switch role {
	case RoleViewer, RoleOperator, RoleAdmin, RoleService:
	default:
		return Principal{}, errors.New("unknown role claim")
	}

	return Principal{Subject: sub, Role: role}, nil
}

// AuthMiddleware bắt buộc Authorization: Bearer <jwt> hợp lệ, trả
// ErrorEnvelope 401 nếu thiếu/sai. Endpoint public (health/ready) không
// đi qua middleware này — wiring ở cmd/api/main.go.
func AuthMiddleware(authn Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			tokenString, ok := strings.CutPrefix(header, "Bearer ")
			if !ok || tokenString == "" {
				WriteError(w, r, http.StatusUnauthorized, "AUTH_MISSING_TOKEN", "Authorization: Bearer <token> is required")
				return
			}

			principal, err := authn.Authenticate(tokenString)
			if err != nil {
				WriteError(w, r, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "token invalid or expired")
				return
			}

			ctx := context.WithValue(r.Context(), principalCtxKey{}, principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole chặn request nếu principal không thuộc danh sách role cho phép.
// Dùng sau AuthMiddleware trong chain.
func RequireRole(roles ...Role) func(http.Handler) http.Handler {
	allowed := make(map[Role]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				WriteError(w, r, http.StatusUnauthorized, "AUTH_MISSING_TOKEN", "authentication required")
				return
			}
			if _, permitted := allowed[principal.Role]; !permitted {
				WriteError(w, r, http.StatusForbidden, "AUTH_ROLE_FORBIDDEN", "role does not permit this action")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
