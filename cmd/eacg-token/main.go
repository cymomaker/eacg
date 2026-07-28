// Command eacg-token 为本地示例生成短期 JWT。
package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type claims struct {
	TenantID string   `json:"tenant_id"`
	Roles    []string `json:"roles"`
	jwt.RegisteredClaims
}

// main 读取环境变量并输出本地测试令牌。
func main() {
	secret := envOrDefault("EACG_JWT_SECRET", "0123456789abcdef0123456789abcdef")
	issuer := envOrDefault("EACG_JWT_ISSUER", "eacg-example")
	audience := envOrDefault("EACG_JWT_AUDIENCE", "eacg")
	userID := envOrDefault("EACG_USER_ID", "user-1")
	tenantID := envOrDefault("EACG_TENANT_ID", "tenant-a")
	roles := strings.Fields(envOrDefault("EACG_ROLES", "reader"))

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		TenantID: tenantID,
		Roles:    roles,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	})
	raw, err := token.SignedString([]byte(secret))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(raw)
}

// envOrDefault 读取环境变量，空值时返回默认值。
func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
