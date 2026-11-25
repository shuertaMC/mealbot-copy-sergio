// Package auth validates Auth0-issued JSON Web Tokens. The expected issuer,
// audience and JWKS location are supplied through config.Auth0.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/johnamadeo/mealbot/internal/config"
)

// InvalidAccessToken : Message to show if invalid access is invalid
const InvalidAccessToken = "Invalid access token"

// SigningAlgorithm is the only signing algorithm accepted for tokens.
// Restricting this is important to avoid the issues described in
// https://auth0.com/blog/2015/03/31/critical-vulnerabilities-in-json-web-token-libraries/
const SigningAlgorithm = "RS256"

// CustomJWTMiddleware : HTTP Handler w/ authentication capabilities
type CustomJWTMiddleware struct {
	ValidationKeyGetter jwt.Keyfunc
	SigningMethod       jwt.SigningMethod
}

// Handler : Start HTTP server if JWT is valid; else return
func (mw *CustomJWTMiddleware) Handler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := mw.CheckJWT(w, r)
		if err != nil {
			fmt.Println(err)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// CheckJWT : Validate JWT sent to server
func (mw *CustomJWTMiddleware) CheckJWT(
	w http.ResponseWriter,
	r *http.Request,
) error {
	// preflight request
	if r.Method == "OPTIONS" {
		return nil
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return errors.New("No authorization header")
	}

	authStr := strings.Split(authHeader, " ")
	if len(authStr) != 2 || strings.ToLower(authStr[0]) != "bearer" {
		return errors.New("Authorization header format must be Bearer {token}")
	}

	token := authStr[1]
	parser := jwt.NewParser(jwt.WithValidMethods([]string{mw.SigningMethod.Alg()}))
	parsedToken, err := parser.Parse(token, mw.ValidationKeyGetter)
	if err != nil {
		return err
	}

	if !parsedToken.Valid {
		return errors.New("Token is invalid")
	}

	if mw.SigningMethod.Alg() != parsedToken.Header["alg"] {
		return errors.New("Token must use 'alg' signing method")
	}

	return nil
}

// Jwks :
type Jwks struct {
	Keys []JSONWebKeys `json:"keys"`
}

// JSONWebKeys : https://auth0.com/docs/jwks
type JSONWebKeys struct {
	Kty string   `json:"kty"`
	Kid string   `json:"kid"`
	Use string   `json:"use"`
	N   string   `json:"n"`
	E   string   `json:"e"`
	X5c []string `json:"x5c"`
}

// GetFakeAuthHandler : Return handler w/o wrapping it w/ authentication; for testing
func GetFakeAuthHandler(handler http.Handler) http.Handler {
	return handler
}

// NewAuthHandler returns a function that wraps handlers with JWT validation
// against the issuer, audience and JWKS URL in cfg.
func NewAuthHandler(cfg config.Auth0) func(http.Handler) http.Handler {
	return func(handler http.Handler) http.Handler {
		return NewMiddleware(cfg).Handler(handler)
	}
}

// NewMiddleware builds the JWT middleware for the given Auth0 settings.
func NewMiddleware(cfg config.Auth0) *CustomJWTMiddleware {
	return &CustomJWTMiddleware{
		ValidationKeyGetter: func(token *jwt.Token) (interface{}, error) {
			claims, _ := token.Claims.(jwt.MapClaims)

			if err := VerifyAudience(claims, cfg.Audience); err != nil {
				return nil, errors.New("Invalid audience")
			}

			if err := VerifyIssuer(claims, cfg.Issuer); err != nil {
				return nil, errors.New("Invalid issuer")
			}

			cert, err := getPEMCertificate(token, cfg.JWKSURL)
			if err != nil {
				return nil, errors.New("PEM Certificate failed")
			}

			result, err := jwt.ParseRSAPublicKeyFromPEM([]byte(cert))
			if err != nil {
				return nil, errors.New("Failed to parse RSA public key from PEM certificate")
			}

			return result, nil
		},

		// The middleware verifies that tokens are signed with this specific signing algorithm.
		SigningMethod: jwt.SigningMethodRS256,
	}
}

// https://support.quovadisglobal.com/kb/a37/what-is-pem-format.aspx
func getPEMCertificate(token *jwt.Token, jwksURL string) (string, error) {
	cert := ""

	resp, err := http.Get(jwksURL)
	if err != nil {
		return cert, err
	}
	defer resp.Body.Close()

	var jwks = Jwks{}
	err = json.NewDecoder(resp.Body).Decode(&jwks)
	if err != nil {
		return cert, err
	}

	for k := range jwks.Keys {
		if token.Header["kid"] == jwks.Keys[k].Kid && len(jwks.Keys[k].X5c) > 0 {
			cert = "-----BEGIN CERTIFICATE-----\n" +
				jwks.Keys[k].X5c[0] +
				"\n-----END CERTIFICATE-----"
		}
	}

	if cert == "" {
		err := errors.New("Unable to find appropriate key")
		return cert, err
	}

	return cert, nil
}

// VerifyAudience checks that the 'aud' claim is present and contains audience.
// The claim may be a single string or an array of strings (RFC 7519 section 4.1.3).
func VerifyAudience(claims jwt.MapClaims, audience string) error {
	if _, ok := claims["aud"]; !ok {
		return errors.New("No audience claim")
	}

	auds, err := claims.GetAudience()
	if err != nil {
		return errors.New("Invalid audience")
	}

	for _, item := range auds {
		if item == audience {
			return nil
		}
	}

	return errors.New("Invalid audience")
}

// VerifyIssuer checks that the 'iss' claim exactly equals issuer.
func VerifyIssuer(claims jwt.MapClaims, issuer string) error {
	iss, err := claims.GetIssuer()
	if err != nil || iss != issuer {
		return errors.New("Invalid issuer")
	}
	return nil
}
